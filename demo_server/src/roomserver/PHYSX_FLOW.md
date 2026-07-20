# roomserver PhysX 接入全流程解析

本文档说明当前 roomserver 接入 PhysX 后的完整链路，包括构建、启动、入房、输入、tick 移动、开火 raycast、离房和资源释放。文档中会额外解释 PhysX 的基础概念，方便后续继续扩展地图碰撞、角色控制器、命中结算和 AOI 遮挡。

## 1. PhysX 在本项目里的定位

PhysX 是 NVIDIA 的 C++ 物理引擎。它负责维护一个物理世界，也就是场景中的碰撞体、刚体、查询结构和物理模拟状态。

在当前 roomserver 中，PhysX 的职责不是替代房间业务逻辑，而是作为 logic 层下面的物理查询和碰撞修正后端：

```text
客户端输入
-> roomserver logic 校验输入和计算移动意图
-> PhysX 根据碰撞体计算最终位置或 raycast 命中
-> roomserver logic 更新玩家状态并广播快照
```

也就是说，PhysX 不决定玩家能不能入房、不决定伤害结算、不直接处理网络消息。它只负责和空间、碰撞、射线有关的计算。

当前第一版接入的能力是：

- 每个房间一个独立 PhysX scene。
- 玩家在 PhysX 中是一个 capsule actor。
- 默认创建一个 y=0 的地面 plane。
- 玩家移动时用 capsule sweep 做碰撞检测。
- 开火时用 raycast 做射线检测。

## 2. PhysX 基础概念

### 2.1 Foundation

`PxFoundation` 是 PhysX 的底层基础对象。它负责内存分配、错误回调等基础设施。一般一个进程可以共享 foundation，但当前实现为了房间 world 生命周期简单，先把 foundation 放在每个 `px_world` 里统一创建和释放。

对应代码：

```cpp
PxCreateFoundation(PX_PHYSICS_VERSION, allocator, error_callback)
```

如果 foundation 创建失败，后续所有 PhysX 对象都不能创建。

### 2.2 Physics

`PxPhysics` 是 PhysX SDK 的核心对象。通过它创建 scene、material、shape、actor 等对象。

对应代码：

```cpp
PxCreatePhysics(PX_PHYSICS_VERSION, *foundation, PxTolerancesScale(), true, nullptr)
```

`PxTolerancesScale` 用来告诉 PhysX 当前世界大概的长度和速度尺度。当前使用默认值。后续如果项目明确“1单位=1米”，可以继续保持默认；如果单位体系变化，需要重新评估这个 scale。

### 2.3 Scene

`PxScene` 是真正的物理场景。可以把它理解成一个独立物理世界。actor、shape、raycast、sweep、simulate 都发生在 scene 中。

本项目按房间创建 scene：

```text
Room A -> PxScene A
Room B -> PxScene B
```

这样 Room A 的玩家不会被 Room B 的射线命中，也不会发生跨房间碰撞。

对应代码：

```cpp
PxSceneDesc scene_desc(physics->getTolerancesScale());
scene_desc.gravity = PxVec3(0.0f, -9.81f, 0.0f);
scene_desc.cpuDispatcher = dispatcher;
scene_desc.filterShader = PxDefaultSimulationFilterShader;
scene = physics->createScene(scene_desc);
```

几个字段含义：

- `gravity`：场景重力。当前玩家 actor 禁用了重力，所以主要为后续动态物体预留。
- `cpuDispatcher`：PhysX 内部执行模拟任务使用的 CPU dispatcher。
- `filterShader`：碰撞过滤规则。当前使用默认过滤规则，后续接 mask 时需要替换或增加 query filter。

### 2.4 Actor

`PxActor` 是 PhysX 场景中的对象基类。常见子类包括：

- `PxRigidStatic`：静态刚体，例如地面、墙、地图碰撞体。不会被物理模拟移动。
- `PxRigidDynamic`：动态刚体，例如箱子、玩家胶囊体、可移动物体。可以参与动态模拟。

当前实现中：

- 默认地面是 `PxRigidStatic`。
- 玩家是 `PxRigidDynamic`，但设置为 kinematic。

### 2.5 Shape 和 Geometry

actor 本身表示“场景中的对象”，shape/geometry 表示“这个对象的碰撞形状”。

当前用到的 geometry：

