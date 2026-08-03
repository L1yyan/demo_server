# 弱网下本机不拉回和位置不一致修复方案

## 需求理解

当前现象：弱网环境下双方都能看到远端玩家，但被观测玩家自己客户端的位置和远端看到的服务端权威位置会不一致，同时本机没有收到或没有执行纠错拉回。

我检查了服务端和你给的 Unity 客户端路径 `/mnt/c/Users/liyan1/codes/democlient/demo_client` 后，判断当前主要问题在服务端 correction 触发链路：

- 客户端 `RoomClient` 已经能解码 `MsgStateCorrection = 10`。
- 客户端 `RoomSessionBehaviour.ApplyStateCorrection` 已经有覆盖权威状态、回滚重放、更新本地模拟层的逻辑。
- 客户端预测模式下每帧都会把 `predicted_state` 填到 `PlayerInputFrameMessage`。
- 但服务端当前 `Room.handleInputBatch` 对轻微迟到输入只重排了输入本身，没有把同一帧的 `predicted_state` 一起挂到重排后的服务端执行 tick。
- 因此服务端能继续推进权威位置，远端客户端能看到被观测玩家移动；但 `verifyPredictedState` 在目标 tick 找不到预测状态，无法计算误差并下发 `StateCorrection`，被观测玩家本机就不会被拉回。

另外当前服务端单测 `TestLateInputRescheduleDoesNotCorrect` 的期望已经和需求相反：它传入明显错误的预测位置，却期望不纠偏。这个测试需要改成迟到输入重排后也必须能触发纠偏。

## 影响范围

预计修改服务端：

- `src/roomserver/logic/room.go`
  - 在迟到输入候选中同时保存合法的 `PredictedState`。
  - 输入重排到 `targetTick` 后，把该预测状态也写入 `syncState.predictedStates[targetTick]`。
  - 视需要补充一条低频诊断日志，确认 correction 发送原因、rollback tick、误差值和 last accepted tick。

- `src/roomserver/logic/sync_test.go`
  - 替换当前迟到输入不纠偏测试。
  - 新增或调整测试，验证迟到输入重排后，如果预测状态与服务端权威状态超过阈值，会发送 `MsgStateCorrection`。
  - 保留弱网迟到输入仍能驱动服务端移动和视角更新的测试。

暂不直接修改客户端：

- 当前客户端 correction 接收和回放路径已经存在，先让服务端实际发出 correction。
- 如果修复后 Unity 日志能看到 `Prediction correction replayed`，说明客户端链路生效。
- 如果服务端确认发了 correction 但 Unity 仍不拉回，再进入客户端侧修复，重点检查 `ReplayFromCorrection` 输入历史缺帧、`ShouldAcceptCorrection` 过期过滤和本地模拟层 SetSimulationState 是否被后续输入覆盖。

## 设计方案

### 1. 迟到输入候选保存预测状态

当前逻辑只保存：

```go
var latestLateInput authoritativeInput
hasLateInput := false
```

修正为同时保存：

```go
var latestLateInput authoritativeInput
var latestLatePredictedState protocol.PredictedPlayerState
hasLateInput := false
hasLatePredictedState := false
```

遍历 batch 时，如果 `inputTick <= lastAppliedTick` 且仍在 `MaxInputHoldTicks` 容错窗口内，则选择 batch 中最新的迟到帧作为重排候选，同时把该帧合法的 `PredictedState` 记录下来。

### 2. 重排后写入对应 targetTick 的 predicted state

当前重排只做：

```go
latestLateInput.ClientTick = targetTick
r.acceptInput(syncState, targetTick, latestLateInput)
```

修正后追加：

```go
if hasLatePredictedState {
    syncState.predictedStates[targetTick] = latestLatePredictedState
}
```

这样 `updatePlayers -> verifyPredictedState` 在服务端执行 `targetTick` 后，可以拿到同 tick 的权威状态和客户端预测状态，误差超阈值时下发 `StateCorrection`。

### 3. 纠偏诊断

可在 `sendCorrection` 成功后输出低频或直接 info 级诊断，包含：

- `room_id`
- `player_id`
- `reason`
- `rollback_tick`
- `server_tick`
- `last_accepted_input_tick`
- `position_error`
- `angle_error`

这样弱网联调时可以直接区分：

- 服务端没发 correction：继续查服务端预测状态/阈值/节流。
- 服务端已发 correction：继续查 Unity 客户端应用和回放。

## 兼容性

- 不修改协议字段和消息 ID。
- 不影响 snapshot-only 客户端。
- 正常未来输入仍按原 tick 处理。
- 只改变轻微迟到输入重排后的 correction 触发行为，符合 prediction-authoritative 模式预期。

## 健壮性

- `PredictedState == nil` 时只重排输入，不写预测状态。
- `PredictedState` 包含 NaN/Inf 时不参与纠偏校验。
- 重排 target tick 仍必须在 future input window 内，且不覆盖已有输入。
- 太旧输入仍按 stale input 处理，不做历史回滚。
- correction 发送失败仍保留现有 warn 日志。

## 性能考虑

- 每个 batch 最大 8 帧，新增保存一个预测状态的开销很小。
- 不引入服务端历史回滚重算，不增加 goroutine 或锁。
- 诊断日志只在 correction 发生时输出，频率受现有 `CorrectionMinIntervalTicks` 约束。

## 验证方式

1. 运行 roomserver logic 单测：

```bash
go test ./src/roomserver/logic
```

2. 运行 roomserver service 单测，确认 snapshot 队列改动未受影响：

```bash
go test ./src/roomserver/service
```

3. 编译全部服务：

```bash
./scripts/build_all.sh
```

4. 重启服务并确认端口：

- matchserver `:8090`
- roomserver UDP `:9001`
- logicserver `:8080`

5. 弱网联调：`lag 100ms drop 3%` 下观察：

- roomserver 日志是否出现 `state correction sent` 或对应 correction 诊断。
- Unity 日志是否出现 `Prediction correction replayed` 或 fallback resync 日志。
- 被观测玩家本机位置是否会向服务端权威位置拉回。

## 自我审查

- 已核对服务端结构：输入链路是 `handleInputBatch -> updatePlayers -> verifyPredictedState -> sendCorrection`，问题集中在迟到输入重排后缺少同 tick predicted state。
- 已核对客户端结构：客户端已有 `StateCorrection` decode 和 replay，不应先盲目改客户端。
- 没有引入新协议或新依赖。
- 当前方案不是完整服务端历史回滚，只是补齐现有“迟到输入重排”策略的纠偏触发闭环，改动范围小。
- 风险点：迟到帧的客户端预测 tick 和服务端重排 tick 并非严格同一时间线，但当前服务端已经采用重排策略推进权威状态，让 correction 对齐实际执行 tick 是现阶段最一致的处理方式。

## 最终方案

先修服务端：迟到输入重排时同步重排合法 `predicted_state`，并把当前错误测试改成“迟到输入重排后预测误差超阈值必须触发 correction”。验证通过后编译重启服务。若服务端日志显示 correction 已发但客户端仍不拉回，再继续进入 Unity 客户端侧修复。

等待确认后实施。
