# roomserver 客户端预测、服务端权威校验与回滚重放详细方案

## 0. 结论先行

建议把同步系统拆成两层：

1. **服务端永远权威**：客户端只上报输入和预测结果，服务端只信任输入意图，不信任客户端坐标、血量、命中等最终状态。
2. **客户端负责体验**：客户端收到输入后立刻本地模拟，降低操作延迟；一旦服务端发现客户端预测偏离，就下发指定帧的权威状态，客户端回滚到该帧，再用本地保存的输入快速重放到当前帧。

第一版不建议直接做“服务端自身历史回滚 + 重新模拟整个房间”。当前项目的 PhysX world 还没有完整世界快照能力，强行做会把范围扩大到动态物体、物理场景快照、命中结算回放。更稳的第一版是：

- 服务端按帧保存最近一段权威历史
- 服务端按帧校验客户端预测关键帧
- 服务端发现误差过大时，给客户端发 correction
- 客户端执行回滚重放
- 迟到太久的客户端输入不改写服务端历史，直接丢弃并纠偏

等这条链路跑通后，再评估是否需要服务端侧 rollback replay。

## 1. 需求理解

用户希望把当前 roomserver 的同步方式从“服务端计算并快照同步”升级为：

```text
客户端输入
  -> 客户端按与服务端相同 tick 进行本地预测
  -> 客户端记录 tick、输入和预测状态并发给服务端
  -> 服务端用同一套物理规则推进权威状态
  -> 服务端按关键帧校验客户端预测状态
  -> 如果差异过大，服务端把客户端拉回到出错帧
  -> 客户端从该帧开始用历史输入快速重放到最新帧
```

当前代码基础：

- `src/roomserver/protocol/message.go` 已有 `MsgPlayerInput`、`MsgSnapshot`
- `PlayerInput` 已带 `ClientTick`
- `Room` 已有固定 tick 循环
- 移动已从“收到输入就改坐标”升级为“tick 中由服务端推进”
- `PhysicsWorld.MovePlayer` 已能通过 simple/PhysX 后端计算最终位置
- `Snapshot` 已能把服务端权威状态发给客户端

当前主要缺口：

- 服务端只缓存每个玩家最近一次输入，没有按 tick 保存输入序列
- 客户端没有上报预测状态，服务端无法知道客户端预测在哪一帧开始偏离
- 服务端没有输入确认 ack，客户端不知道哪些输入已经被服务端处理
- 服务端没有 correction 消息，客户端不知道应该回滚到哪一帧
- 服务端没有最近 N 帧权威历史，无法稳定地下发“出错帧”的权威状态
- PhysX `px_world_move_player` 内部固定 `simulate(1/60)`，与房间 `tick_rate=20` 不一致，不适合作为预测一致性的基础
- PhysX 后端缺少玩家状态读写接口，后续做回滚或状态恢复会受限

## 2. 为什么要这样设计

### 2.1 为什么客户端要预测

**原理角度**：网络游戏里，玩家按下移动键后，如果必须等服务端返回 snapshot 才显示移动，操作延迟至少等于半个 RTT 到一个 RTT。即使 RTT 只有 60ms，玩家也能明显感觉角色“慢半拍”。客户端预测的核心是把“输入反馈”提前到本地：客户端先假设服务端会认可这次输入，并立即模拟显示。

**实用角度**：当前 roomserver 默认 `tick_rate=20`，每 tick 50ms；`snapshot_rate=10`，每 100ms 一个快照。如果完全依赖服务端快照，客户端运动会天然有 100ms 级更新粒度，再叠加网络延迟，手感会很差。客户端预测能把本地玩家移动反馈压到本机帧率级别。

**安全角度**：预测只影响客户端自己的临时显示，最终状态仍由服务端覆盖，因此不会把权威交给客户端。

### 2.2 为什么服务端只信输入，不信状态

**原理角度**：输入是“意图”，状态是“结果”。移动结果应该由服务端根据速度、碰撞、地图、tick 和规则计算。如果客户端可以直接提交状态，就等于允许客户端说“我现在在这里”，服务端很难区分正常移动和瞬移作弊。

**实用角度**：当前 `PlayerInput` 已经是输入意图结构，包含 `move_x/move_z/yaw/pitch/fire`。沿用这个方向，改动比“客户端提交完整状态并让服务端反校验”更小，也更符合 FPS/TPS 常见架构。

**反作弊角度**：服务端可以对输入做清洗和限制：

- `MoveX/MoveZ` 限制到 `[-1, 1]`
- 斜向移动归一化
- `Yaw/Pitch` 做合法范围处理
- 每 tick 最大移动距离由服务端速度配置决定
- `Fire` 只在对应输入帧触发，不能靠重复包刷开火

所以客户端即使构造异常包，也只能表达有限输入，不能直接制造非法结果。

### 2.3 为什么要上报预测关键帧状态

**原理角度**：服务端仅靠输入可以计算权威状态，但不知道客户端本地预测是否和服务端一致。客户端上报预测状态后，服务端可以比较：

```text
client_predicted_state[tick]
vs
server_authoritative_state[tick]
```

如果差异小，说明客户端预测还在可接受范围；如果差异大，说明客户端需要回滚纠偏。

**实用角度**：预测状态不必每 tick 上报。可以按关键帧上报，例如每 2 或 3 tick 带一次状态。这样既能发现偏离，又不会显著增加带宽。第一版调试阶段可以每 tick 上报，联调稳定后改成配置化间隔。

**安全角度**：预测状态只用于误差检测，不用于覆盖服务端状态。客户端把自己预测成墙外或高速位移，只会触发 correction，不会让服务端承认这个位置。