- `PxCapsuleGeometry`：玩家胶囊体。
- `PxPlane`：默认地面。

玩家用 capsule，而不是 box 或 sphere，是因为 FPS/TPS 游戏里角色通常接近“竖直胶囊”：

```text
   ____
 /      \
|        |
|        |
 \______/ 
```

胶囊体适合角色移动，因为它没有尖角，贴着墙和障碍移动时比 box 更不容易卡住。

### 2.6 Material

`PxMaterial` 定义摩擦和弹性参数。

当前创建方式：

```cpp
physics->createMaterial(0.5f, 0.5f, 0.6f)
```

三个参数大致表示：

- static friction：静摩擦。
- dynamic friction：动摩擦。
- restitution：反弹系数。

当前玩家是 kinematic，移动主要靠 sweep 修正，这些参数影响不大。后续如果加入动态箱子、投掷物、弹跳物体，这些参数会更重要。

### 2.7 Kinematic Actor

玩家 actor 创建后设置为 kinematic：

```cpp
actor->setRigidBodyFlag(PxRigidBodyFlag::eKINEMATIC, true)
actor->setActorFlag(PxActorFlag::eDISABLE_GRAVITY, true)
```

kinematic actor 的含义是：

- 它是 dynamic actor 的一种。
- 它不会像普通动态刚体那样被力、重力、碰撞自动推着走。
- 外部系统可以主动设置它的目标位置。
- 它仍然可以参与 scene query，例如 raycast、sweep。

当前 roomserver 是服务端权威移动，不希望玩家因为物理模拟误差自由漂移，所以玩家用 kinematic。玩家最终怎么移动由 room tick 和输入决定，PhysX 负责回答“这一步移动会不会撞到东西，最终能到哪里”。

### 2.8 Raycast

raycast 是从一个点沿一个方向发射射线，查询最近命中的碰撞体。

常见用途：

- 枪械命中检测。
- 视线遮挡检测。
- 地面高度检测。
- AI 感知检测。

当前开火时使用 raycast。命中玩家 actor 后，可以从 actor 的 `userData` 取回玩家 ID。

### 2.9 Sweep

sweep 是把一个几何体沿某个方向移动一段距离，查询移动路径上是否撞到其他碰撞体。

raycast 是“线”检测，sweep 是“体积”检测。

玩家移动不能只用 raycast，因为玩家不是一条线，而是有半径和高度的胶囊体。使用 capsule sweep 可以检测“这个胶囊体从当前位置移动到目标位置的过程中是否撞墙”。

当前 `MovePlayer` 使用的是 capsule sweep。

### 2.10 Simulate 和 FetchResults

PhysX 的完整模拟通常分两步：

```cpp
scene->simulate(dt);
scene->fetchResults(true);
```

`simulate` 提交模拟任务，`fetchResults` 等待并取回结果。

当前移动逻辑里，设置 kinematic target 后会调用一次 simulate/fetchResults，让 PhysX 更新 scene 状态。由于当前玩家 actor 是 kinematic，而且每个房间 goroutine 串行访问 scene，所以这一步比较直接。

后续如果接大量动态物体，应该把 simulate 放到每个房间 tick 的统一位置，而不是每个玩家移动都 simulate 一次。当前小房间第一版可以工作，但这也是后续性能优化点。

## 3. 构建链路

PhysX 相关构建入口主要有两个：

```text
scripts/setup_physx.sh
scripts/build_all.sh
```

`setup_physx.sh` 负责准备本地 PhysX SDK：

1. 如果系统没有 `cmake`，下载用户态 CMake 到 `third_party/tools`。
2. 从 NVIDIA PhysX 仓库拉取源码到 `third_party/PhysX`。
3. 使用 PhysX 官方 preset `linux-gcc-cpu-only` 生成 Linux 构建工程。
4. 只构建服务端需要的核心 PhysX 库。
5. 将头文件和库整理到 `third_party/physx-sdk`。

当前只构建这些核心库：

```text
PhysX
PhysXExtensions
PhysXPvdSDK
PhysXCommon
PhysXFoundation
```

这些库的大致职责：

