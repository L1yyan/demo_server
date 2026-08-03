# roomserver 跳跃功能实现方案

## 需求理解

服务端增加玩家跳跃功能：客户端输入携带跳跃意图，roomserver 仍保持服务端权威，按固定 tick 模拟玩家垂直运动，并通过快照/纠偏把权威位置同步给客户端。

默认语义建议为“按下跳跃键的单帧触发”：玩家在地面或接近地面时，服务端给玩家一个向上的初速度；空中再次发送 jump 不重复起跳，避免无限连跳。

## 影响范围

预计修改这些文件：

- `pb/room/room.proto`：给 `PlayerInput` 和 `PlayerInputFrame` 追加 `bool jump` 字段，保持字段编号追加，不改旧编号。
- `src/roomserver/protocol/message.go`：JSON 协议输入结构增加 `jump` 字段。
- `src/roomserver/logic/movement.go`：`authoritativeInput` 增加 Jump，并在输入清洗和旧/新输入转换中传递。
- `src/roomserver/logic/player.go`：玩家运行时状态增加垂直速度和落地状态。
- `src/roomserver/logic/physics.go`：扩展移动物理请求/结果，simple 后端支持 Y 轴重力、跳跃、落地判定。
- `src/roomserver/logic/room.go`：每 tick 模拟时把 jump、垂直速度和地面状态交给物理层，回写到玩家；复活/出生时重置跳跃相关状态。
- `src/roomserver/logic/sync.go`：权威历史帧保存跳跃相关运行时状态，纠偏和复活后状态一致。
- `src/roomserver/physx/world.go`、`src/roomserver/physx/physx_bridge.h`、`src/roomserver/physx/physx_bridge.cc`：PhysX 后端在移动时支持垂直位移 sweep，返回落地/阻挡结果。
- `src/roomserver/logic/*_test.go`、`src/roomserver/physx/world_test.go`：增加跳跃输入、空中不可重复跳、落地、非法参数测试。
- `src/roomserver/CLIENT_PREDICTION_ROLLBACK.md` 和学习文档：更新客户端接入说明。

## 设计方案

### 1. 协议设计

在输入协议追加字段：

- `PlayerInput.jump = 7`：是否请求跳跃。
- `PlayerInputFrame.jump = 8`：批量输入帧是否请求跳跃，保留 `predicted_state = 7` 不变。

JSON 侧字段名为 `jump`。旧客户端不传该字段时默认为 `false`，兼容现有移动/射击。

暂不新增单独的 `MsgJump`。跳跃属于每帧输入的一部分，跟移动、视角、开火一起走 `MsgPlayerInput` / `MsgPlayerInputBatch`，这样能复用现有输入窗口、ack、预测和纠偏链路。

### 2. 运行时状态

在 `Player` 中增加：

- `VerticalVelocity float64`：当前 Y 轴速度。
- `Grounded bool`：服务端判定是否在地面。

出生、入房、复活时：

- `VerticalVelocity = 0`
- `Grounded = true`

权威历史 `playerFrameState` 也保存这两个字段，保证纠偏和复活后清理未来输入时不会留下空中状态。

### 3. 跳跃模拟

服务端每 tick 执行：

1. 清洗输入，`jump` 只作为 bool 传递，不允许客户端提供速度或 Y 坐标。
2. 如果 `jump == true && player.Grounded == true`，设置 `VerticalVelocity = defaultPlayerJumpSpeed`。
3. 每 tick 对 `VerticalVelocity` 施加重力：`VerticalVelocity += gravity * deltaTime`。
4. 水平移动仍按现有 `move_x/move_z/yaw` 计算，位移为水平速度乘 `deltaTime`。
5. 垂直位移为 `VerticalVelocity * deltaTime`，由物理层和地面/地图碰撞修正。
6. 如果本 tick 触地，回写 `Grounded = true`，并把向下速度归零；否则 `Grounded = false`。

建议常量第一版直接放在 logic 层：

- `defaultPlayerJumpSpeed = 5.0`
- `defaultPlayerGravity = -9.8`
- `defaultGroundSnapDistance = 0.05`

这些值暂不进 YAML，避免把配置链路扩大；后续手感稳定后再暴露配置。

### 4. 物理接口

扩展现有 `MovePlayerRequest`，避免新增第二套物理调用：

- 增加 `VerticalVelocity float64`
- 增加 `Jump bool`
- 增加 `Grounded bool`

扩展 `MovePlayerResult`：

- 增加 `VerticalVelocity float64`
- 增加 `Grounded bool`

`buildMovePlayerRequest` 仍负责把输入转成水平移动方向和距离，同时带上玩家当前垂直状态。`Room.simulatePlayerTick` 统一调用一次 `physics.MovePlayer`，减少每帧跨 cgo 调用次数。

### 5. simple 后端

simple 后端用于单测和兜底：

