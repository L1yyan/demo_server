# roomserver 接入单地图碰撞方案

## 1. 需求理解

当前 Unity 已经导出了 `collision.json`，它是服务端物理用的地图碰撞数据。用户希望把该文件从项目根目录移动到规范位置，并让 roomserver 的 PhysX 后端在创建房间时加载这张地图，用静态碰撞体参与玩家移动和 raycast 判定。

本游戏当前只有一张地图，因此第一版不做多地图选择流程，默认加载 `map_001` 的服务端碰撞文件。

## 2. 当前代码现状

已检查相关代码：

- `collision.json` 位于项目根目录，未被纳入规范资源目录
- `src/roomserver/service/server.go` 根据配置创建 `physx.Factory`
- `src/roomserver/logic/room_manager.go` 每个房间通过 `PhysicsWorldFactory.NewWorld(roomID)` 创建独立物理世界
- `src/roomserver/physx/world.go` 当前只创建 PhysX world、玩家 capsule、默认地面，不加载地图
- `src/roomserver/physx/physx_bridge.cc` 当前 C ABI 只支持玩家 actor、移动 sweep、raycast，不支持静态地图 actor
- `src/roomserver/config/config.go` 当前没有地图碰撞文件路径配置

## 3. 影响范围

预计修改或新增：

- `collision.json` -> `configs/maps/map_001/collision.json`
  - 把当前服务端地图碰撞文件移动到规范目录
- `src/roomserver/config/config.go`
  - 增加默认地图 ID 和地图碰撞文件路径配置
- `config/config.yaml`
  - 同步补充 roomserver 地图配置，作为部署配置示例
- `src/roomserver/physx/types.go`
  - 增加地图碰撞路径等 PhysX 配置字段
- `src/roomserver/service/server.go`
  - 创建 PhysX factory 时传入地图碰撞路径
- `src/roomserver/physx/map_collision.go` 或同包相近文件
  - 新增 JSON 加载、结构体定义、路径解析和数据校验
- `src/roomserver/physx/world.go`
  - `NewWorld` 创建 scene 后加载地图静态碰撞体
- `src/roomserver/physx/physx_bridge.h`
  - 新增 C ABI：向 world 添加静态 box 碰撞体
- `src/roomserver/physx/physx_bridge.cc`
  - 创建 `PxRigidStatic + PxBoxGeometry` 并加入 scene
- `src/roomserver/physx/world_test.go`
  - 增加带 PhysX tag 的地图碰撞验证
- `src/roomserver/physx/map_collision_test.go`
  - 增加纯 Go 的 JSON 解析与校验测试
- `src/roomserver/PHYSX_FLOW.md` 或 `src/roomserver/README.md`
  - 更新当前已支持地图静态碰撞的说明

## 4. 设计方案

### 4.1 地图文件位置

采用以下目录：

```text
configs/
  maps/
    map_001/
      collision.json
```

理由：

- 这是服务端运行资源，不是客户端表现资源
- 后续如果再有地图，可以自然扩展为 `configs/maps/<map_id>/collision.json`
- 当前只有一张地图，默认配置直接指向 `configs/maps/map_001/collision.json`

### 4.2 roomserver 配置

在 roomserver config 中增加：

```go
DefaultMapID       string // 默认地图ID
MapCollisionPath  string // 地图碰撞文件路径
```

默认值：

```text
DefaultMapID = "map_001"
MapCollisionPath = "configs/maps/map_001/collision.json"
```

当前 `cmd/main.go` 仍然使用 `roomconfig.DefaultConfig()`，因此默认值会立即生效，不依赖统一 YAML 加载器。

### 4.3 地图 JSON 加载

在 `physx` 包内加载服务端碰撞文件，结构按当前 `collision.json` 设计：

```text
map_id
map_version
physics_hash
units
rotation
colliders[]
spawn_points[]
```

第一版实现重点支持当前文件里的 `shape: "box"`：

- `position`: 世界坐标
- `rotation`: Unity 导出的 `[x,y,z,w]` 四元数
- `size`: box 的完整尺寸，传给 PhysX 前转换为 half extents
- `is_trigger`: 第一版静态碰撞只处理 `false`；如果遇到 `true`，先跳过或保留为后续触发区能力

由于当前导出的文件全部是 box，第一版不引入 sphere/capsule 创建逻辑，避免在暂时没有测试数据的情况下写错 capsule 轴向。若 JSON 中出现暂不支持的 shape，启动或创建房间时返回明确错误，避免静默漏碰撞。

### 4.4 PhysX 静态碰撞体创建

C ABI 增加类似函数：

```c
int px_world_add_static_box(
    px_world* world,
    px_vec3 position,
    px_quat rotation,
    px_vec3 size,
    char* err,
    int err_len
);
```

C++ 内部流程：

```text
校验 world / position / rotation / size
-> PxBoxGeometry(size.x/2, size.y/2, size.z/2)
-> PxTransform(position, rotation)
-> PxCreateStatic(...)
-> scene->addActor
-> actor->release
```

`scene->addActor` 后释放本地引用，scene 会持有 actor 生命周期，最终随 scene 释放。

### 4.5 房间创建流程

保持现有每房间独立 PhysX world 模式：

