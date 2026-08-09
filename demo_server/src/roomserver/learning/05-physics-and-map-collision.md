# 阶段五：物理接口、PhysX 后端和地图碰撞

本阶段目标：看懂 logic 层如何通过 `PhysicsWorld` 使用物理能力，以及 PhysX 后端如何加载地图碰撞和出生点。

## 1. 物理在 roomserver 里的位置

房间逻辑不直接操作 PhysX。调用链是：

```text
Room.updatePlayers
  -> simulatePlayerTick
  -> buildMovePlayerRequest
  -> r.physics.MovePlayer
  -> PhysicsWorld 实现返回 MovePlayerResult
  -> Room 把结果写回 Player.X/Y/Z
```

`r.physics` 的静态类型是接口 `PhysicsWorld`，定义在 [../logic/physics.go](../logic/physics.go)。

## 2. Vector3 字段说明

结构在 [../logic/player.go](../logic/player.go)。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `X` | `float64` | X 轴坐标或方向分量 |
| `Y` | `float64` | Y 轴坐标或方向分量，当前通常代表高度 |
| `Z` | `float64` | Z 轴坐标或方向分量 |

当前移动输入主要影响 X/Z 平面，Y 轴由物理后端和地图碰撞决定。

## 3. PhysicsWorld 接口

定义在 [../logic/physics.go](../logic/physics.go)。

| 方法 | 作用 |
| --- | --- |
| `AddPlayer(playerID, position)` | 在物理世界中创建玩家碰撞体 |
| `RemovePlayer(playerID)` | 从物理世界移除玩家碰撞体 |
| `MovePlayer(req)` | 按输入方向、距离和垂直状态推进玩家，返回碰撞修正后的位置 |
| `GetPlayerPosition(playerID)` | 读取玩家当前物理坐标 |
| `SetPlayerPosition(playerID, position)` | 设置玩家当前物理坐标，后续重生或传送会用到 |
| `Raycast(req)` | 执行单条射线检测，当前开火时预留调用 |
| `BatchRaycast(reqs)` | 批量射线检测，为高频射线或 AOI 遮挡预留 |
| `SpawnPoints()` | 返回地图出生点列表 |
| `Close()` | 释放物理世界资源 |

这个接口隔离了 logic 和具体物理实现。Room 不关心底层是 Simple 后端还是 PhysX 后端。

## 4. MovePlayerRequest 字段

结构在 [../logic/physics.go](../logic/physics.go)。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `PlayerID` | `uint64` | 要移动的玩家 ID |
| `Direction` | `Vector3` | 服务端计算出的世界坐标水平移动方向，通常应归一化 |
| `Distance` | `float64` | 本 tick 希望水平移动的距离 |
| `DeltaTime` | `float64` | 当前物理步长，通常是 `1 / tickRate` |
| `Jump` | `bool` | 本 tick 是否请求跳跃 |
| `Grounded` | `bool` | 玩家上一帧是否处于地面 |
| `VerticalVelocity` | `float64` | 玩家上一帧垂直速度 |

请求由 [../logic/movement.go](../logic/movement.go) `buildMovePlayerRequest` 构造。

## 5. MovePlayerResult 字段

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `Position` | `Vector3` | 物理后端计算后的最终坐标 |
| `Blocked` | `bool` | 本次移动是否被碰撞阻挡或修正 |
| `Grounded` | `bool` | 移动后是否处于地面 |
| `VerticalVelocity` | `float64` | 移动后的垂直速度 |

Room 只使用 `Position` 更新玩家权威坐标。`Blocked` 当前主要用于测试和后续玩法扩展。

## 6. RaycastRequest 和 RaycastHit 字段

`RaycastRequest`：

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `Origin` | `Vector3` | 射线起点 |
| `Direction` | `Vector3` | 射线方向，必须非零 |
| `MaxDistance` | `float64` | 最大检测距离 |
| `Mask` | `uint32` | 碰撞过滤掩码，当前预留 |

