# 阶段四：服务端权威移动、客户端预测和纠偏

本阶段目标：看懂 roomserver 为什么只信输入、不信客户端坐标，以及当前预测纠偏链路是如何落到代码上的。

## 1. 总体思路

当前设计是：

```text
客户端：采集输入、本地预测、上报输入和预测状态
服务端：接收输入、校验输入、按 tick 模拟、保存权威历史、校验预测误差、必要时纠偏
```

服务端不会把客户端上报的 `x/y/z` 当成最终坐标。客户端预测坐标只用于比较误差。

关键代码：

- 输入清洗：[../logic/movement.go](../logic/movement.go)
- 同步状态：[../logic/sync.go](../logic/sync.go)
- 房间更新和纠偏：[../logic/room.go](../logic/room.go)
- 协议字段：[../protocol/message.go](../protocol/message.go)

## 2. 同步模式

同步模式定义在 [../logic/sync.go](../logic/sync.go)。

| 常量 | 字符串 | 含义 |
| --- | --- | --- |
| `SyncModeSnapshotOnly` | `snapshot_only` | 只接收服务端快照，不启用客户端预测纠偏 |
| `SyncModePredictionAuthoritative` | `prediction_authoritative` | 客户端可以预测，服务端权威校验并纠偏 |

玩家实际模式由 [../logic/room.go](../logic/room.go) `playerSyncMode` 判断：

```text
服务端 PredictionEnabled 必须为 true
客户端 PredictionEnabled 必须为 true
客户端 SyncVersion 必须 > 0
如果服务端 physicsHash 非空，客户端 PhysicsHash 必须一致
以上都满足才使用 prediction_authoritative
否则降级 snapshot_only
```

这就是为什么 JoinRoomAck 里会返回 `sync_mode`。客户端不能只看自己请求了预测模式，还要以服务端返回为准。

## 3. SyncConfig 字段说明

结构在 [../logic/sync.go](../logic/sync.go)。配置来源是 [../config/config.go](../config/config.go)，在 [../service/server.go](../service/server.go) `Start` 中组装后传给 RoomManager。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `PredictionEnabled` | `bool` | 服务端是否启用预测同步能力 |
| `RollbackWindowTicks` | `int64` | 服务端保存多少帧权威历史、输入历史和预测历史 |
| `FutureInputWindowTicks` | `int64` | 客户端输入允许领先服务端多少帧 |
| `PredictionKeyframeInterval` | `int64` | 每隔多少服务端帧校验一次客户端预测状态 |
| `PositionTolerance` | `float64` | 位置误差小于等于该值时认为预测可接受 |
| `HardPositionTolerance` | `float64` | 位置误差超过该值时强制纠偏，不受最小间隔限制 |
| `AngleTolerance` | `float64` | 视角误差小于等于该值时认为预测可接受 |
| `MaxInputBatchFrames` | `int` | 单个输入包最多接受多少帧 |
| `MaxInputHoldTicks` | `int64` | 缺少当前帧输入时，上一帧输入最多沿用多少帧 |
| `CorrectionMinIntervalTicks` | `int64` | 普通纠偏最小间隔，避免抖动和消息过多 |

`Normalize` 会按 tickRate 补默认值。比如 `RollbackWindowTicks` 默认是 `tickRate * 3`，当前 20 tick 下就是 60 帧，约 3 秒。

## 4. playerSyncState 字段说明

结构在 [../logic/sync.go](../logic/sync.go)。这是每个玩家的同步运行时状态。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `inputs` | `map[int64]authoritativeInput` | 服务端已接受但还未应用或仍在窗口内的输入，key 是客户端 tick |
| `predictedStates` | `map[int64]protocol.PredictedPlayerState` | 客户端上报的预测状态，key 是客户端 tick |
| `authoritativeHistory` | `map[int64]playerFrameState` | 服务端保存的权威帧历史，key 是服务端 tick |
| `lastInput` | `authoritativeInput` | 最近一次应用过的输入，用于短时间缺帧沿用 |
| `hasLastInput` | `bool` | 是否有可沿用的上一帧输入 |
| `lastInputTick` | `int64` | `lastInput` 对应的 tick |
| `lastAppliedTick` | `int64` | 服务端已经应用到的 tick |
| `lastAcceptedInputTick` | `int64` | 服务端最后接受的客户端输入 tick，会通过 InputAck 返回 |
| `lastVerifiedTick` | `int64` | 服务端最后校验预测状态的 tick，会通过 InputAck 返回 |
| `lastCorrectionTick` | `int64` | 上一次发送纠偏的服务端 tick |

