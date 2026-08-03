# 关闭弱网后仍持续纠错修复方案

## 需求理解

当前现象：弱网期间远端不再静止，客户端也能被拉回；但关闭弱网后仍持续出现纠错。

结合服务端日志和代码，当前持续纠错更像是 tick 语义被迟到输入重排打乱，而不是网络关闭后还在丢包：

- 服务端把弱网迟到输入从原始 `client_tick` 重排到未来 `target_tick` 执行。
- 当前实现同时把这帧原始 `client_tick` 的 `predicted_state` 绑定到 `target_tick`，并强制校验。
- 但该 `predicted_state` 是客户端在原始 tick 的预测结果，不是 target tick 的预测结果。
- 服务端用 target tick 的权威状态和旧 client tick 的预测状态比较，会制造非真实误差。
- correction 的 `last_accepted_input_tick` 也被推进到 target tick，客户端会把它当作原始 client tick ack，可能过早丢弃输入历史，导致 replay 后继续错位。

这会造成弱网恢复后仍持续 `position_error` 纠错。

## 影响范围

预计修改：

- `src/roomserver/logic/sync.go`
  - 增加区分“服务端执行 tick”和“客户端原始输入 tick”的状态字段。
  - 移除或替换 `forceVerifyPredictedTicks` 的错误用途。

- `src/roomserver/logic/room.go`
  - 重排迟到输入时，保留原始 `client_tick` 作为 ack 语义。
  - 输入仍写入服务端 `target_tick` 执行，但 `last_accepted_input_tick` 不再推进到 target tick，而是推进到原始 client tick。
  - 不再把原始 tick 的 `predicted_state` 直接挂到 target tick 做 position_error 校验。
  - 如果需要拉回，只在重排输入执行后发送“当前权威状态纠偏”，不做错误的 predicted_state 对比。

- `src/roomserver/logic/sync_test.go`
  - 增加 ack 语义测试：迟到输入从 client tick 1 重排到 server tick 9 后，ack 仍应确认 client tick 1，而不是 9。
  - 增加测试：重排输入携带的旧 predicted_state 不应按 target tick 触发 `position_error` 校验。
  - 保留迟到输入仍能推进服务端权威状态的测试。

可能修改：

- `src/roomserver/logic/sync.go`
  - 新增 correction reason，例如 `late_input_reschedule`，用于区分“当前权威状态重同步”和真实预测误差。

不修改：

- 协议字段和消息 ID。
- Unity 客户端代码。

## 设计方案

### 1. 拆分两个 tick 语义

当前 `acceptInput(syncState, inputTick, input)` 同时承担：

- 服务端把输入放到哪个 tick 执行。
- 客户端应该 ack 到哪个 input tick。

这在正常输入下没问题，因为 `server execution tick == client input tick`。但迟到重排后不成立。

调整为：

```go
acceptInput(syncState, executeTick, acceptedClientTick, input)
```

- `executeTick`：服务端权威时间线中实际执行输入的 tick。
- `acceptedClientTick`：客户端原始输入 tick，用于 `last_accepted_input_tick` ack 和客户端清理历史。

正常输入：

```text
executeTick = inputTick
acceptedClientTick = inputTick
```

迟到重排：

```text
executeTick = targetTick
acceptedClientTick = originalLateTick
```

### 2. 迟到重排不再强制 predicted_state 对比

迟到重排时，`PredictedState` 属于原始 `client_tick`，不能直接放到 `targetTick`：

```go
syncState.predictedStates[targetTick] = latestLatePredictedState // 删除这个行为
syncState.forceVerifyPredictedTicks[targetTick] = true            // 删除这个行为
```

否则服务端会用不同时间线的状态做比较，持续产生假 correction。

### 3. 需要拉回时发送当前权威状态纠偏

如果迟到重排确实导致本机预测和服务端权威分叉，服务端可以在执行该重排输入后，用当前权威状态发送 correction，但 reason 不应是 `position_error`：

```text
reason = late_input_reschedule
rollback_tick = 当前服务端 tick
state = 当前服务端权威状态
last_accepted_input_tick = 原始 client tick ack
position_error = 0
angle_error = 0
```

这类 correction 是“服务端时间线被重排后的权威重同步”，不是预测状态误差对比。它可以拉回客户端，但不会因为错误的 predicted_state 对比一直触发。

为了避免弱网期间 spam：

- 复用 `CorrectionMinIntervalTicks` 做最小间隔。
- 若同一玩家刚发过 correction，则跳过当前重排 correction。

### 4. 正常输入恢复后再用真实预测误差校验

弱网关闭后，客户端输入重新以未来 tick 正常到达服务端。此时：

- 正常输入的 `predicted_state` 仍按同 tick 存储。
- `verifyPredictedState` 继续做真实的 `position_error` / `angle_error` 校验。
- 因为 ack 不再错误跳到 target tick，客户端不会过早删掉输入历史，replay 更容易收敛。

## 兼容性

- 协议不变。
- 客户端仍按原 `last_accepted_input_tick` 清理 input history，但该字段语义会恢复为“客户端原始输入 tick”。
- 正常网络路径行为不变。
- 只改变迟到输入重排后的 ack 和 correction 触发方式。

## 健壮性

- rollback window 外输入仍 stale correction。
- future window 外输入仍丢弃。
- target tick 占用时仍向后找空位。
- 找不到 target tick 时丢弃并记录诊断日志。
- 迟到重排不再基于不匹配的 predicted_state 触发 position_error。
- 当前权威重同步 correction 做间隔限制，避免弱网期间每 tick 都发。

## 性能考虑

- 只是多传一个 `acceptedClientTick` 参数，不增加复杂度。
- 不做服务端历史回滚重算。
- correction 数量应下降，因为去掉了错误的 target tick 强制校验。

## 验证方式

1. 新增/调整单测后运行：

```bash
go test ./src/roomserver/logic
```

2. 运行：

```bash
go test ./src/roomserver/service
go test ./src/roomserver/config
```

3. 编译并重启：

```bash
./scripts/build_all.sh
```

4. 弱网联调：

- 开启 `lag 50ms drop 3%`：远端玩家不应静止。
- 关闭弱网后：`position_error` correction 应快速下降并停止持续刷。
- roomserver 日志可区分：
  - `late_input_reschedule`：弱网期间权威重同步。
  - `position_error`：正常同 tick 预测误差。

## 自我审查

- 当前持续 correction 的根因不应继续靠扩大容错窗口解决，窗口越大，错误 tick 比较越明显。
- 直接改客户端忽略 correction 会掩盖服务端 tick 语义错误，不合适。
- 完整服务端历史回滚更严格，但当前项目还没实现回滚重算，先修正 ack 和 correction 语义更小、更稳。
- 需要注意拉回能力不能丢，所以保留“重排后当前权威状态 correction”，但不再伪装成 predicted_state position_error。

## 最终方案

修正服务端迟到输入重排的 tick 语义：服务端执行 tick 和客户端 ack tick 分离；迟到输入不再把原始 predicted_state 绑定到 target tick 强制校验；必要时发送带 `late_input_reschedule` reason 的当前权威状态 correction。这样弱网期间仍能救远端静止，关闭弱网后不会继续因为错误 tick 对比持续纠错。

等待确认后实施。