### 2.4 为什么不用“完全一致”判断，而要用误差阈值

**原理角度**：即使客户端和服务端使用同样规则，也很难做到跨平台、跨语言、跨物理引擎 bit-level 完全一致。浮点计算、PhysX 版本、Unity 物理封装、CPU 浮点行为、碰撞查询细节都可能造成微小差异。

**实用角度**：如果用 `client_pos == server_pos` 精确比较，客户端可能频繁被极小浮点误差纠偏，导致画面抖动、手感变差。用阈值能把“无感误差”留给客户端平滑处理，只对真正影响公平性或碰撞结果的偏移做 correction。

建议默认阈值：

- `position_tolerance = 0.15`：普通位置误差阈值
- `hard_position_tolerance = 0.5`：硬纠偏阈值，超过立即纠偏
- `angle_tolerance = 2.0`：视角误差阈值，单位度

这些值必须配置化，后续根据地图尺度、角色速度和联调误差分布调整。

### 2.5 为什么服务端第一版不做自身回滚

用户描述里提到“服务端发现错误这一帧，也就是回溯，然后再快速模拟物理到最后一帧”。这里要区分两件事：

1. **客户端回滚**：客户端收到服务端 correction 后，回到服务端指定帧，再用本地输入重放到当前帧。
2. **服务端回滚**：服务端收到迟到输入或发现历史错误后，把整个房间恢复到旧帧，然后重放到当前帧。

第一版建议先做客户端回滚，不做服务端自身回滚。

原因：

- 当前房间只有玩家 kinematic actor 和地图静态碰撞，表面上可以只恢复玩家坐标
- 但后续一旦加入动态箱子、投掷物、门、机关、伤害事件、命中结算，只恢复玩家坐标就不够
- PhysX world 目前没有完整 scene snapshot，只支持移动和 raycast
- 服务端回滚需要保存完整房间状态、物理状态、输入历史、随机数状态和事件历史，否则重放结果不可控
- 服务端每次回滚还会带来额外物理模拟开销，必须限制触发频率和窗口

更实用的做法是：

- 服务端固定向前推进，过旧输入不改写历史
- 服务端保存历史只用于纠偏客户端
- 客户端负责回滚重放自己的预测
- 后续如果要做射击延迟补偿或服务器接收历史输入，再单独做服务端回滚模块

### 2.6 为什么要做 tick 对齐，而不是直接用时间戳

**原理角度**：预测和回滚需要可复现的离散步骤。tick 是离散帧号，能明确表示“第 N 帧输入产生第 N 帧状态”。时间戳是连续值，会受时钟漂移、延迟估算和浮点误差影响，不适合直接作为模拟索引。

**实用角度**：当前 `Room` 已经有 `r.tick` 和固定 `tick_rate`，`PlayerInput` 已有 `ClientTick`。继续用 tick 作为同步单位，改动更贴合现有结构。

需要注意：客户端 tick 不应长期自由运行。客户端要根据服务端 `JoinRoomAck.Tick`、`HeartbeatAck.ServerTick`、RTT 估算和微调本地模拟 tick，避免长期漂移。

## 3. 当前架构与改造边界

当前链路：

```text
KCP Session
  -> Server.HandleMessage
  -> Server.handlePlayerInput
  -> RoomManager.PushInput
  -> Room.PushInput
  -> Room.handleInput 缓存最近输入
  -> Room.update 每 tick 推进玩家
  -> PhysicsWorld.MovePlayer
  -> Room.broadcastSnapshots
```

改造后链路：

```text
客户端本地 tick
  -> 记录 input[tick]
  -> 本地预测 state[tick]
  -> 批量发送 PlayerInputBatch

服务端 KCP Session
  -> service 解包并投递
  -> logic 按玩家保存 input frame buffer
  -> Room.update 在服务端 tick N 取 input[N]
  -> PhysicsWorld 按固定 dt 推进权威状态
  -> 保存 authoritative_history[N]
  -> 校验 predicted_state[N]
  -> 发送 InputAck / StateCorrection / Snapshot

客户端收到 correction
  -> state = server_state[rollback_tick]
  -> replay input[rollback_tick+1..local_latest_tick]
  -> 渲染层做平滑，模拟层立即服从权威
```

分层边界保持不变：

- `service`：只做消息解析、基础校验、调用 `RoomManager`
- `logic`：处理输入帧、tick 模拟、误差校验、纠偏策略
- `physx`：只提供物理查询、移动、状态读写能力，不处理网络协议
- `protocol` / `pb`：只定义消息结构，不写业务规则

## 4. 协议设计

### 4.1 消息类型

当前消息类型：

```go
MsgJoinRoom    = 1
MsgJoinRoomAck = 2
MsgHeartbeat   = 3
MsgHeartbeatAck = 4
MsgPlayerInput = 5
MsgSnapshot    = 6
MsgError       = 7
```

建议追加：

```go
MsgPlayerInputBatch uint16 = 8  // 批量玩家输入
MsgInputAck         uint16 = 9  // 输入处理确认
MsgStateCorrection  uint16 = 10 // 权威状态纠偏
```

为什么新增消息，而不是直接改 `MsgPlayerInput`：

- 兼容旧客户端，旧客户端继续发单帧输入
- 新客户端明确使用 batch 和预测状态
- 服务端可按客户端能力决定是否发送 ack/correction
- 排障更清晰，抓包时消息类型能直接区分同步阶段

### 4.2 JoinRoomAck 扩展

建议在 `JoinRoomAck` 追加字段：