- `PhysXFoundation`：底层基础设施，内存、错误回调、平台抽象。
- `PhysXCommon`：通用几何、碰撞查询、mesh 等基础能力。
- `PhysX`：核心物理 SDK，scene、actor、rigid body 等主要 API。
- `PhysXExtensions`：一些默认扩展工具，例如默认 dispatcher、默认 filter shader、plane 创建等。
- `PhysXPvdSDK`：PhysX Visual Debugger 相关支持。当前链接它是为了满足 PhysX 静态库依赖。

没有构建 PhysX snippets。原因是 snippets 会依赖 OpenGL/X11 示例渲染环境，而服务端物理不需要这些依赖。之前全量构建时遇到过 `X11/Xlib.h` 缺失，所以脚本改成只构建核心库。

构建成功后的 SDK 目录结构大致是：

```text
third_party/physx-sdk
├── include
│   └── PxPhysicsAPI.h
├── pxshared
│   └── include
└── lib
    └── linux.x86_64
        └── release
            ├── libPhysX_static_64.a
            ├── libPhysXExtensions_static_64.a
            ├── libPhysXPvdSDK_static_64.a
            ├── libPhysXCommon_static_64.a
            └── libPhysXFoundation_static_64.a
```

`build_all.sh` 会先检查 `third_party/physx-sdk` 是否存在。编译 `roomserver` 时默认加上：

```bash
-tags physx
```

因此默认构建出来的 roomserver 使用 PhysX 后端。

## 4. 默认配置

roomserver 配置定义在：

```text
src/roomserver/config/config.go
```

新增了这些 PhysX 相关配置：

```go
PhysicsBackend      string
PlayerCapsuleRadius float64
PlayerCapsuleHeight float64
PhysicsGroundPlane  bool
```

默认值是：

```go
PhysicsBackend:      "physx"
PlayerCapsuleRadius: 0.35
PlayerCapsuleHeight: 1.8
PhysicsGroundPlane:  true
```

对应 `config/config.yaml` 中的配置是：

```yaml
physics_backend: "physx"
player_capsule_radius: 0.35
player_capsule_height: 1.8
physics_ground_plane: true
```

配置含义：

- `physics_backend`：物理后端。默认 `physx`，可改为 `simple` 做调试回退。
- `player_capsule_radius`：玩家胶囊体半径。半径越大，玩家越“胖”，越不容易穿过狭窄空间。
- `player_capsule_height`：玩家胶囊体总高度。必须大于两倍半径，否则胶囊体不合法。
- `physics_ground_plane`：是否创建默认 y=0 地面。

如果后续接地图碰撞，默认地面可以保留作为兜底，也可以按地图配置关闭。

## 5. 服务启动流程

启动入口在：

```text
src/roomserver/service/server.go
```

`Server.Start` 会先创建物理世界工厂：

```go
physicsFactory, err := s.newPhysicsWorldFactory()
```

`newPhysicsWorldFactory` 按配置选择后端：

```text
physx  -> src/roomserver/physx.NewFactory
simple -> logic.NewSimplePhysicsWorldFactory
其他值 -> 启动失败
```

然后将 factory 注入 `RoomManager`：

```go
logic.NewRoomManager(..., physicsFactory)
```

这里注入的是 factory，而不是一个共享 world。真实 PhysX 场景不能被所有房间共用，否则 A 房间的玩家可能被 B 房间的 raycast 或 sweep 查询到。

## 6. Logic 层物理接口

物理接口定义在：

```text
src/roomserver/logic/physics.go
```

当前接口是：

```go
type PhysicsWorld interface {
    AddPlayer(playerID uint64, position Vector3) error
    RemovePlayer(playerID uint64) error
    MovePlayer(MovePlayerRequest) (MovePlayerResult, error)
    Raycast(RaycastRequest) (RaycastHit, error)
    BatchRaycast([]RaycastRequest) ([]RaycastHit, error)
    Close() error
}
```

各方法职责：

- `AddPlayer`：玩家入房时创建物理 actor。
- `RemovePlayer`：玩家离房时释放物理 actor。
- `MovePlayer`：每个 tick 根据服务端认可的移动方向推进玩家位置。
- `Raycast`：用于开火命中、后续 AOI 遮挡等射线检测。
- `BatchRaycast`：为后续高频批量射线检测预留。
- `Close`：房间结束时释放整个物理 world。

logic 层只依赖这个 Go 接口，不直接 `import "C"`，避免业务逻辑和 cgo 绑定。