`RaycastHit`：

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `Hit` | `bool` | 是否命中 |
| `TargetID` | `uint64` | 命中的玩家 ID，静态场景命中时可能为 0 |
| `Point` | `Vector3` | 命中点 |
| `Normal` | `Vector3` | 命中面法线 |
| `Distance` | `float64` | 起点到命中点距离 |

当前开火输入会调用 `Raycast`，但伤害结算还没有接上。

## 7. SpawnPoint 字段

结构在 [../logic/physics.go](../logic/physics.go)。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `ID` | `string` | 出生点 ID，比如 `spawn_a` |
| `Position` | `Vector3` | 出生坐标 |
| `Yaw` | `float64` | 初始水平朝向 |

Room 入房时调用 `r.physics.SpawnPoints()`，按顺序选择未被占用的出生点。

## 8. SimplePhysicsWorld

Simple 后端在 [../logic/physics.go](../logic/physics.go)，主要用于不依赖 PhysX 的测试和兜底。

字段：

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `mu` | `sync.Mutex` | 保护 players 和 closed |
| `players` | `map[uint64]Vector3` | 简单记录玩家当前位置 |
| `closed` | `bool` | 物理世界是否已关闭 |

特点：

- `MovePlayer` 只做简单世界边界 clamp，不做真实地图碰撞
- `Raycast` 和 `BatchRaycast` 只校验参数，当前不返回命中
- `SpawnPoints` 返回默认 `spawn_a` 和 `spawn_b`

Simple 后端适合 logic 单元测试，不适合真实战斗服。

## 9. PhysX Config 字段

结构在 [../physx/types.go](../physx/types.go)。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `PlayerCapsuleRadius` | `float64` | 玩家胶囊体半径 |
| `PlayerCapsuleHeight` | `float64` | 玩家胶囊体总高度 |
| `CreateGroundPlane` | `bool` | 是否创建默认地面 |
| `PVDEnabled` | `bool` | 是否启用 PhysX Visual Debugger |
| `PVDHost` | `string` | PVD 监听地址 |
| `PVDPort` | `int` | PVD 监听端口，默认 5425 |
| `PVDTimeoutMS` | `int` | PVD socket 连接超时毫秒数 |
| `DefaultMapID` | `string` | 期望加载的地图 ID |
| `MapCollisionPath` | `string` | 地图碰撞 JSON 路径 |

这些字段由 [../service/server.go](../service/server.go) `newPhysicsWorldFactory` 从 roomserver Config 传入。PVD 默认关闭，只建议本地调试时开启。PVD 需要 checked/profile PhysX 库；release 构建会关闭 `PX_SUPPORT_PVD`。

## 10. PhysX World 字段

结构在 [../physx/world.go](../physx/world.go)，需要 `-tags physx` 才会编译真实实现。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `ptr` | `*C.px_world` | 指向 C++ 层 PhysX world 的指针 |
| `cfg` | `Config` | PhysX 后端配置 |
| `spawnPoints` | `[]logic.SpawnPoint` | 从地图碰撞 JSON 转换来的出生点 |

如果没有启用 `physx` build tag，会编译 [../physx/world_stub.go](../physx/world_stub.go)。此时配置 `physics_backend=physx` 会在创建 world 时返回明确错误：`physx backend requires building with -tags physx`。

## 11. PhysX world 创建流程

代码在 [../physx/world.go](../physx/world.go) `Factory.NewWorld`。

```text
newCErrorBuffer
  -> C.px_world_create(createGroundPlane)
  -> world.loadMapCollision
  -> 返回 *World
```

如果地图碰撞加载失败，会调用 `world.Close()` 释放已经创建的 C++ world，避免资源泄漏。

C++ ABI 在 [../physx/physx_bridge.h](../physx/physx_bridge.h) 中定义，真实实现是 [../physx/physx_bridge.cc](../physx/physx_bridge.cc)。

## 12. C ABI 关键类型

`physx_bridge.h` 里定义了 Go 和 C++ 之间传递的数据结构。

`px_vec3`：

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `x` | `double` | X 分量 |
| `y` | `double` | Y 分量 |
| `z` | `double` | Z 分量 |

`px_quat`：

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `x` | `double` | 四元数 X |
| `y` | `double` | 四元数 Y |
| `z` | `double` | 四元数 Z |
| `w` | `double` | 四元数 W |