```go
type JoinRoomAck struct {
    OK bool
    RoomID string
    Content string
    Tick int64
    SpawnID string
    X, Y, Z float64
    Yaw, Pitch float64

    TickRate int       `json:"tick_rate"`
    SnapshotRate int   `json:"snapshot_rate"`
    ServerTime int64   `json:"server_time"`
    SyncMode string    `json:"sync_mode"`
    MapID string       `json:"map_id"`
    PhysicsHash string `json:"physics_hash"`
    RollbackWindowTicks int64 `json:"rollback_window_ticks"`
    FutureInputWindowTicks int64 `json:"future_input_window_ticks"`
    PredictionKeyframeInterval int64 `json:"prediction_keyframe_interval"`
    PositionTolerance float64 `json:"position_tolerance"`
    HardPositionTolerance float64 `json:"hard_position_tolerance"`
    AngleTolerance float64 `json:"angle_tolerance"`
}
```

解释：

- `TickRate`：客户端必须用同样的逻辑帧率模拟
- `SnapshotRate`：客户端知道服务端快照频率，用于插值其他玩家
- `ServerTime`：用于估算服务端当前 tick
- `SyncMode`：例如 `snapshot_only` / `prediction_authoritative`
- `MapID` 和 `PhysicsHash`：确认客户端加载的是同一份物理地图
- 各种阈值和窗口：避免客户端硬编码服务端策略

### 4.3 HeartbeatAck 扩展

当前心跳只返回 `server_time`。建议追加：

```go
type Heartbeat struct {
    ClientTime int64 `json:"client_time"`
    ServerTime int64 `json:"server_time"`
    ServerTick int64 `json:"server_tick"`
}
```

为什么要在心跳里带 `server_tick`：

- 客户端可以持续估算 tick 偏移，避免本地 tick 长期漂移
- RTT 抖动时，客户端可以用多次 heartbeat 做平滑估计
- 不需要每个 snapshot 都承担时间同步职责

service 层不应直接访问 room 复杂状态，可由 `RoomManager` 提供轻量方法：按 `playerID` 查询当前房间 tick。查不到则返回 0。

### 4.4 批量输入结构

建议协议：

```go
type PlayerInputBatch struct {
    BaseClientTick int64 `json:"base_client_tick"`
    Frames []PlayerInputFrame `json:"frames"`
    LastReceivedServerTick int64 `json:"last_received_server_tick"`
}

type PlayerInputFrame struct {
    ClientTick int64 `json:"client_tick"`
    MoveX float64 `json:"move_x"`
    MoveZ float64 `json:"move_z"`
    Yaw float64 `json:"yaw"`
    Pitch float64 `json:"pitch"`
    Fire bool `json:"fire"`
    PredictedState *PredictedPlayerState `json:"predicted_state,omitempty"`
}

type PredictedPlayerState struct {
    X float64 `json:"x"`
    Y float64 `json:"y"`
    Z float64 `json:"z"`
    Yaw float64 `json:"yaw"`
    Pitch float64 `json:"pitch"`
    StateHash uint32 `json:"state_hash,omitempty"`
}
```

为什么 batch：

- KCP 本身有包头和流控成本，每 tick 单包会增加网络开销
- batch 可以一次带最近几帧输入，也可以重复带未 ack 输入，提高抗丢包能力
- 服务端可以限制 `max_input_batch_frames`，避免恶意客户端发超大数组

`BaseClientTick` 的作用：

- 方便客户端表达一个连续帧段
- 后续如果要压缩，可以让每个 frame 使用相对 tick
- 第一版也可以保留每帧 `ClientTick`，避免乱序或缺帧时难排查

`LastReceivedServerTick` 的作用：

- 客户端告诉服务端自己已经看到哪个服务端快照
- 后续可以用于统计延迟、判断客户端落后程度
- 第一版不强依赖它做业务决策

### 4.5 InputAck

建议：

```go
type InputAck struct {
    ServerTick int64 `json:"server_tick"`
    LastAcceptedInputTick int64 `json:"last_accepted_input_tick"`
    LastVerifiedInputTick int64 `json:"last_verified_input_tick"`
}
```

作用：

- 客户端可以删除已确认的旧输入，控制本地历史缓冲大小
- 客户端知道服务端已经模拟到哪里
- 服务端可以不每 tick 单独发送 ack，而是附带在 snapshot 或按 `snapshot_rate` 发送

### 4.6 StateCorrection

建议：

```go
type StateCorrection struct {
    PlayerID uint64 `json:"player_id"`
    RollbackTick int64 `json:"rollback_tick"`
    ServerTick int64 `json:"server_tick"`
    LastAcceptedInputTick int64 `json:"last_accepted_input_tick"`
    State PlayerState `json:"state"`
    Reason string `json:"reason"`
    PositionError float64 `json:"position_error"`
    AngleError float64 `json:"angle_error"`
}
```

字段解释：

- `RollbackTick`：客户端必须回到哪一帧
- `ServerTick`：服务端发送 correction 时的当前帧
- `LastAcceptedInputTick`：服务端最后接受的输入帧，客户端可据此丢弃更老输入
- `State`：服务端在 `RollbackTick` 的权威状态
- `Reason`：调试用，例如 `position_error`、`angle_error`、`stale_input`、`physics_hash_mismatch`
- `PositionError/AngleError`：方便联调统计，不参与客户端权威判断

为什么 correction 用指定旧帧，而不是只发当前帧：

- 客户端预测历史是一条输入序列
- 如果只把当前帧位置硬改为服务端位置，会出现速度突变和后续输入对不上
- 回到出错帧再重放，可以最大化保留玩家之后输入的效果，手感更自然