- 水平 X/Z 继续按世界边界 clamp。
- Y 轴按重力和跳跃速度推进。
- 地面简化为 `Y >= 0`，低于 0 时钳到 0，`Grounded = true`，下落速度清零。

这个后端不做斜坡/台阶/复杂地形，只验证服务端跳跃状态机和输入语义。

### 6. PhysX 后端

当前 PhysX 玩家 actor 是 kinematic capsule 且禁用 gravity，移动通过 `scene->sweep` 实现。跳跃第一版建议继续保持 kinematic，不改为动态刚体：

- Go 层计算本 tick 合成位移方向和距离，包含水平位移和垂直位移。
- C++ `px_world_move_player` 对合成位移做一次 capsule sweep，避免水平和垂直拆成多次 cgo 调用。
- sweep 被阻挡时沿可行距离前进；如果阻挡法线向上，判定落地；如果上方阻挡，清掉向上速度。
- 额外做一个短距离向下 sweep 或 raycast，用来判断站在地面上的 `Grounded` 状态。

这能保留现有服务端权威和地图静态 box 阻挡逻辑，改动集中在 PhysX bridge，不引入复杂角色控制器。

### 7. 输入沿用和开火语义

当前缺帧沿用上一帧输入时会强制 `Fire = false`。跳跃也应同样强制 `Jump = false`，避免网络缺帧时一次跳跃被重复触发。

同一 tick 重复输入仍保留先到的一帧，后到不覆盖。

### 8. 快照和纠偏

位置 Y 已经在 `PlayerState`/`PredictedPlayerState` 中存在，所以跳跃位置误差可直接复用现有三维 `positionError`。

第一版不把 `VerticalVelocity` 下发给客户端，客户端纠偏时以服务端位置为准并重放本地输入。如果客户端预测接入时发现空中纠偏收敛慢，再追加速度字段。

## 兼容性影响

- `.proto` 只追加字段，不改现有字段编号，protobuf 层兼容。
- JSON 旧客户端不传 `jump` 时默认为 `false`，旧功能不受影响。
- 服务端仍不信任客户端坐标或速度，客户端只能提交跳跃意图。
- 如果客户端尚未实现跳跃预测，只按 snapshot 同步也能看到服务端 Y 坐标变化。
- PhysX ABI 会变更，需要 Go/C++ 同步修改；未启用 `physx` tag 的默认 Go 测试仍能通过 simple 后端。

## 健壮性

- 输入清洗拒绝 NaN/Inf，jump 只接受 bool。
- tickRate 非法时沿用现有默认保护。
- 空中 jump 不触发，避免无限跳。
- 缺帧沿用时强制 `Jump = false`，避免一次按键被重复消费。
- 物理层遇到非法速度、位移或关闭 world 时返回错误，房间按现有逻辑记录日志并发送当前纠偏。
- 复活、出生和传送时重置垂直速度和地面状态，避免死亡前的空中速度污染复活状态。

## 性能考虑

- 仍保持每玩家每 tick 一次 `MovePlayer`，不会因为跳跃额外增加 cgo 调用次数。
- simple 后端只增加常量级计算。
- PhysX 后端增加一次合成 sweep 和必要的短距离落地检测，玩家数当前默认 2，对现有压力影响很小。
- 不引入 goroutine、无界队列或额外跨层依赖。

## 验证方式

计划执行：

```bash
gofmt -w src/roomserver/logic src/roomserver/protocol src/roomserver/physx
go test ./src/roomserver/logic ./src/roomserver/protocol ./src/roomserver/config
```

如果本机 PhysX SDK 可用，再执行：

```bash
go test -tags physx ./src/roomserver/physx
```

如果修改了 `.proto` 并本机有 `protoc` 插件，再执行：

```bash
make proto
```

## 自我审查

### 原始方案风险

1. 直接把跳跃速度下发到协议，会扩大协议面，客户端和服务端更容易出现速度语义不一致。
2. 使用 PhysX dynamic rigid body 看似更真实，但会改变现有 kinematic capsule 的移动模型，碰撞、同步和测试都会变复杂。
3. 如果把跳跃配置一次性接入 YAML，需要同时改总配置加载链路；当前 roomserver 入口还没有统一读取 YAML，会扩大需求范围。
4. 只在 simple 后端实现跳跃会导致默认 physx 后端运行时不一致，不适合真实服务端。

### 修正后的最终方案

采用最小协议追加：输入新增 `jump`，服务端内部维护 `VerticalVelocity/Grounded`，仍通过现有 `x/y/z` 做快照和纠偏；物理层扩展 `MovePlayer`，simple 与 PhysX 后端都支持跳跃；暂不暴露跳跃速度和重力配置，暂不新增独立跳跃消息，暂不切换 PhysX dynamic actor。

等待你确认后，我再开始改代码。
