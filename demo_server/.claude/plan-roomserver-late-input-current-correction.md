# Roomserver 弱网重排输入后权威拉回修复方案

## 需求理解

当前弱网下还有一种分叉：

- 本机客户端看到自己按本地预测走得更靠前。
- 远端玩家看到的是服务端 snapshot 里的权威位置。
- 两边位置不一致，但 correction 不一定触发。

原因是上一轮修复后，迟到输入会被服务端重排到后续 tick 执行，但不会再把原始 `predicted_state` 绑到重排 tick 上。这避免了错帧误纠偏，但也带来一个空档：

```text
迟到输入被重排 -> 服务端权威能继续移动 -> 但没有同 tick predicted_state 可校验 -> 不发 StateCorrection -> 本机预测不一定被拉回
```

所以现在需要给“迟到输入被服务端重排”增加一个明确、限频的权威纠偏触发点。

## 影响范围

预计修改：

- `src/roomserver/logic/sync.go`
  - 在 `playerSyncState` 增加待纠偏标记，例如 `pendingCorrectionReason string`。

- `src/roomserver/logic/room.go`
  - 迟到输入被重排时，标记该玩家需要一次 `late_input_rescheduled` 纠偏。
  - 在服务端实际应用该重排输入、保存权威历史后，按现有 `CorrectionMinIntervalTicks` 限频发送当前权威状态 correction。
  - 不恢复错帧 `predicted_state` 校验。

- `src/roomserver/logic/sync_test.go`
  - 增加测试：迟到输入重排后，在执行目标 tick 时会发送 correction。
  - 保留测试：迟到 predicted_state 不参与错帧 position_error 纠偏。

不修改：

- 协议结构和消息 ID。
- AOI 逻辑。
- 客户端文档结构。

## 设计方案

### 1. 增加新的 correction reason

在 `sync.go` 增加：

```go
correctionReasonLateInputRescheduled = "late_input_rescheduled"
```

语义：客户端输入晚到，服务端没有回滚历史，而是把输入排到后续 tick 执行；客户端本地预测时间线可能已经领先服务端权威，因此服务端主动发一次权威状态用于拉回。

### 2. 在重排时标记待纠偏

当前重排发生在：

```go
targetTick, ok := r.nextAvailableInputTick(syncState)
latestLateInput.ClientTick = targetTick
r.acceptInput(syncState, targetTick, latestLateInput)
```

修改为：

```go
r.acceptInput(...)
syncState.pendingCorrectionReason = correctionReasonLateInputRescheduled
```

只标记，不立即发。因为立即发时 targetTick 的输入还没被服务端执行，发的是重排前状态，客户端会刚拉回又马上看到服务端前进。

### 3. 在服务端执行后发送当前权威 correction

在 `updatePlayers` 中，服务端执行输入、保存权威历史后：

```text
inputForTick
simulatePlayerTick
lastAppliedTick = r.tick
saveAuthoritativeState
maybeSendPendingCorrection
verifyPredictedState
cleanupSyncState
```

新增 `sendPendingCorrection`：

```go
func (r *Room) sendPendingCorrection(player *Player, syncState *playerSyncState) {
  if pending reason empty: return
  if player.SyncMode != prediction_authoritative: clear and return
  if r.tick - lastCorrectionTick < CorrectionMinIntervalTicks: return
  sendCorrection(current authoritative state, reason, 0, 0)
  clear pending reason on success
}
```

这样 correction 的 `rollback_tick` 是服务端已经执行过的权威 tick，客户端可以覆盖该 tick 并重放之后历史。

### 4. 为什么不直接提高阈值或恢复 predicted_state 重排

- 提高 `position_tolerance` 只能减少 correction，不解决分叉。
- 恢复 `predicted_state` 重排会再次导致错帧误纠偏，正常网络也拉回。
- 迟到输入重排本身就是服务端发现“客户端时间线不理想”的信号，用它触发一次限频权威 correction 更直接。

## 错误处理和边界

- 如果发送队列满，`sendCorrection` 失败时不清 pending reason，下一次 tick 可继续尝试，但受 `CorrectionMinIntervalTicks` 限制。
- 如果玩家离房或死亡，pending 状态随 `syncState` 删除或不再发送。
- 如果随后收到正常同 tick predicted_state，仍由 `verifyPredictedState` 做普通误差纠偏。
- 该 correction 的误差字段 `position_error` / `angle_error` 填 0，因为它不是来自同 tick predicted_state 比较，而是服务端主动权威对齐。

## 兼容性影响

- 不改协议，客户端已有 `MsgStateCorrection` 处理即可兼容。
- 客户端可能看到新的 `reason = "late_input_rescheduled"`，如果客户端 reason 只用于 debug，不需要特殊处理；如果客户端对 reason 做白名单，需要加入该值。
- 弱网下会增加 correction 数量，但按 `CorrectionMinIntervalTicks` 限频，默认至少间隔 2 tick。

## 性能考虑

- 每个玩家只增加一个字符串标记和一次 tick 检查。
- 不做服务端历史回滚，不增加批量重算成本。
- correction 数量比每帧强拉少很多，只在输入重排时触发且限频。

## 验证方式

1. 运行：

```bash
go test ./src/roomserver/logic
```

2. 编译：

```bash
./scripts/build_all.sh
```

3. 重启服务。

4. 联调验证：

- 正常网络：不应频繁 correction。
- 弱网 `lag 100ms drop 3%`：远端玩家继续移动，且本机预测与远端看到的位置出现明显分叉时，应收到 `MsgStateCorrection` 拉回。
- 客户端日志里能看到新的 reason：`late_input_rescheduled`。

## 自我审查

- 是否遗漏已有结构：纠偏发送入口统一走 `sendCorrection`，新增 pending reason 不绕过协议。
- 是否过度设计：没有做服务端历史回滚，不改协议，只补齐重排输入后的权威对齐信号。
- 协议兼容风险：reason 新值可能需要客户端 debug 白名单更新，但消息结构不变。
- 错误处理风险：需要确保发送失败不误清 pending。
- 性能风险：低。
- 后续扩展：真正完整方案仍是客户端稳定 input lead + 服务端可选历史回滚；当前修复先保证弱网 1v1 同步体验。

## 最终方案

为迟到输入重排增加 `late_input_rescheduled` 待纠偏标记，并在服务端实际执行重排输入后的 tick 发送一次限频 `StateCorrection`。不再使用错帧 `predicted_state` 触发 correction。

等待确认后实施。