## 5. 服务端数据结构设计

### 5.1 玩家同步状态

建议在 `logic` 层新增内部结构：

```go
type playerSyncState struct {
    inputs inputRingBuffer
    predictedStates predictedStateRingBuffer
    authoritativeHistory stateRingBuffer
    lastInput authoritativeInput
    hasLastInput bool
    lastAppliedTick int64
    lastAcceptedInputTick int64
    lastVerifiedTick int64
    lastCorrectionTick int64
}
```

为什么用 ring buffer，不用无限 map：

- 同步只关心最近几秒历史，旧帧没有价值
- ring buffer 内存固定，避免恶意客户端不断发未来/历史 tick 造成 map 膨胀
- tick 是递增整数，ring buffer 很适合按 `tick % capacity` 定位

第一版为了代码简单，也可以先用 `map[int64]... + cleanup`，但要配置最大窗口并定期清理。最终更推荐 ring buffer。

### 5.2 房间级同步配置

建议 `Room` 持有同步参数：

```go
type SyncConfig struct {
    PredictionEnabled bool
    RollbackWindowTicks int64
    FutureInputWindowTicks int64
    PredictionKeyframeInterval int64
    PositionTolerance float64
    HardPositionTolerance float64
    AngleTolerance float64
    MaxInputBatchFrames int
}
```

默认值建议：

- `PredictionEnabled = true`
- `RollbackWindowTicks = tickRate * 3`，默认 60 tick，约 3 秒
- `FutureInputWindowTicks = 8`，约 400ms
- `PredictionKeyframeInterval = 2`，每 100ms 校验一次，和当前 snapshot_rate 接近
- `PositionTolerance = 0.15`
- `HardPositionTolerance = 0.5`
- `AngleTolerance = 2.0`
- `MaxInputBatchFrames = 8`

为什么窗口不是无限大：

- 太旧输入会破坏当前权威时间线
- 服务端保存过多历史会增加内存和潜在重放成本
- 实时对战里，几秒前的输入已经不应影响当前权威状态

### 5.3 权威帧状态

建议内部结构：

```go
type playerFrameState struct {
    Tick int64
    PlayerID uint64
    Position Vector3
    Yaw float64
    Pitch float64
    HP int
    Alive bool
    SpawnID string
}
```

和 `protocol.PlayerState` 分开，是因为内部状态可能需要更多字段，例如 `Alive`、状态标记、物理状态；协议状态只保留对客户端需要的字段。

## 6. Tick 对齐设计

### 6.1 入房时对齐

入房成功后，服务端返回：

```text
server_tick = T0
server_time = S0
 tick_rate = R
```

客户端收到 ack 时记录本地时间 `C1`，如果有 RTT 估算，可以估算当前服务端 tick：

```text
estimated_server_tick = T0 + ((C1 - S0 - rtt/2) / 1000) * R
```

不需要第一版公式非常精确，但客户端必须知道：服务端 tick 是唯一参考，本地 tick 要围绕服务端 tick 做微调。

### 6.2 输入提前量

实战中客户端通常会发送略微超前的输入，例如提前 1 到 3 tick。原因是网络延迟会让服务端在 tick N 时还没收到 input[N]。

服务端允许一个 `future_input_window`：

```text
client_tick <= server_tick + future_input_window
```

这样客户端可以提前发送未来几帧输入，服务端到对应帧时就能使用。

如果未来太多，服务端丢弃：

- 防止客户端占用大量缓冲
- 防止客户端预提交太多输入后再试图用乱序包改变历史
- 保持实时性

### 6.3 本地 tick 漂移修正

客户端不应频繁硬改本地 tick。建议策略：

- 小漂移：通过轻微加快/放慢本地模拟节奏修正
- 大漂移：直接对齐到服务端估算 tick，并准备接受 correction
- 如果长期落后：减少渲染插值延迟或请求重同步

服务端只负责提供 `server_tick/server_time`，具体平滑策略在客户端实现。

## 7. 服务端处理流程

### 7.1 service 层

新增处理：

```text
Server.HandleMessage
  -> MsgPlayerInput: handlePlayerInput 旧单帧兼容
  -> MsgPlayerInputBatch: handlePlayerInputBatch
```

service 层只做：

- session 是否已入房
- JSON decode
- batch 基础长度限制
- 调用 `RoomManager.PushInputBatch`

不做：

- tick 校验
- 误差比较
- 物理移动
- correction 生成

这样符合项目 `service -> logic -> repo` 分层要求。

### 7.2 RoomManager 层

新增：

```go
func (m *RoomManager) PushInputBatch(playerID uint64, batch protocol.PlayerInputBatch) error
func (m *RoomManager) RoomTick(playerID uint64) int64
```

`PushInputBatch` 找房间并投递事件，不处理业务规则。

`RoomTick` 给心跳返回 `server_tick` 用，注意只需要轻量查询，不能让 service 直接改房间状态。

### 7.3 Room 事件层

事件类型新增或复用：

```go
roomEventInputBatch
```

`handleInputBatch` 流程：

```text
1. 找玩家，确认存在且存活
2. 校验 batch frame 数量
3. 遍历 frame
4. sanitizePlayerInput
5. 校验 tick 窗口
6. 保存到 input buffer
7. 如果 frame 带 predicted_state，保存到 predicted buffer
8. 更新 lastAcceptedInputTick
```

tick 窗口：

```text
太旧: frame_tick < current_server_tick - rollback_window
太新: frame_tick > current_server_tick + future_input_window
```

太旧输入：