`px_raycast_hit`：

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `hit` | `int` | 是否命中 |
| `target_id` | `uint64_t` | 命中玩家 ID |
| `point` | `px_vec3` | 命中点 |
| `normal` | `px_vec3` | 命中法线 |
| `distance` | `double` | 命中距离 |

## 13. C ABI 方法说明

| 方法 | 作用 |
| --- | --- |
| `px_world_create` | 创建房间级 PhysX world，并在开启时连接 PVD |
| `px_world_release` | 释放 PhysX world |
| `px_world_add_static_box` | 添加地图静态 box 碰撞体 |
| `px_world_add_player_capsule` | 添加玩家胶囊体 |
| `px_world_remove_player` | 移除玩家胶囊体 |
| `px_world_move_player` | 使用 PhysX sweep 推进玩家移动 |
| `px_world_get_player_position` | 获取玩家当前物理位置 |
| `px_world_set_player_position` | 设置玩家当前物理位置 |
| `px_world_raycast` | 单条射线检测 |
| `px_world_batch_raycast` | 批量射线检测 |

C++ 层内部使用进程级 runtime 共享 `PxFoundation` 和 `PxPhysics`，每个房间独立 `PxScene`，避免不同房间互相碰撞。

## 14. 地图碰撞 JSON 结构

默认文件是 [../../../config/maps/mfps_arena/collision.json](../../../config/maps/mfps_arena/collision.json)。解析代码在 [../physx/map_collision.go](../physx/map_collision.go)。

`MapCollision` 字段：

| JSON 字段 | Go 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `map_id` | `MapID` | `string` | 地图 ID，必须和配置 `DefaultMapID` 一致 |
| `map_version` | `MapVersion` | `int` | 地图碰撞版本 |
| `physics_hash` | `PhysicsHash` | `string` | 地图物理数据 hash |
| `units` | `Units` | `string` | 坐标单位，当前支持 `meter` |
| `rotation` | `Rotation` | `string` | 旋转格式，当前支持 `quat_xyzw` |
| `colliders` | `Colliders` | `[]MapCollider` | 静态碰撞体列表 |
| `spawn_points` | `SpawnPoints` | `[]MapSpawnPoint` | 出生点列表 |

## 15. MapCollider 字段

| JSON 字段 | Go 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `id` | `ID` | `string` | 碰撞体 ID，用于定位配置错误 |
| `shape` | `Shape` | `string` | 碰撞体形状，当前实体阻挡只支持 `box` |
| `position` | `Position` | `Vector3Raw` | 世界坐标，数组 `[x,y,z]` |
| `rotation` | `Rotation` | `QuatRaw` | 世界旋转，数组 `[x,y,z,w]` |
| `size` | `Size` | `Vector3Raw` | box 的完整尺寸，不是半尺寸 |
| `radius` | `Radius` | `float64` | sphere/capsule 半径，当前预留 |
| `height` | `Height` | `float64` | capsule 高度，当前预留 |
| `direction` | `Direction` | `string` | capsule 轴向，当前预留 |
| `is_trigger` | `IsTrigger` | `bool` | 是否触发器，当前触发器不参与实体阻挡 |

当前 `validateMapCollider` 对非 trigger 的支持规则是：

```text
shape 必须是 box
position 必须是有限数
rotation 必须是有效四元数
size 每个分量必须 > 0
```

暂不支持的实体 shape 会返回 `ErrUnsupportedMapColliderShape`。

## 16. MapSpawnPoint 字段

| JSON 字段 | Go 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `id` | `ID` | `string` | 出生点 ID |
| `position` | `Position` | `Vector3Raw` | 出生点世界坐标 |
| `rotation` | `Rotation` | `QuatRaw` | 出生点旋转，服务端会提取 yaw |

`toLogicSpawnPoints` 会把地图出生点转成 logic 层的 `SpawnPoint`。`quaternionYaw` 从 Unity Y 轴四元数中提取水平朝向。

## 17. 地图加载流程

代码在 [../physx/world.go](../physx/world.go) `loadMapCollision`。

