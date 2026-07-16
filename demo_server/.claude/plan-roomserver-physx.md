# roomserver 直接接入 PhysX 实现方案

## 需求理解

用户希望 roomserver 不再停留在 `SimplePhysicsWorld` 占位实现，而是直接接入真实 NVIDIA PhysX，用于后续移动碰撞、射线检测、AOI 遮挡和射击命中。

当前项目状态：

- `src/roomserver/logic/physics.go` 已定义 `PhysicsWorld` 接口
- `Room` 已通过接口依赖物理世界
- 当前 `SimplePhysicsWorld` 是空实现，`Raycast` 永远不命中
- 当前仓库和系统路径没有发现 PhysX SDK、`PxPhysicsAPI.h` 或 PhysX 动态库

因此本次接入需要分两步：

1. 先接入 PhysX SDK 的 native wrapper 和 Go 封装
2. 再把 roomserver 启动时的 physics 实现从 `SimplePhysicsWorld` 切换为 PhysX 实现

## 关键前置条件

需要确认或准备 PhysX SDK：

- PhysX 版本：建议 PhysX 5.x
- SDK 路径：例如 `/opt/physx` 或项目外部路径
- 头文件：需要能找到 `PxPhysicsAPI.h`
- 动态库或静态库：至少需要 PhysX Foundation、PhysX、PhysXExtensions、PhysXPvdSDK，后续 cooking 还需要 PhysXCooking
- 运行时库路径：Linux 下需要 `LD_LIBRARY_PATH` 或 rpath

当前环境没有搜到 PhysX SDK，所以直接写强依赖 cgo 代码会导致 `go test ./...` 失败。为保证项目默认可编译，建议用 build tag 控制真实 PhysX 实现。

## 影响范围

预计新增：

- `src/roomserver/physics/physx/physx.go`：Go 层 PhysXWorld，实现 `logic.PhysicsWorld`
- `src/roomserver/physics/physx/bridge.h`：C ABI 桥接头文件
- `src/roomserver/physics/physx/bridge.cpp`：C++ PhysX 封装实现
- `src/roomserver/physics/physx/types.go`：Go/C 类型转换
- `src/roomserver/physics/physx/physx_disabled.go`：未启用 build tag 时返回明确错误
- `src/roomserver/physics/physx/README.md`：说明 PhysX SDK 安装、环境变量和启动方式

预计修改：

- `src/roomserver/logic/physics.go`：补充物理接口所需字段，例如忽略自身、碰撞类型等
- `src/roomserver/service/server.go`：根据配置选择 `SimplePhysicsWorld` 或 `PhysXWorld`
- `src/roomserver/config/config.go`：增加 physics 配置
- `config/config.go` / `config/config.yaml`：统一配置增加 roomserver physics 项
- `Makefile`：增加可选 `test-physx` 或 `roomserver-physx` 命令

暂不修改：

- matchserver
- logicserver
- room token 和匹配协议

## 设计方案

### 1. 保持 Go 业务层只依赖 PhysicsWorld 接口

`Room` 和 `RoomManager` 不直接导入 cgo 包，只继续依赖：

```go
type PhysicsWorld interface {
    Raycast(RaycastRequest) (RaycastHit, error)
    BatchRaycast([]RaycastRequest) ([]RaycastHit, error)
}
```

PhysX 实现放到独立包：

```text
src/roomserver/physics/physx
```

这样业务层不会被 C++ 头文件和链接参数污染。

### 2. 使用 cgo + C ABI 桥接 C++ PhysX

Go 不直接调用 C++ 类，而是调用一层 C ABI：

```cpp
extern "C" {
  PxWorldHandle px_world_create(...);
  void px_world_destroy(PxWorldHandle world);
  int px_world_raycast(PxWorldHandle world, PxRaycastReq req, PxRaycastHit* out);
  int px_world_batch_raycast(PxWorldHandle world, PxRaycastReq* reqs, int count, PxRaycastHit* outs);
}
```

C++ 内部持有：

- `PxFoundation`
- `PxPhysics`
- `PxScene`
- `PxMaterial`
- `PxDispatcher`
- 必要时的 `PxPvd`

Go 层只保存 opaque handle：