这个结构只在房间 loop 内使用，所以不需要单独加锁。

## 5. authoritativeInput 字段说明

结构在 [../logic/movement.go](../logic/movement.go)。它是服务端清洗后的输入。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `ClientTick` | `int64` | 客户端输入帧号 |
| `MoveX` | `float64` | 清洗后的左右输入，范围 `[-1, 1]`，斜向会归一化 |
| `MoveZ` | `float64` | 清洗后的前后输入，范围 `[-1, 1]`，斜向会归一化 |
| `Yaw` | `float64` | 归一化后的水平视角，范围 `[-180, 180]` |
| `Pitch` | `float64` | 限制后的垂直视角，范围 `[-89, 89]` |
| `Fire` | `bool` | 当前帧是否请求开火 |

`sanitizePlayerInput` 会拒绝 NaN 和 Inf，防止异常浮点数污染服务端物理计算。

## 6. 输入接收流程

新版客户端发送 `MsgPlayerInputBatch`，服务端入口是 [../service/server.go](../service/server.go) `handlePlayerInputBatch`。

```text
检查 session.PlayerID != 0
  -> DecodeJSON[PlayerInputBatch]
  -> 检查 frames 数量：1 到 MaxInputBatchFrames
  -> manager.PushInputBatch
  -> room.PushInputBatch
  -> Room.loop 中 handleInputBatch
```

旧版 `MsgPlayerInput` 会在 [../logic/room.go](../logic/room.go) `handleInput` 里被包装成只有一帧的 batch。

## 7. handleInputBatch 做什么

函数在 [../logic/room.go](../logic/room.go)。核心逻辑：

```text
检查玩家存在且存活
检查 batch 非空且不超长
遍历 frames：
  -> inputTick 取 frame.ClientTick，空则用 batch.BaseClientTick
  -> 太旧：发送当前权威纠偏
  -> 太未来：丢弃
  -> 已应用：丢弃
  -> 重复 tick：丢弃
  -> sanitizePlayerInput
  -> 写入 syncState.inputs[inputTick]
  -> 更新 lastAcceptedInputTick
  -> 如果 predicted_state 有效，写入 syncState.predictedStates[inputTick]
```

输入窗口判断：

```text
太旧：inputTick < r.tick - RollbackWindowTicks
太未来：inputTick > r.tick + FutureInputWindowTicks
```

这能限制客户端通过很旧或很未来的输入扰乱服务端模拟。

## 8. updatePlayers 如何推进权威状态

每个服务端 tick，`Room.update` 调用 [../logic/room.go](../logic/room.go) `updatePlayers`。

```text
遍历 r.players
  -> ensureSyncState
  -> inputForTick(syncState, r.tick)
  -> 有当前帧输入，或允许沿用上一帧输入时，simulatePlayerTick
  -> syncState.lastAppliedTick = r.tick
  -> saveAuthoritativeState
  -> verifyPredictedState
  -> cleanupSyncState
```

这里有两个重点：

- 服务端按自己的 `r.tick` 推进，不由客户端 tick 驱动房间时间。
- 权威状态每帧保存一份，用于 correction 的 `rollback_tick`。

## 9. inputForTick 的缺帧处理

函数在 [../logic/room.go](../logic/room.go)。

逻辑：

```text
如果 syncState.inputs[tick] 存在：
  -> 使用精确输入
  -> 记录 lastInput / lastInputTick
否则如果有 lastInput 且 tick - lastInputTick <= MaxInputHoldTicks：
  -> 沿用 lastInput，但 ClientTick 改成当前 tick，Fire 强制 false
否则：
  -> 当前 tick 无输入
```

为什么 `Fire` 沿用时强制 false：避免网络缺帧导致一次开火输入被服务端重复当成多次开火。

## 10. simulatePlayerTick 如何移动玩家

函数在 [../logic/room.go](../logic/room.go)。

```text
applyViewRotation
  -> buildMovePlayerRequest
  -> physics.MovePlayer
  -> 把物理结果写回 player.X/Y/Z
  -> 如果精确输入里 Fire=true，执行 Raycast
```