```text
loadMapCollision(path, expectedMapID)
  -> resolveProjectPath
  -> os.ReadFile
  -> json.Unmarshal
  -> validateMapCollision
  -> toLogicSpawnPoints
  -> 遍历 colliders
      -> trigger 跳过
      -> 非 box 报错
      -> addStaticBox
```

`addStaticBox` 会调用 C ABI：

```go
C.px_world_add_static_box(...)
```

C++ 层会把完整 size 转成 PhysX `PxBoxGeometry` 需要的半尺寸。

## 18. 路径解析

`resolveProjectPath` 支持绝对路径和相对路径。

相对路径会通过 `findProjectRoot` 向上查找 `go.mod`，找到项目根目录后拼接：

```text
projectRoot + MapCollisionPath
```

所以默认 `config/maps/mfps_arena/collision.json` 可以从不同工作目录启动时被找到。

## 19. 错误和边界

物理和地图加载常见错误：

| 错误 | 触发情况 |
| --- | --- |
| `ErrMapCollisionPathEmpty` | 地图碰撞路径为空 |
| `project root not found` | 相对路径解析时向上找不到 `go.mod` |
| `read map collision` | 文件不存在或无权限 |
| `parse map collision` | JSON 格式错误 |
| `map collision id mismatch` | JSON 的 map_id 和配置 DefaultMapID 不一致 |
| `unsupported map collision units` | units 不是 `meter` |
| `unsupported map collision rotation` | rotation 不是 `quat_xyzw` |
| `ErrUnsupportedMapColliderShape` | 非 trigger 碰撞体 shape 不是 `box` |
| `physics world closed` | world 已 Close 后继续调用 |
| `physx backend requires building with -tags physx` | 未启用 physx build tag 却选择 PhysX 后端 |

## 20. 测试对应关系

相关测试：

| 测试文件 | 关注点 |
| --- | --- |
| [../logic/physics_test.go](../logic/physics_test.go) | Simple 物理后端移动、非法请求、raycast 参数 |
| [../logic/room_spawn_test.go](../logic/room_spawn_test.go) | 入房分配出生点、离房复用出生点、快照携带 spawn_id |
| [../physx/map_collision_test.go](../physx/map_collision_test.go) | 地图碰撞 JSON 加载、出生点转换、不支持 shape 报错 |
| [../physx/world_test.go](../physx/world_test.go) | PhysX world、静态地图碰撞、多 world runtime 共享 |

常用验证命令：

```bash
go test ./src/roomserver/...
```

如果要跑真实 PhysX 后端测试，需要确保本机 PhysX SDK 已准备好，并使用对应 build tag：

```bash
go test -tags physx ./src/roomserver/physx
```

## 21. PhysX PVD 调试

PVD 是 PhysX Visual Debugger，用来在本地观察 scene、actor、静态碰撞体、raycast 和 sweep 等调试数据。roomserver 默认关闭 PVD，避免服务端启动依赖外部 GUI，也避免调试传输影响性能。

配置字段在 `room_server_01` 下：

```yaml
physx_pvd_enabled: false
physx_pvd_host: "127.0.0.1"
physx_pvd_port: 5425
physx_pvd_timeout_ms: 100
```

开启流程：

1. 先打开 NVIDIA PhysX Visual Debugger，并监听 `5425` 端口。
2. 把 `physx_pvd_enabled` 改成 `true`。
3. 用 `-tags physx` 启动 roomserver。
4. 创建或加入房间，让 roomserver 创建 PhysX world。

启用后，C++ 层会在进程级 PhysX runtime 创建 `PxPvd` 和 socket transport，创建 `PxPhysics` 后调用 `PxInitExtensions`，并给每个房间的 `PxScene` 打开 contacts、scene queries、constraints 传输。当前移动使用 sweep，开火使用 raycast，所以 PVD 中可以重点观察 scene query 数据。

如果启用 PVD 但没有打开 PVD 工具，或当前 PhysX 库没有启用 PVD scene client，roomserver 会在创建房间时失败并提示 PVD 连接或 scene client 错误。这是为了避免误以为调试链路已经连上。