```go
type World struct {
    handle C.PxWorldHandle
}
```

### 3. build tag 设计

真实 PhysX 文件使用 build tag：

```go
//go:build physx
```

默认未启用时，`physx_disabled.go` 提供同名构造函数，但返回错误：

```go
func NewWorld(cfg Config) (*World, error) {
    return nil, errors.New("physx build tag is not enabled")
}
```

这样：

- `go test ./...` 默认仍能通过
- 真正接 PhysX 时使用 `go test -tags physx ./...` 或 `go run -tags physx ./src/roomserver/cmd`

### 4. cgo 编译参数

建议通过环境变量指定 SDK 路径：

```bash
export PHYSX_ROOT=/opt/physx
export CGO_CXXFLAGS="-I$PHYSX_ROOT/include"
export CGO_LDFLAGS="-L$PHYSX_ROOT/lib/linux.x86_64/release -lPhysX_static_64 -lPhysXFoundation_static_64 ..."
```

或者在 `physx.go` 中写基础注释，但路径仍依赖环境：

```go
#cgo CXXFLAGS: -std=c++17
#cgo LDFLAGS: -lPhysX -lPhysXFoundation -lPhysXExtensions -lPhysXPvdSDK
```

更稳的方式是先写 README 和 Makefile 目标，明确用户本机如何配置。

### 5. PhysicsWorld 接口字段补充

当前 `RaycastRequest` 不够用，建议扩展：

```go
type RaycastRequest struct {
    Origin         Vector3
    Direction      Vector3
    MaxDistance    float64
    Mask           uint32
    IgnorePlayerID uint64
}
```

`RaycastHit` 建议补充：

```go
type RaycastHit struct {
    Hit        bool
    TargetID   uint64
    ColliderID uint64
    Point      Vector3
    Normal     Vector3
    Distance   float64
}
```

后续 PhysX actor 的 `userData` 可以保存 collider/player ID，raycast 命中后映射回 Go 层。

### 6. 对象生命周期

每个 room 可以有一个 PhysX scene，也可以多个 room 共享一个 scene。

第一版建议：每个 `Room` 一个 `PhysicsWorld` 实例。

理由：

- 房间之间物理状态完全隔离
- 生命周期跟 Room 一致，空房销毁时释放 PhysX scene
- 错误隔离更清晰

但当前 `RoomManager` 是把同一个 `physics` 传给所有新房间。需要改为工厂：

```go
type PhysicsWorldFactory interface {
    NewWorld(roomID string) (PhysicsWorld, error)
}
```

或更简单：

```go
type PhysicsWorldFactory func(roomID string) (PhysicsWorld, error)
```

`RoomManager.getOrCreateRoom` 创建房间时调用工厂，避免多个房间共用同一个 PhysX scene。

### 7. 玩家同步到 PhysX

真实 PhysX 需要知道玩家碰撞体位置。第一版可以先只做动态 actor 或 kinematic actor：

- 玩家加入房间：创建 capsule actor
- 玩家离开房间：销毁 actor
- 玩家移动：更新 actor transform
- Raycast：从 PhysX scene 查询命中

为了避免每帧大量 cgo 调用，后续可以做批量同步：

```go
SyncPlayers([]PhysicsPlayerState) error
```

但第一版可以先做单玩家更新，等链路跑通后再批量优化。

### 8. 房间逻辑接入顺序

第一版先做最小闭环：

1. PhysXWorld 能初始化和销毁
2. 能创建静态地面 plane
3. 能创建/更新玩家 capsule actor
4. `Raycast` 能命中玩家 actor
5. 玩家开火时根据 `RaycastHit.TargetID` 扣血

暂不做：

- 复杂地图 mesh cooking
- 完整角色控制器 CCT
- 网络预测和回滚
- 多线程 PhysX scene simulate

## 配置设计

`config/config.yaml` 可增加：

```yaml
room_server_01:
  physics:
    type: "physx"        # simple 或 physx
    enable_pvd: false
    gravity_y: -9.81
    player_radius: 0.35
    player_half_height: 0.9
```

`src/roomserver/config.Config` 增加：

```go
type PhysicsConfig struct {
    Type             string
    EnablePVD        bool
    GravityY         float64
    PlayerRadius     float64
    PlayerHalfHeight float64
}
```