`SimplePhysicsWorld` 也实现了完整接口。它维护一份 Go 内存中的玩家坐标，并做简单世界边界 clamp。它不是正式物理后端，只用于不带 `physx` tag 的测试或临时调试。

## 7. 每房间独立物理世界

房间管理逻辑在：

```text
src/roomserver/logic/room_manager.go
```

`RoomManager` 现在持有：

```go
physicsFactory PhysicsWorldFactory
```

创建房间时：

```go
physicsWorld, err := m.physicsFactory.NewWorld(roomID)
room = NewRoom(..., physicsWorld)
```

也就是每个房间都有独立的 PhysX scene：

```text
Room A -> PxScene A
Room B -> PxScene B
Room C -> PxScene C
```

这样可以避免不同房间之间发生物理串扰。

独立 scene 也让后续扩展更清晰：

- 每个房间可以加载自己的地图碰撞体。
- 每个房间可以独立暂停、销毁、回收。
- 每个房间可以独立统计物理耗时。
- 房间之间不会共享 actor ID、query cache 或碰撞对象。

## 8. 玩家入房流程

入房主链路：

```text
客户端 JoinRoom
-> service 解析 room token
-> RoomManager.JoinRoom
-> Room.Join 投递事件
-> room goroutine 执行 handleJoin
```

`handleJoin` 位于：

```text
src/roomserver/logic/room.go
```

现在玩家加入房间时，会先创建物理对象：

```go
r.physics.AddPlayer(player.ID, Vector3{X: player.X, Y: player.Y, Z: player.Z})
```

在 PhysX 后端中，这会走到 C++ 的 `px_world_add_player_capsule`：

```cpp
PxCapsuleGeometry(radius, halfHeight)
PxCreateDynamic(...)
actor->setRigidBodyFlag(PxRigidBodyFlag::eKINEMATIC, true)
actor->setActorFlag(PxActorFlag::eDISABLE_GRAVITY, true)
world->scene->addActor(*actor)
```

如果物理 actor 创建失败，玩家不会写入 `r.players`，而是返回入房失败 ack，并记录日志。这样可以避免逻辑层有玩家、物理层没有 actor 的不一致状态。

成功后才执行：

```go
r.players[player.ID] = player
```

## 9. 输入处理流程

客户端输入仍然通过原来的 `PlayerInput` 消息提交。

service 层在：

```text
src/roomserver/service/server.go
```

收到输入后只做解包和转发：

```go
s.manager.PushInput(session.PlayerID(), input)
```

房间逻辑在 `handleInput` 中缓存最近一次有效输入：

```go
sanitized, ok := sanitizePlayerInput(input)
r.inputs[playerID] = playerInputState{input: sanitized, hasData: true}
```

输入清洗在：

```text
src/roomserver/logic/movement.go
```

清洗规则包括：

- 拒绝 NaN 和 Inf。
- `MoveX`、`MoveZ` 限制到 `[-1, 1]`。
- 斜向移动归一化，避免斜走速度更快。
- `Yaw` 归一化到 `[-180, 180]`。
- `Pitch` 限制到 `[-89, 89]`。

客户端只提交移动意图，最终位置仍由服务端 tick 和物理后端决定。

## 10. Tick 中推进移动

房间 tick 更新入口在：

```text
src/roomserver/logic/room.go
```

`updatePlayers` 每 tick 遍历玩家并处理最近一次输入：

```text
遍历玩家
-> 取最近一次有效输入
-> 更新 yaw/pitch
-> 生成物理移动请求
-> 调用 PhysicsWorld.MovePlayer
-> 用物理返回位置更新 Player.X/Y/Z
```

移动请求由 `movement.go` 中的 `buildMovePlayerRequest` 生成：

```go
buildMovePlayerRequest(playerID, input, r.tickRate)
```

移动距离计算方式：

```go
defaultPlayerMoveSpeed / float64(tickRate)
```

当前默认：

```text
移动速度 = 4.0 单位/秒
tickRate = 20
每 tick 最大移动 = 0.2 单位
```

之前逻辑层会直接修改坐标。现在改成：

```go
result, err := r.physics.MovePlayer(moveReq)
player.X = result.Position.X
player.Y = result.Position.Y
player.Z = result.Position.Z
```

最终位置由物理后端返回，logic 层不再绕过碰撞规则。

## 11. PhysX Go 封装

PhysX Go 封装在：

```text
src/roomserver/physx/world.go
```