```text
RoomManager.getOrCreateRoom
  -> physicsFactory.NewWorld(roomID)
  -> px_world_create
  -> load configs/maps/map_001/collision.json
  -> add static boxes to scene
  -> NewRoom(..., physicsWorld)
```

这样每个房间都有自己的地图静态碰撞体，不会跨房间串扰。

### 4.6 玩家出生点

当前 roomserver 玩家入房时默认 `X/Y/Z = 0`，本次先不改入房协议和匹配流程。JSON 中已有 `spawn_points`，本次只完成解析和校验，不立即改变玩家出生逻辑。

原因：出生点分配涉及玩家队伍、座位或房间内第几个玩家，当前 room token 和 room manager 没有传 map/team/slot 信息。后续可以在房间内按玩家加入顺序使用 `spawn_a`、`spawn_b`。

## 5. 兼容性影响

- 客户端协议不变，仍然发送现有 `JoinRoom` 和 `PlayerInput`
- room token 不变，暂不增加 `map_id`
- roomserver 默认运行行为会变化：玩家移动会被地图墙体、建筑、箱子等静态 box 阻挡
- raycast 会命中地图静态碰撞体；静态物体目前没有可返回的字符串 ID，`TargetID` 仍主要用于玩家命中
- 如果地图 JSON 丢失、格式错误、尺寸非法或存在不支持的 shape，PhysX world 创建失败，玩家入房会失败并返回已有 `join_failed` 错误

## 6. 健壮性

- 地图路径支持相对路径和绝对路径；相对路径按项目根目录解析，避免从 `src/roomserver/cmd` 或 `bin` 启动时找不到文件
- 加载时校验 `map_id`、`units`、`rotation`、collider 数组和关键数值是否有效
- box 的 `size` 必须为正数，position/rotation/size 都必须是有限数
- 四元数如果长度非法则报错，不传入 C++
- 不支持的 shape 明确报错，不静默忽略，避免服务端地图缺块导致穿墙
- 任意一个静态 collider 创建失败则释放已创建的 world，并返回错误，避免半初始化物理场景
- 继续保持 PhysX scene 只在房间 goroutine 创建后由房间流程访问，避免并发访问 scene

## 7. 性能考虑

- 地图碰撞只在房间创建时加载，不在每帧读取 JSON
- 当前约百级 box 碰撞体，每个房间创建时逐个 C ABI 调用可接受
- 玩家移动仍是每 tick 一次 `MovePlayer`，新增地图碰撞会进入 PhysX broadphase，运行时不需要 Go 层遍历 collider
- 第一版不做 mesh collider，避免复杂 cooking 和高面数查询成本
- 默认地面 plane 可以暂时保留；当前 JSON 里也有 `bg_ground`，后续确认无问题后可把 `physics_ground_plane` 改为 false，避免重复地面

## 8. 验证方式

实现后执行：

1. `gofmt` 格式化修改过的 Go 文件
2. `go test ./src/roomserver/...`
   - 验证非 PhysX tag 下普通 roomserver 包和 JSON 解析测试
3. `go test -tags physx ./src/roomserver/physx`
   - 验证 PhysX 后端可以加载测试地图并阻挡玩家移动或被 raycast 命中
4. `go build -tags physx ./src/roomserver/cmd`
   - 验证 roomserver PhysX 构建通过
5. 如本机 PhysX SDK 可用，再执行 `scripts/build_all.sh`
   - 验证全服务构建

如果 PhysX SDK 缺失或外部库链接失败，会明确报告对应命令失败原因。

## 9. 自我审查

发现的问题与修正：

1. 不能只把 `collision.json` 放到目录里，否则 PhysX scene 仍然没有地图 actor；最终方案同时补加载器和 C++ 静态 box 创建
2. 一开始考虑直接支持 box/sphere/capsule，但当前文件全是 box，capsule 轴向和缩放容易在没有测试样本时写错；最终方案第一版只支持 box，并对其他 shape 报错
3. 不能让 service 或 logic 直接解析 PhysX 细节，否则会破坏分层；最终方案把地图碰撞加载放在 `physx` 包内，logic 仍只依赖 `PhysicsWorldFactory`
4. 不能依赖当前进程工作目录，否则从不同目录启动 roomserver 会找不到地图；最终方案做项目根目录解析
5. 不能在地图加载失败时降级到无地图物理，否则用户会误以为服务端已经做了墙体判定；最终方案失败即拒绝创建 PhysX world
6. 当前 spawn_points 已有数据，但直接改出生逻辑会牵涉玩家座位和匹配流程；最终方案先解析保留，不在本次改变入房行为

## 10. 最终执行方案

确认后我会按以下顺序实施：

1. 创建 `configs/maps/map_001/`，移动根目录 `collision.json` 到该目录
2. 给 roomserver 配置增加默认地图 ID 和碰撞文件路径，并同步 `config/config.yaml`
3. 在 `physx` 包新增地图碰撞 JSON 结构、路径解析和校验
4. 扩展 PhysX C ABI 和 C++ bridge，支持添加静态 box actor
5. 修改 PhysX factory/world 创建流程，在每个房间 world 创建时加载 `map_001` 静态碰撞
6. 增加 JSON 解析测试和 PhysX 静态碰撞测试
7. 更新 roomserver 相关文档中的地图碰撞说明
8. 执行 gofmt、测试和构建验证，并报告结果

等待用户确认后再修改业务代码。