默认 `Type=simple`，避免没有 PhysX 环境时 roomserver 启动失败。用户明确启用 `physx` 时，如果 build tag 或 SDK 不存在，启动直接失败并输出明确错误。

## 错误处理

- SDK 未启用：返回 `physx build tag is not enabled`
- PhysX 初始化失败：roomserver 启动失败
- 创建 room scene 失败：拒绝创建房间并记录错误
- 创建玩家 actor 失败：入房失败
- Raycast 失败：返回错误，上层记录 warn；射击命中不生效
- Destroy 必须幂等，避免 room stop 重复释放崩溃

## 兼容性影响

- 默认配置继续使用 `SimplePhysicsWorld`，不影响现有 `go test ./...`
- 启用 `physx` 后需要本机安装 SDK 和动态库
- `PhysicsWorld` 接口如果增加方法，会影响 `SimplePhysicsWorld`，需要同步实现
- `RoomManager` 如果改成 physics factory，会影响 `NewRoomManager` 调用点，主要是 `service/server.go`

## 性能考虑

- 不要在每个玩家、每个 tick 做大量单次 cgo 调用
- 第一版只在开火时 Raycast，调用频率较低
- AOI 遮挡后续要优先使用 `BatchRaycast`
- 玩家状态同步可以先逐个调用，压测后改成批量 `SyncPlayers`
- 每房间一个 PhysX scene 对隔离有利，但房间数多时内存会增加；后续可做 scene 池或按玩法合并

## 验证方式

默认验证：

```bash
go test ./...
```

PhysX 启用验证：

```bash
export PHYSX_ROOT=/path/to/physx
export LD_LIBRARY_PATH=$PHYSX_ROOT/lib/linux.x86_64/release:$LD_LIBRARY_PATH
go test -tags physx ./src/roomserver/physics/physx
go test -tags physx ./src/roomserver/...
go run -tags physx ./src/roomserver/cmd
```

需要增加测试：

- PhysXWorld 初始化和销毁
- 创建玩家 capsule
- Raycast 命中玩家
- Raycast 忽略自己
- Raycast 超出距离不命中
- BatchRaycast 返回数量和请求数量一致

## 自我审查

1. 是否遗漏项目结构：当前 roomserver 已有 `PhysicsWorld` 接口，适合新增独立 PhysX 实现，不应让 room 直接依赖 cgo
2. 是否过度设计：第一版只做初始化、玩家 actor、raycast 和开火命中，不做完整地图 cooking 和 CCT
3. 协议风险：不改客户端协议；后续扣血会通过现有 snapshot 的 HP 字段体现
4. 错误处理：必须明确 SDK 未启用、初始化失败、actor 创建失败、raycast 失败
5. 性能风险：cgo 高频调用是主要风险，第一版只在开火时调用，AOI 遮挡后续用 batch
6. 扩展性：物理工厂让每个房间独立 scene，后续可接地图、CCT、batch sync
7. 构建风险：当前环境没有 PhysX SDK，因此必须用 build tag，保证默认测试不被破坏

## 修正后的最终方案

最终建议按以下顺序做：

1. 先新增 `src/roomserver/physics/physx` 包，建立 cgo/C++ bridge 骨架和 build tag
2. 增加 `pkg`/配置说明，明确 `PHYSX_ROOT`、`LD_LIBRARY_PATH` 和 `-tags physx` 用法
3. 修改 roomserver 物理接入为 factory，支持每个 room 创建独立 physics world
4. 扩展 `RaycastRequest`，增加 `IgnorePlayerID`
5. 实现 PhysX 初始化、scene 创建、地面 plane、玩家 capsule actor 创建和更新
6. 实现 PhysX raycast 映射到 `RaycastHit.TargetID`
7. 在 `Room.handleInput` 中用玩家 yaw/pitch 生成射线方向，命中玩家后扣血
8. 默认 `go test ./...` 继续走 simple；提供 `go test -tags physx` 做真实 PhysX 验证

等待确认后再开始改代码。实现前还需要你提供 PhysX SDK 路径或确认我按 `PHYSX_ROOT` 环境变量方式接入。