- 不改写服务端历史
- 可以发送 correction，让客户端回到服务端当前认可状态

重复输入：

- 建议忽略后到帧
- 原因：同一 tick 的输入应当稳定，允许覆盖会让乱序网络包改变已验证输入

### 7.4 Room.update 推进

当前 `update`：

```go
r.tick++
r.updatePlayers(ctx)
...
r.broadcastSnapshots(ctx)
```

改造后 `updatePlayers` 逻辑：

```text
当前 tick = r.tick
对每个玩家：
  input = getInputForTick(playerID, r.tick)
  simulatePlayerTick(player, input)
  saveAuthoritativeHistory(playerID, r.tick, player state)
  verifyPredictedStateIfExists(playerID, r.tick)
```

输入缺帧策略建议：

- 移动输入：短时间缺帧可沿用最近一次有效输入，避免弱网时角色瞬间停顿
- Fire 输入：不能沿用，只能在对应 tick 的输入帧触发一次
- 视角：可沿用最近 yaw/pitch，或者缺帧时保持当前视角

为什么 Fire 不能沿用：

- 如果最近输入 `Fire=true` 被当成持续状态，会导致一次点击在丢包/缺帧时重复开火
- 射击应按输入帧边沿或武器射速系统处理，不能和移动连续输入完全一样

## 8. 误差校验设计

### 8.1 校验内容

首版只校验本地玩家：

- `X/Y/Z`
- `Yaw/Pitch`

暂不校验：

- HP
- 命中结果
- 弹药
- buff
- 动态物体

原因：当前玩法还没有完整伤害和命中结算，先把移动预测跑通，避免一次性扩大范围。

### 8.2 位置误差

```go
positionError := distance(client.Position, server.Position)
```

处理建议：

- `positionError <= position_tolerance`：认为预测可接受
- `positionError > hard_position_tolerance`：立即 correction
- 中间区域：可以累计连续超阈值次数，或第一版直接 correction

第一版为了简单，建议：普通阈值直接 correction；后续联调如果 correction 太频繁，再加连续计数。

### 8.3 角度误差

Yaw 要使用归一化角度差，不能直接相减。例如 `179` 和 `-179` 实际只差 2 度。

```text
angleDiff = abs(normalizeDegrees(clientYaw - serverYaw))
```

Pitch 可直接相减后取绝对值，因为 pitch 已限制在 `[-89, 89]`。

### 8.4 correction 限频

恶劣网络或客户端物理不一致时，服务端可能连续每帧 correction。需要限频：

- 同一玩家 correction 最小间隔，例如 2 tick 或 100ms
- 超过 hard tolerance 时不受普通限频约束，立即纠偏

为什么要限频：

- 保护网络带宽
- 避免客户端频繁重放造成卡顿
- 给客户端一次 correction 后的重放留出收敛时间

## 9. 物理一致性设计

### 9.1 当前必须修正的问题

当前 PhysX C++ 移动里固定：

```cpp
world->scene->simulate(1.0f / 60.0f);
```

但房间默认 `tick_rate=20`，每 tick 是 50ms。客户端和服务端要按同 tick 模拟，这里必须改成由 Go 层传入 dt 或 tickRate。

建议改接口：

```go
type MovePlayerRequest struct {
    PlayerID uint64
    Direction Vector3
    Distance float64
    DeltaTime float64
}
```

或者如果当前移动完全按 sweep 距离决定，PhysX simulate 只用于 kinematic target 刷新，也应至少传入 `1/tickRate`，避免后续动态对象引入后时间步不一致。

### 9.2 玩家状态读写接口

建议扩展 `PhysicsWorld`：

```go
GetPlayerState(playerID uint64) (PhysicsPlayerState, error)
SetPlayerState(playerID uint64, state PhysicsPlayerState) error
```

或更简单：

```go
GetPlayerPosition(playerID uint64) (Vector3, error)
SetPlayerPosition(playerID uint64, position Vector3) error
```

第一版只需要玩家 position。后续如果物理状态增加速度、grounded、姿态，再扩展为完整 `PhysicsPlayerState`。

为什么要有 set/get：

- 服务端保存历史和 correction 时可以确认物理后端状态与 Go `Player` 状态一致
- 后续服务端回滚时需要恢复玩家 actor 位置
- 测试可以验证 PhysX actor 状态不会和 Go 状态漂移

### 9.3 地图物理 hash

客户端必须加载与服务端相同的地图碰撞数据。当前 `configs/maps/map_001/collision.json` 已有 `physics_hash`。

建议：

- `JoinRoomAck` 下发 `map_id` 和 `physics_hash`
- 客户端首个预测同步包可回传自己加载的 `physics_hash`
- 不一致时，服务端不启用预测模式，或者直接拒绝入房/要求重新加载资源

为什么 hash 重要：

- 地图碰撞不同会导致预测长期偏离
- 阈值只能处理浮点误差，不能处理“墙的位置不一样”
- 提前发现资源不一致比不断 correction 更容易排查

### 9.4 关于“同一套物理规则”的边界

需要明确：如果客户端是 Unity，服务端是 Linux cgo PhysX，即使都叫 PhysX，也不一定完全确定。不同平台和封装会有差异。

首版目标应定义为：

```text
同一套规则参数 + 同一份地图物理数据 + 固定 tick + 可接受误差阈值 + 服务端权威纠偏
```

不是：

```text
客户端和服务端每一帧浮点结果完全一致
```

如果未来必须追求强确定性，有两条路线：

1. 抽出一套共享 C++ 角色控制器，客户端和服务端都调用同一份逻辑
2. 使用 fixed-point 的自研 KCC，牺牲一部分物理复杂度换确定性