它负责：

- 保存 C 层 `px_world*`。
- 将 Go 的 `logic.Vector3` 转换为 C 结构体。
- 调用 C ABI 函数。
- 将 C 返回值转换回 Go 结构。
- 将 C 错误缓冲区转换为 Go error。

例如 `MovePlayer` 会调用：

```go
C.px_world_move_player(...)
```

然后转换为：

```go
logic.MovePlayerResult{
    Position: fromCVec3(outPosition),
    Blocked:  outBlocked != 0,
}
```

PhysX 后端使用 build tag 隔离：

```text
world.go      -> //go:build physx
world_stub.go -> //go:build !physx
```

如果配置为 `physx`，但构建时没加 `-tags physx`，stub 会返回明确错误：

```text
physx backend requires building with -tags physx
```

这样不会静默降级，避免误以为已经启用 PhysX。

### 11.1 为什么要有 Go 封装层

cgo 可以让 Go 调 C，但不适合把业务逻辑散落在 cgo 调用点里。当前封装层有几个目的：

1. 隔离 C++ 复杂度。外部只看到 Go interface。
2. 集中做 Go/C 类型转换。
3. 集中处理 C 错误缓冲区。
4. 避免 logic 层保存 C 指针。
5. 方便后续替换 PhysX 版本或增加批量接口。

Go 层不会把 Go 指针传给 C++ 保存。C++ 层只保存自己的对象和基础类型，避免违反 cgo 指针规则。

## 12. C ABI 桥接

C ABI 声明在：

```text
src/roomserver/physx/physx_bridge.h
```

C++ 实现在：

```text
src/roomserver/physx/physx_bridge.cc
```

Go 不直接操作 PhysX C++ 类，而是调用这些 C ABI 函数：

```c
px_world_create
px_world_release
px_world_add_player_capsule
px_world_remove_player
px_world_move_player
px_world_raycast
px_world_batch_raycast
```

C++ 层内部的 `px_world` 持有：

```cpp
PxFoundation* foundation;
PxPhysics* physics;
PxDefaultCpuDispatcher* dispatcher;
PxScene* scene;
PxMaterial* material;
std::unordered_map<uint64_t, player_actor> players;
```

也就是每个房间一个完整的 PhysX scene。

### 12.1 为什么要包一层 C ABI

Go 的 cgo 可以直接包含 C 头文件，但不能直接方便地调用复杂 C++ 类、模板、析构函数和命名空间对象。PhysX 是 C++ SDK，所以中间包一层 C ABI 是更稳的做法。

C ABI 层把复杂 C++ 对象隐藏起来：

```text
Go
-> C ABI: px_world_move_player
-> C++: PxScene::sweep / PxRigidDynamic / PxCapsuleGeometry
```

这样 Go 层只需要处理普通 C 函数、数字、结构体和 opaque pointer。

## 13. PhysX world 创建

`px_world_create` 的创建流程：

```text
PxCreateFoundation
-> PxCreatePhysics
-> PxDefaultCpuDispatcherCreate
-> createScene
-> createMaterial
-> 可选创建默认 ground plane
```

如果 `physics_ground_plane` 为 true，会创建 y=0 的默认地面：

```cpp
PxCreatePlane(*world->physics, PxPlane(0, 1, 0, 0), *world->material)
```

默认地面是 `PxRigidStatic`。它不会移动，主要用于阻挡玩家往 y<0 掉落，也给后续测试 raycast 提供一个静态碰撞体。

当前第一阶段还没有接地图 mesh，所以 world 中至少包含默认地面和玩家 capsule。

### 13.1 创建顺序为什么重要

PhysX 对象之间有依赖关系：

```text
Foundation
└── Physics
    ├── Material
    └── Scene
        └── Actor
            └── Shape/Geometry
```

创建时必须先有 foundation，再有 physics，再创建 scene/material/actor。释放时则反过来，先释放 actor，再释放 scene/material，最后释放 physics/foundation。

## 14. 玩家 PhysX actor

玩家入房时，C++ 层创建 capsule actor：

```cpp
PxCapsuleGeometry(radius, halfHeight)
PxCreateDynamic(...)
```

PhysX 的 capsule 参数不是“总高度”，而是：

```text
radius
halfHeight
```

这里的 `halfHeight` 指的是胶囊体中间圆柱部分的一半长度，不包含上下两个半球。代码里用：