`buildMovePlayerRequest` 在 [../logic/movement.go](../logic/movement.go)：

```text
movementDirection(input.Yaw, input.MoveX, input.MoveZ)
  -> 根据 yaw 把本地输入转换成世界方向
Distance = defaultPlayerMoveSpeed * (1 / tickRate)
DeltaTime = 1 / tickRate
```

当前默认移动速度是 `4.0` 单位/秒，20 tick 下每 tick 最大移动距离是 `0.2`。

## 11. movementDirection 坐标关系

函数在 [../logic/movement.go](../logic/movement.go)。

```text
yaw = 0：forward 是 +Z，right 是 +X
yaw = 90：forward 是 +X，right 是 -Z
```

计算公式：

```text
worldMove = forward * moveZ + right * moveX
```

这意味着客户端只需要发送“本地输入方向”和视角，服务端自己换算成世界移动方向。

## 12. playerFrameState 字段说明

结构在 [../logic/sync.go](../logic/sync.go)。这是服务端保存的权威历史帧。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `Tick` | `int64` | 服务端帧号 |
| `PlayerID` | `uint64` | 玩家 ID |
| `Position` | `Vector3` | 服务端权威位置 |
| `Yaw` | `float64` | 服务端认可水平视角 |
| `Pitch` | `float64` | 服务端认可垂直视角 |
| `HP` | `int` | 生命值 |
| `Alive` | `bool` | 是否存活 |
| `SpawnID` | `string` | 出生点 ID |

`frameStateFromPlayer` 从当前 Player 构造权威历史，`toPlayerState` 把历史帧转成 correction 或 snapshot 用的协议状态。

## 13. 预测状态校验

函数是 [../logic/room.go](../logic/room.go) `verifyPredictedState`。

校验条件：

```text
服务端启用预测
玩家 SyncMode == prediction_authoritative
当前 tick 有客户端 predictedState
当前 tick 满足 PredictionKeyframeInterval
当前 tick 有服务端 authoritativeHistory
```

误差计算：

- `positionError`：客户端预测位置和服务端权威位置的三维距离
- `angleError`：yaw 误差和 pitch 误差取较大值

纠偏判断：

```text
posError <= PositionTolerance 且 angleError <= AngleTolerance：不纠偏
否则：准备纠偏
如果 posError > HardPositionTolerance：强制纠偏
否则要满足 tick - lastCorrectionTick >= CorrectionMinIntervalTicks
```

## 14. StateCorrection 发送内容

函数是 [../logic/room.go](../logic/room.go) `sendCorrection`。

服务端发送：

```text
MsgStateCorrection
  player_id
  rollback_tick
  server_tick
  last_accepted_input_tick
  state: 权威 PlayerState
  reason
  position_error
  angle_error
```

客户端处理方式应该是：

```text
把本地状态回滚到 correction.state
  -> 从 rollback_tick + 1 开始重放本地输入
  -> 一直模拟到当前本地帧
```

详细客户端接入说明见 [../CLIENT_PREDICTION_ROLLBACK.md](../CLIENT_PREDICTION_ROLLBACK.md)。

## 15. InputAck 发送内容

函数是 [../logic/room.go](../logic/room.go) `broadcastAcks`。

只发给 `prediction_authoritative` 模式玩家：

```text
server_tick
last_accepted_input_tick
last_verified_input_tick
```

客户端可以用它清理太旧的输入和预测历史，但要保留回滚窗口内的数据。

## 16. cleanupSyncState 清理什么

函数在 [../logic/room.go](../logic/room.go)。

```text
minTick := r.tick - RollbackWindowTicks
删除过旧 inputs
删除过旧 predictedStates
删除过旧 authoritativeHistory
```

清理的目的是防止每个玩家的历史 map 无界增长。

## 17. 为什么这套设计能防很多移动外挂

因为服务端不接受“我现在在这里”作为真相。客户端能影响服务端的主要是：

```text
move_x / move_z / yaw / pitch / fire / client_tick
```

服务端会做：

- 输入范围限制
- 斜向归一化
- 视角范围限制
- 输入窗口限制
- 固定 tick 推进
- 物理后端碰撞计算
- 预测误差检测和纠偏

如果客户端伪造坐标，只会体现在 `predicted_state` 里，最多导致服务端发现误差并下发 correction，不会直接改写 `player.X/Y/Z`。