当前项目更适合先走第一版实用路线。

## 10. 客户端接入要求

虽然本次主要改服务端，但客户端必须配合以下逻辑，否则服务端方案无法闭环。

客户端需要维护：

```text
inputHistory[tick]
predictedStateHistory[tick]
lastAckedInputTick
latestLocalTick
estimatedServerTick
```

每个本地 tick：

```text
1. 采集输入
2. 保存 inputHistory[tick]
3. 用本地物理/角色控制模拟一帧
4. 保存 predictedStateHistory[tick]
5. 把最近未 ack 的输入按 batch 发送给服务端
```

收到 `InputAck`：

```text
删除 <= last_accepted_input_tick 的历史输入和预测状态
保留 rollback_window 内必要历史
```

收到 `StateCorrection`：

```text
1. 找到 rollback_tick
2. 用服务端 state 覆盖本地模拟状态
3. for tick in rollback_tick+1..latestLocalTick:
       input = inputHistory[tick]
       simulate(input)
4. 渲染层从旧显示位置平滑过渡到新模拟位置
```

为什么模拟层要立即覆盖，而渲染层可平滑：

- 模拟层必须服从权威，否则后续输入仍基于错误状态
- 渲染层可以做短时间插值，减少画面瞬移感
- 这能兼顾公平性和观感

其他玩家不建议用预测，先用 snapshot 插值：

- 自己：客户端预测 + correction
- 其他玩家：服务端 snapshot + 插值/外推

原因：客户端没有其他玩家的真实输入，只能根据快照插值，不能可靠预测。

## 11. 兼容性影响

协议兼容策略：

- 旧 `MsgPlayerInput` 保留
- 新客户端使用 `MsgPlayerInputBatch`
- `pb/room/room.proto` 只追加字段和 message，不删除字段、不复用编号
- JSON payload 追加字段对宽松客户端一般兼容，但未知新消息不要发给旧客户端
- 可通过 `sync_mode` 或后续 `JoinRoomRequest` 中的 `sync_version` 做能力协商

建议新增能力协商：

```go
type JoinRoomRequest struct {
    Token string `json:"token"`
    SyncVersion int `json:"sync_version,omitempty"`
    PredictionEnabled bool `json:"prediction_enabled,omitempty"`
    PhysicsHash string `json:"physics_hash,omitempty"`
}
```

如果客户端不声明预测能力，服务端按旧模式：

- 接收单帧输入
- 服务端权威推进
- 广播 snapshot
- 不发送 InputAck/StateCorrection

## 12. 错误处理与边界情况

### 12.1 非法输入

处理：

- NaN/Inf：丢弃该 frame
- move 超范围：clamp + normalize
- yaw/pitch 非法：丢弃或归一化/夹取，沿用当前 `sanitizePlayerInput`
- batch 超过最大帧数：整个 batch 拒绝或只处理前 N 帧，建议拒绝并返回错误码

### 12.2 过旧输入

条件：

```text
input_tick < server_tick - rollback_window_ticks
```

处理：

- 丢弃
- 发送 `InputAck` 或 `StateCorrection`
- 不让旧输入改写服务端历史

原因：实时对战中，太旧输入再生效会造成其他玩家看到的世界线被改写，代价大于收益。

### 12.3 过远未来输入

条件：

```text
input_tick > server_tick + future_input_window_ticks
```

处理：

- 丢弃
- 可限频记录 warn

原因：防止客户端提前塞大量输入，占用缓冲或试探服务端 tick 窗口。

### 12.4 乱序与重复包

KCP 流模式整体有序，但应用层 batch 可能重复发送未 ack 输入。服务端处理同 tick 重复输入时，建议：

- 如果该 tick 未处理，已存在输入则忽略后到帧
- 如果该 tick 已处理，直接忽略

原因：避免后到包改变已模拟帧。

### 12.5 丢包和缺帧

虽然 KCP 提供可靠传输，但实时业务仍要考虑延迟和队列拥塞。服务端缺少某 tick 输入时：

- 移动可沿用最近一次有效输入，最多沿用一个很短窗口
- Fire 不沿用
- 长时间缺输入则使用空输入，让玩家停止

可配置：

```go
MaxInputHoldTicks int64
```

第一版可先不加配置，简单沿用移动、禁用 Fire 复用；联调再细化。

### 12.6 物理错误

如果 `PhysicsWorld.MovePlayer` 返回错误：

- 不更新 Go player 坐标
- 保存当前状态为权威历史
- 限频记录日志
- 给该玩家发 correction，让客户端回到服务端当前状态

不能因为物理错误使用客户端预测位置兜底，否则会破坏权威。

## 13. 性能与带宽考虑

### 13.1 输入带宽

默认 20 tick/s，如果每 tick 一帧输入：

- 单帧包含 tick、move、view、fire，JSON 会偏大
- batch 后每秒可发送 10 次，每次带 2 tick，接近当前 snapshot 节奏
- 后续如果改 protobuf，带宽会明显下降

首版仍用 JSON 是为了贴合当前协议和调试便利；稳定后再考虑 protobuf 或二进制压缩。

### 13.2 历史内存

两人房，20 tick/s，3 秒窗口：

```text
2 players * 60 ticks = 120 player states
```

这个内存量很小。即使每个状态几十到上百字节，也不是问题。

真正需要注意的是不要使用无界 map 保存所有历史，因此必须有窗口清理或 ring buffer。

### 13.3 物理开销

当前每 tick 每玩家一次 `MovePlayer`。两人房成本很低。

如果后续做服务端回滚，最坏会出现：