```cpp
(height - radius * 2.0) * 0.5
```

把配置中的玩家总高度转换为 PhysX capsule 的 halfHeight。

随后设置为 kinematic：

```cpp
actor->setRigidBodyFlag(PxRigidBodyFlag::eKINEMATIC, true)
actor->setActorFlag(PxActorFlag::eDISABLE_GRAVITY, true)
```

选择 kinematic 的原因是当前是服务端权威移动。输入只代表意图，房间 tick 决定移动步长，PhysX 负责碰撞修正，而不是让玩家刚体自由受力移动。

同时将玩家 ID 写入：

```cpp
actor->userData
```

后续 raycast 命中该 actor 时，可以反查出 `TargetID`。

### 14.1 玩家坐标和 PhysX actor 坐标

业务层 `Player.X/Y/Z` 表示玩家脚底或逻辑位置。PhysX actor 的 transform 位置更接近胶囊体中心点。

创建 actor 时会做转换：

```cpp
center_y = position.y + height * 0.5
```

返回给 Go 时再转换回来：

```cpp
logic_y = pose.p.y - height * 0.5
```

这样业务层仍然可以把 `Y=0` 理解为玩家站在地面上，而 PhysX 内部使用胶囊中心点计算碰撞。

### 14.2 胶囊体朝向

PhysX capsule 默认沿 X 轴方向。角色胶囊通常需要竖直站立，也就是沿 Y 轴方向。因此创建 transform 时用了一个旋转：

```cpp
PxQuat(PxHalfPi, PxVec3(0, 0, 1))
```

它把 capsule 从默认轴向旋转成竖直方向。后续如果发现碰撞形状方向不符合预期，优先检查这里。

## 15. MovePlayer 物理移动

C++ 层 `px_world_move_player` 的流程：

```text
找到 player actor
-> 校验 direction/distance
-> direction 归一化
-> 用 capsule geometry 从当前位置沿方向 sweep
-> 如果 sweep 命中阻挡，只移动到命中点前一点
-> setKinematicTarget
-> scene.simulate / fetchResults
-> 返回最终位置
```

核心点是使用 PhysX scene sweep，而不是直接改坐标。这样后续加入墙体、箱子、地图 mesh 后，玩家移动会被碰撞体阻挡。

### 15.1 为什么移动用 sweep

如果只把玩家坐标从 A 改到 B，会有两个问题：

1. 目标点 B 可能已经在墙里。
2. A 到 B 中间可能穿过薄墙，但最终 B 在墙外，看起来像穿墙。

sweep 是“把整个胶囊体从 A 推到 B 的路径检测”。它能发现路径上的阻挡，并返回命中距离。

代码里如果 sweep 命中，会把移动距离截短：

```cpp
travel = max(0, hitDistance - 0.01)
```

`0.01` 是一个很小的安全间隔，避免最终位置刚好贴进碰撞面导致下一帧抖动或卡住。

### 15.2 当前移动还不是完整角色控制器

当前实现只做了最基础的 sweep 阻挡，还没有做完整的角色控制器行为：

- 没有自动沿墙滑动。
- 没有台阶处理。
- 没有坡面限制。
- 没有跳跃和重力。
- 没有地面检测状态。

如果后续要做更像游戏角色的移动，可以考虑两条路线：

1. 使用 PhysX Character Controller。
2. 在当前 sweep 基础上补 slide、ground check、step offset、slope limit。

第一版先用 sweep，是因为它简单、可控，适合作为服务端权威移动的基础版本。

返回结果包括：

```go
type MovePlayerResult struct {
    Position Vector3
    Blocked  bool
}
```

目前 Go 层只使用 `Position` 更新玩家坐标，`Blocked` 预留给后续做客户端纠偏、碰撞反馈或状态同步。

## 16. 开火 Raycast

开火触发在 `room.go`：

```go
if inputState.input.Fire {
    _, _ = r.physics.Raycast(...)
}
```

射线方向由 `movement.go` 中的 `viewDirection` 根据 yaw/pitch 计算。

C++ 层 raycast 使用：

```cpp
scene->raycast(origin, dir, max_distance, hit, ...)
```

raycast 的输入包括：

- `origin`：射线起点，当前是玩家位置。
- `direction`：射线方向，当前由 yaw/pitch 计算。
- `max_distance`：最大检测距离。
- `mask`：预留的碰撞过滤掩码。

