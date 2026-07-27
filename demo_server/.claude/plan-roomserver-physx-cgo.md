# roomserver 默认启用 PhysX cgo 实现方案

## 1. 需求理解

当前 `roomserver` 已有 `PhysicsWorld` 接口，但 `SimplePhysicsWorld` 是空壳：`Raycast` 永远未命中，移动也没有真实碰撞。用户要求用 cgo 接入 PhysX，并且默认启用 PhysX；同时希望我在当前机器下载并构建 PhysX。

当前环境探测结果：

- 未找到 `PxPhysicsAPI.h`、`libPhysX*` 或 `pkg-config physx`
- `CGO_ENABLED=1`，`g++`、`make`、`curl`、`wget` 可用
- `cmake` 不存在
- `sudo apt-get` 无法执行，因为当前会话没有交互式 sudo 认证

因此实现要按“仓库自带脚本下载工具链和 PhysX SDK”的方式推进，避免依赖系统包管理器。

参考来源：

- [NVIDIA-Omniverse/PhysX](https://github.com/NVIDIA-Omniverse/PhysX)
- [PhysX BuildingWithPhysX 文档](https://nvidia-omniverse.github.io/PhysX/physx/5.1.3/docs/BuildingWithPhysX.html)
- [PhysX 文档首页](https://nvidia-omniverse.github.io/PhysX/)

## 2. 影响范围

预计新增或修改：

- `src/roomserver/logic/physics.go`
  - 扩展物理接口，加入玩家 actor 生命周期、移动推进、关闭释放
  - 保留 `Raycast` / `BatchRaycast`
- `src/roomserver/logic/room_manager.go`
  - 从共享 `PhysicsWorld` 改成每个房间通过 factory 创建独立物理世界
- `src/roomserver/logic/room.go`
  - 玩家加入时创建物理 actor，失败则拒绝入房并回滚
  - 玩家离开和房间停止时释放 actor/world
  - tick 中用物理后端推进位置
  - 开火时用 PhysX raycast
- `src/roomserver/logic/movement.go`
  - 保留输入清洗、视角归一化和方向计算
  - 移动函数改成生成移动意图，不再直接写死 clamp 作为唯一物理规则
- `src/roomserver/physx/`
  - `world.go`：Go 层 PhysXPhysicsWorld，实现 logic 层接口
  - `physx_bridge.h/.cc`：C ABI 包装 PhysX C++ API
  - `types.go`：Go/C 数据结构转换
- `src/roomserver/physicsfactory/` 或 `src/roomserver/service` 内部 factory
  - 默认创建 PhysX 后端
  - 明确配置时允许使用 simple 后端做开发回退
- `src/roomserver/config/config.go`
  - 增加 `PhysicsBackend`，默认值为 `physx`
  - 增加 PhysX 相关配置，例如默认地面、玩家 capsule 半径/高度
- `scripts/setup_physx.sh`
  - 下载用户态 CMake 到 `third_party/tools/cmake`
  - 下载 PhysX 源码到 `third_party/PhysX`
  - 构建并整理 headers/libs 到 `third_party/physx-sdk`
- `scripts/build_all.sh` / `Makefile`
  - 默认构建 roomserver 时启用 `-tags physx`
  - 设置 `CGO_CXXFLAGS` / `CGO_LDFLAGS` 或 `PHYSX_ROOT`
- `.gitignore`
  - 忽略 `third_party/PhysX`、`third_party/tools`、`third_party/physx-sdk` 等下载和构建产物
- `src/roomserver/README.md`
  - 更新 PhysX 下载、构建、运行、故障排查说明

## 3. 设计方案

### 3.1 默认启用策略

- `DefaultConfig().PhysicsBackend = "physx"`
- `scripts/build_all.sh` 默认先检查 `third_party/physx-sdk` 是否存在
- 若不存在，提示先运行 `scripts/setup_physx.sh`，或者脚本按确认后的实现直接自动执行 setup
- roomserver 默认按 `-tags physx` 编译
- 如果配置为 `physx` 但二进制没有启用 PhysX tag，启动直接返回明确错误，不静默降级
- 保留 `simple` 后端仅用于开发调试或 PhysX 排障，不作为默认行为

### 3.2 分层边界

- `logic` 层只定义接口和业务流程，不 `import "C"`
- cgo/C++ 代码集中在 `src/roomserver/physx`
- `service` 层负责根据配置创建 physics factory 并注入 `RoomManager`
- 不修改 KCP 协议、protobuf 或客户端消息格式

### 3.3 每房间独立 PhysX world

当前 `RoomManager` 将一个 physics 实例传给所有房间。真实 PhysX 下这样会导致不同房间共享 scene，玩家和 raycast 会串房间。因此改为 factory：

```go
type PhysicsWorldFactory interface {
    NewWorld(roomID string) (PhysicsWorld, error)
}
```

`RoomManager.getOrCreateRoom` 创建房间时创建独立 world；`Room.Stop` 时释放 world。

### 3.4 PhysicsWorld 扩展

拟调整为：

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

新增请求/结果：

```go
type MovePlayerRequest struct {
    PlayerID uint64
    Direction Vector3
    Distance float64
}

type MovePlayerResult struct {
    Position Vector3
    Blocked bool
}
```

第一版 PhysX world：

- 创建 foundation / physics / scene / material
- 创建默认 ground plane
- 玩家用 capsule actor 表示
- `MovePlayer` 先做 capsule sweep 或 kinematic actor 位置更新，返回最终位置
- `Raycast` 命中玩家 capsule 时返回 `TargetID`、命中点、法线、距离

### 3.5 cgo C ABI

Go 只调用 C ABI，不直接操作 C++ 对象：

- `px_world_create`
- `px_world_release`
- `px_world_add_player_capsule`
- `px_world_remove_actor`
- `px_world_move_player`
- `px_world_raycast`
- `px_world_batch_raycast`

C++ 层内部维护 `playerID -> actor` 映射。C 层不保存 Go 指针，所有跨语言传参使用基础类型或 C 数组。

## 4. 兼容性影响

- 网络协议不变，客户端仍发原 `PlayerInput`
- 默认构建链路会变重：首次需要下载 CMake 和 PhysX，并构建 C++ 库
- 默认运行行为改变：玩家移动位置由 PhysX 修正，可能和当前简单 clamp 有差异
- 开火 raycast 将开始真实命中 actor，后续可接伤害逻辑
- 如果用户没有运行 setup 或构建失败，默认 `build_all.sh` 会失败并提示原因，因为默认就是 PhysX

## 5. 健壮性

- PhysX world 创建失败时房间创建失败，不创建半初始化房间
- 玩家 actor 创建失败时拒绝入房，并回滚 `players` / `inputs`
- 离房、房间停止、服务停止都释放 PhysX 对象，重复释放安全
- `Raycast` 校验方向和距离，避免无效参数进入 C++ 层
- cgo 调用返回码统一转换为 Go error，并记录日志
- room goroutine 内串行访问对应 PhysX scene，避免第一版引入复杂锁
- setup 脚本使用固定目录，下载失败、校验失败、构建失败都显式退出

## 6. 性能考虑

- 物理调用集中在房间 tick，不在网络读 goroutine 中执行
- 默认 10 人房间下，每 tick 每玩家一次移动 cgo 调用可接受
- `BatchRaycast` 先保留接口，后续 AOI 遮挡或自动武器高频检测时批量调用
- 每房间独立 scene 避免串扰，但内存更高；后续根据实际房间规模压测
- C++ 层复用 world/actor/material，不在每帧创建 PhysX 核心对象
- setup 产物放入 `third_party` 并 gitignore，不污染源码提交

## 7. 验证方式

本次实现后执行：

1. `scripts/setup_physx.sh`
   - 下载用户态 CMake
   - 下载 PhysX 源码
   - 构建 PhysX SDK
2. `scripts/build_all.sh`
   - 默认启用 PhysX tag 编译所有服务，重点验证 roomserver
3. `go test ./src/roomserver/...`
   - 验证 roomserver 包编译和基础逻辑
4. `go build -tags physx ./src/roomserver/cmd`
   - 单独验证 roomserver PhysX 构建
5. 如构建成功，运行 roomserver 做基本启动验证

如果 PhysX 源码下载或 SDK 构建受网络/上游脚本影响失败，我会保留仓库侧改动并报告具体失败命令和错误，不会声称 PhysX 已验证通过。

## 8. 自我审查

发现并修正的问题：

1. 原方案默认 simple，不符合用户“默认启用”的要求，已改为 `PhysicsBackend=physx` 和默认 `-tags physx`
2. 当前机器没有 cmake，且 sudo 不可用，不能依赖 apt 安装，已改为用户态下载 CMake
3. 直接把 cgo 写进 logic 会破坏分层，已收敛到独立 physx 包
4. 共享物理 world 会导致跨房间碰撞污染，已改为每房间独立 world factory
5. 一次性做完整地图 mesh cooking 风险过大，第一阶段只做 ground plane、玩家 capsule、移动碰撞和 raycast
6. 高频 cgo 有潜在性能风险，但当前小房间可接受，并保留批量接口扩展
7. setup 会产生大目录和二进制产物，必须加入 `.gitignore`

## 9. 最终执行方案

确认后我会按以下顺序实施：

1. 新增 PhysX setup 脚本和 third_party ignore 规则
2. 调整 roomserver config，使默认物理后端为 `physx`
3. 扩展 `PhysicsWorld` 接口和 simple 后端，使默认以外的开发回退仍可用
4. 引入 room 级 physics factory，保证每个房间独立 PhysX world
5. 新增 `src/roomserver/physx` cgo/C++ 桥接实现
6. 修改 room tick、玩家加入/离开、房间停止流程，接入 actor 生命周期和 PhysX 移动/raycast
7. 修改构建脚本，使 roomserver 默认启用 PhysX tag 并使用本地 `third_party/physx-sdk`
8. 执行 setup、构建、测试；如外部下载/构建失败，准确报告阻塞点

等待用户确认后再修改业务代码。