```text
每次回滚重放 rollback_window_ticks * player_count 次物理移动
```

所以第一版不默认做服务端重放，避免把性能风险提前引入。

### 13.4 cgo 开销

当前移动每玩家每 tick 一次 cgo 调用，两人房可接受。后续如果人数增加或引入大量查询，建议：

- 批量移动接口
- 批量 raycast 已有雏形
- 避免每帧多次 get/set 玩家状态
- 只在 correction 或测试时读写物理状态

## 14. 配置设计

`src/roomserver/config/config.go` 增加：

```go
PredictionEnabled bool
RollbackWindowTicks int64
FutureInputWindowTicks int64
PredictionKeyframeInterval int64
PositionTolerance float64
HardPositionTolerance float64
AngleTolerance float64
MaxInputBatchFrames int
MaxInputHoldTicks int64
```

`config/config.yaml` 对应增加：

```yaml
prediction_enabled: true
rollback_window_ticks: 60
future_input_window_ticks: 8
prediction_keyframe_interval: 2
position_tolerance: 0.15
hard_position_tolerance: 0.5
angle_tolerance: 2.0
max_input_batch_frames: 8
max_input_hold_ticks: 3
```

为什么配置化：

- 不同 tick_rate 下窗口需要调整
- 地图尺度和移动速度会影响合理误差阈值
- 联调初期可能需要放宽阈值观察误差分布
- 上线后可以灰度开关预测模式

## 15. 影响范围

预计修改：

- `src/roomserver/protocol/message.go`
  - 新增消息类型、batch 输入、ack、correction、预测状态结构
  - 扩展 JoinRoomAck/Heartbeat/Snapshot 可选字段

- `pb/room/room.proto`
  - 追加对应 message 和字段，按项目规范补简短注释

- `src/roomserver/config/config.go`
  - 增加同步配置及默认值归一化

- `config/config.yaml`
  - 增加同步配置示例

- `src/roomserver/service/server.go`
  - 新增 `MsgPlayerInputBatch` 分支
  - 心跳响应带 `server_tick`
  - service 只解包和投递

- `src/roomserver/logic/room_manager.go`
  - 新增 `PushInputBatch`
  - 新增查询玩家所在房间 tick 的方法

- `src/roomserver/logic/room.go`
  - 增加玩家同步状态
  - 输入按 tick 入缓冲
  - tick 中按帧选择输入
  - 保存权威历史
  - 校验预测状态并发送 correction/ack

- `src/roomserver/logic/movement.go`
  - 抽出单 tick 模拟和误差计算辅助函数
  - 增加角度差计算

- `src/roomserver/logic/player.go`
  - 增加内部帧状态转换函数，或保持现有 `ToState` 并新增非协议状态结构

- `src/roomserver/logic/physics.go`
  - 扩展物理接口，支持状态读写和 dt
  - simple 后端同步实现

- `src/roomserver/physx/world.go`
  - 实现状态读写和 dt 传递

- `src/roomserver/physx/physx_bridge.h` / `physx_bridge.cc`
  - 增加玩家位置 get/set C ABI
  - `px_world_move_player` 接收 dt 或由 Go 侧传入

- `src/roomserver/README.md` / `PHYSX_FLOW.md`
  - 更新同步流程、协议、客户端要求、验证步骤

- 测试
  - 新增输入帧缓冲、纠偏、ack、阈值、PhysX 状态读写等测试

## 16. 实施阶段

### 阶段一：协议与配置骨架

目标：先让服务端能识别预测同步能力，但不改变核心移动行为。

内容：

1. 新增协议结构和消息类型
2. 扩展 JoinRoomAck/HeartbeatAck
3. 增加同步配置默认值
4. `service` 增加 batch 消息分发
5. `RoomManager` 增加投递 batch 的方法
6. 文档补充协议说明

验证：

- `go test ./src/roomserver/protocol ./src/roomserver/config`
- `make proto`

### 阶段二：输入帧缓冲与权威历史

目标：让服务端真正按 tick 使用输入，而不是只用最近输入。

内容：

1. `Room` 增加 `playerSyncState`
2. `handleInput/handleInputBatch` 写入输入帧缓冲
3. `updatePlayers` 按当前 tick 取输入
4. 保存最近 N tick 权威状态
5. 实现 input ack
6. 保留旧 `MsgPlayerInput` 兼容路径

验证：

- 同 tick 重复输入不会覆盖已处理帧
- 太旧/太未来输入被丢弃
- 缺帧时移动和 Fire 行为符合预期
- 旧单帧输入仍能移动

### 阶段三：预测关键帧校验与 correction

目标：服务端能发现客户端预测偏离并发纠偏消息。

内容：

1. 保存客户端 `PredictedPlayerState`
2. 每 tick 或关键帧间隔执行误差校验
3. 超阈值生成 `StateCorrection`
4. correction 使用权威历史中的指定帧
5. 增加 correction 限频
6. Snapshot 或独立消息携带 input ack

验证：

- 小误差不纠偏
- 大误差纠偏
- correction 的 rollback_tick 和 state 正确
- 连续错误不会无限刷 correction

### 阶段四：物理 dt 和状态读写

目标：补齐预测一致性和后续回滚的物理基础。

内容：

1. `MovePlayerRequest` 增加 dt 或 tickRate 相关字段
2. simple 后端兼容实现
3. PhysX C ABI 增加 get/set player position
4. PhysX move 使用房间 tick dt，不再固定 `1/60`
5. 增加 PhysX 状态读写测试

验证：

- `go test ./src/roomserver/logic`
- `go test -tags physx ./src/roomserver/physx`
- 位置 set 后再 move 能得到预期结果