如果命中玩家 actor，会从：

```cpp
hit.block.actor->userData
```

取出玩家 ID，并填入 Go 侧结果：

```go
RaycastHit.TargetID
```

`RaycastHit` 还包含：

- `Hit`：是否命中。
- `Point`：命中点。
- `Normal`：命中面的法线。
- `Distance`：从 origin 到命中点的距离。

当前已经接通物理查询，但还没有接伤害结算。也就是说 raycast 能返回命中结果，后续还需要把结果用于扣血、击杀、广播命中事件等玩法逻辑。

### 16.1 Raycast 和枪械命中的关系

对于即时命中的枪械，raycast 可以直接表示子弹路径：

```text
枪口位置 + 瞄准方向 + 最大射程 -> 最近命中物
```

如果命中玩家，就可以进入伤害结算。如果命中墙，则子弹被墙挡住。

如果后续要做有飞行时间的子弹，例如火箭、手雷、抛射物，就不应该只用 raycast，而应该创建 projectile actor 或用多帧 sweep/overlap 来模拟。

### 16.2 Mask 和过滤

`RaycastRequest.Mask` 当前保留在接口中，但 C++ 查询还没有真正按 mask 做过滤。

后续可以用 PhysX 的 query filter 实现：

- 玩家层。
- 地图层。
- 掩体层。
- 队友层。
- 触发器层。

例如开火 raycast 可能需要命中地图和敌人，但不命中自己，也不命中纯触发器。

## 17. 离房和停房释放

玩家离房时，`handleLeave` 会执行：

```go
delete(r.players, playerID)
delete(r.inputs, playerID)
r.physics.RemovePlayer(playerID)
```

C++ 层会释放对应 actor：

```cpp
actor->release()
players.erase(playerID)
```

房间停止时，`Room.loop` 的 defer 会调用：

```go
r.physics.Close()
```

C++ 层释放顺序是：

```text
所有 player actor
material
scene
dispatcher
physics
foundation
```

这个顺序不能乱。PhysX 的核心对象必须最后释放，否则 scene、actor 等对象还活着时 foundation 已经释放，会有资源生命周期风险。

## 18. 并发模型

当前 PhysX scene 的访问都发生在房间 goroutine 内：

```text
Room.handleJoin
Room.handleLeave
Room.updatePlayers
```

网络 goroutine 不直接调用 PhysX，只投递事件到房间 channel。

这样第一版不需要给 PhysX scene 加复杂锁，也避免了多个 goroutine 同时操作同一个 scene 的风险。

需要注意：PhysX scene 本身不应该被多个 goroutine 随意并发调用。后续如果要从其他 goroutine 做物理查询，应该统一投递到房间 goroutine，或者给 world 加明确锁和调用约束。

## 19. 当前边界

这版已经把 PhysX 接入 roomserver 主流程，但还不是完整战斗物理系统。

当前已完成：

- 默认 PhysX 后端。
- 每房间独立 PhysX scene。
- 玩家 capsule actor。
- 默认 ground plane。
- 服务端 tick 中通过 PhysX 推进玩家位置。
- PhysX raycast 查询。
- 玩家离房和房间停止时释放资源。

后续还需要补：

- 地图静态碰撞加载和 mesh cooking。
- raycast 命中后的伤害结算。
- `RaycastRequest.Mask` 对应的 PhysX query filter。
- AOI 遮挡接入 `BatchRaycast`。
- 高频场景下的批量移动接口。
- 更完整的角色控制能力，例如重力、跳跃、坡面、台阶、滑动。

## 20. 已执行验证

已执行过以下命令：

```bash
scripts/setup_physx.sh
go build -tags physx ./src/roomserver/cmd
scripts/build_all.sh
go test ./...
```

验证结果：

```text
scripts/setup_physx.sh 成功，PhysX SDK 已准备到 third_party/physx-sdk
go build -tags physx ./src/roomserver/cmd 成功
scripts/build_all.sh 成功，logicserver/matchserver/roomserver 全部编译完成
go test ./... 通过
```

`build_all.sh` 产物：

```text
bin/logicserver
bin/matchserver
bin/roomserver
```

其中 `bin/roomserver` 是带 `-tags physx` 的默认 PhysX 构建。