### 阶段五：联调和参数校准

目标：通过客户端或测试工具验证完整闭环。

内容：

1. 客户端按 JoinRoomAck 配置启动预测
2. 客户端批量发送输入和预测状态
3. 服务端统计误差分布和 correction 次数
4. 调整阈值、关键帧间隔、输入提前窗口
5. 观察弱网、乱序、延迟下的行为

验证场景：

- 正常直线移动不频繁 correction
- 客户端故意加速会被拉回
- 客户端碰撞数据不一致会被持续 correction，并能定位 physics_hash 问题
- 丢包或延迟抖动时不会重复开火
- 高输入发送频率不会获得额外速度

## 17. 测试计划

新增单元测试建议：

- `TestInputBatchRejectsTooManyFrames`
- `TestInputFrameRejectsTooOldTick`
- `TestInputFrameRejectsFutureTick`
- `TestDuplicateInputTickDoesNotOverrideProcessedInput`
- `TestRoomAppliesInputForMatchingTick`
- `TestRoomHoldsMoveInputButNotFire`
- `TestAuthoritativeHistoryKeepsRollbackWindow`
- `TestPredictionWithinToleranceDoesNotCorrect`
- `TestPredictionBeyondToleranceSendsCorrection`
- `TestCorrectionUsesHistoricalAuthoritativeState`
- `TestHeartbeatIncludesRoomTick`
- `TestLegacyPlayerInputStillWorks`
- `TestSimplePhysicsSetGetPlayerPosition`

PhysX build tag 测试：

- `TestWorldSetGetPlayerPosition`
- `TestWorldMoveUsesProvidedDeltaTime`
- `TestWorldSetPositionThenRaycast`

验证命令：

```bash
gofmt -w src/roomserver/protocol/message.go src/roomserver/config/config.go src/roomserver/service/server.go src/roomserver/logic/*.go src/roomserver/physx/*.go
make proto
go test ./src/roomserver/...
go test -tags physx ./src/roomserver/physx ./src/roomserver/logic
scripts/build_all.sh
```

如果本机 PhysX SDK 或 `protoc` 环境缺失，需要准确报告失败原因，不把未执行的验证说成通过。

## 18. 兼容性与迁移策略

1. 默认可以先让 `PredictionEnabled` 配置为 true，但只有客户端声明支持预测时才走新协议。
2. 老客户端继续 `MsgPlayerInput` + `MsgSnapshot`。
3. 新客户端入房时声明 `sync_version=1` 和 `prediction_enabled=true`。
4. 服务端 ack 中返回实际启用的 `sync_mode`。
5. 如果客户端 physics hash 不一致，服务端返回 `sync_mode=snapshot_only` 或错误，避免持续纠偏。
6. proto 字段只追加，不删除、不复用编号；如删除字段必须 reserved。

## 19. 风险与应对

### 风险一：客户端与服务端物理不一致导致频繁 correction

应对：

- 下发 physics_hash，先排除资源不一致
- 阈值配置化
- 统计误差分布再调参数
- 长期方案考虑共享角色控制器

### 风险二：服务端回滚需求被低估

应对：

- 第一版明确只做客户端回滚
- 服务端保存历史但不重写历史
- 后续如做命中延迟补偿，再设计完整房间快照和事件重放

### 风险三：协议一次性变化太大

应对：

- 新增消息，不破坏旧消息
- 分阶段提交
- 每阶段都有可运行测试

### 风险四：JSON 带宽偏大

应对：

- batch 输入减少消息数
- 关键帧状态间隔上报
- 后续再迁移 protobuf，不把编码优化和同步逻辑混在第一版

### 风险五：Fire 缺帧复用导致射击异常

应对：

- 移动和 Fire 分开处理
- Fire 只消费对应 tick 的输入帧
- 后续接武器系统时按服务端射速和弹药做二次约束

## 20. 自我审查

1. 是否符合现有项目结构：符合。service 只解析投递，logic 做同步规则，physx 只做物理能力。
2. 是否过度设计：已把服务端完整回滚放到后续阶段，第一版聚焦客户端预测纠偏。
3. 是否存在协议兼容风险：通过新增消息和能力协商降低风险，proto 只追加字段。
4. 是否存在错误处理不足：已覆盖非法输入、旧输入、未来输入、重复输入、缺帧、物理错误、hash 不一致。
5. 是否存在性能风险：第一版只增加有限 ring buffer 和误差计算；不默认做服务端重放，避免高额物理重算。
6. 是否存在物理一致性误判：已明确不追求 bit-level 一致，采用阈值和权威纠偏。
7. 是否方便未来扩展：权威历史、输入缓冲、物理 set/get 是后续延迟补偿、服务端回滚、命中验证的基础。

## 21. 最终推荐方案

最终建议按以下顺序实施：

1. **协议与配置**：新增 batch input、ack、correction、同步配置和 Join/Heartbeat 扩展。
2. **输入帧缓冲**：`Room` 内按玩家保存输入帧，tick 中按帧消费输入。
3. **权威历史**：保存最近 `rollback_window_ticks` 的玩家权威状态。
4. **预测校验**：对客户端关键帧预测状态做位置/角度误差比较。
5. **客户端纠偏**：服务端超阈值发送 `StateCorrection`，客户端回滚重放。
6. **物理基础补齐**：修正 PhysX dt，增加玩家状态 get/set，为后续服务端回滚打基础。
7. **联调调参**：通过真实客户端或测试工具观察误差、带宽和 correction 频率。

等待用户确认后，再开始改业务代码。
