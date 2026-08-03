# Roomserver late_input_rescheduled 纠偏降噪方案

## 需求理解

现在 `late_input_rescheduled` correction 在弱网开启后会持续出现，甚至关闭弱网后仍会继续一段时间。客户端日志里的：

```text
reason=late_input_rescheduled rollback=579 server=579 posErr=0.000 angleErr=0.000
```

说明这不是服务端检测到真实预测误差，而是服务端因为“输入迟到并被重排”主动发起的权威对齐。上一版把重排输入直接标记为待纠偏，触发条件太敏感。

要解决的问题：

- 偶发轻微迟到输入不应触发拉回。
- 弱网下连续迟到、客户端明显跟不上服务端时，仍需要服务端发 correction 兜底。
- 关闭弱网后，正常按时输入恢复时，应尽快停止 `late_input_rescheduled` correction。

## 影响范围

预计修改：

- `src/roomserver/logic/sync.go`
  - 在 `playerSyncState` 增加迟到输入统计字段，例如连续迟到次数、最近迟到 tick。
  - 增加内部阈值常量，例如连续迟到几次后才触发 late correction。

- `src/roomserver/logic/room.go`
  - 正常接受未执行 tick 的输入时，清理迟到统计和 pending correction。
  - 迟到输入重排时，只累计迟到统计；达到阈值后才标记 `late_input_rescheduled` 待纠偏。
  - `sendPendingCorrection` 发送成功后清理 pending，但迟到统计可保留到正常输入恢复时清理。

- `src/roomserver/logic/sync_test.go`
  - 增加测试：单次迟到输入重排不触发 correction。
  - 增加测试：连续迟到输入重排达到阈值后才触发 correction。
  - 保留普通 prediction error correction 测试。

不修改：

- 协议结构和消息 ID。
- AOI 逻辑。
- 客户端消息结构。

## 设计方案

### 1. 引入迟到纠偏阈值

增加内部常量：

```go
lateInputCorrectionConsecutiveThreshold = 3
```

含义：同一玩家连续发生 3 次“只能重排的迟到输入”后，才认为客户端时间线明显落后，需要服务端主动 correction。

### 2. 迟到输入只累计，不立即纠偏

当前逻辑：

```go
r.acceptInput(syncState, targetTick, latestLateInput)
syncState.pendingCorrectionReason = correctionReasonLateInputRescheduled
```

改成：

```go
r.acceptInput(syncState, targetTick, latestLateInput)
syncState.consecutiveLateInputReschedules++
if syncState.consecutiveLateInputReschedules >= lateInputCorrectionConsecutiveThreshold {
    syncState.pendingCorrectionReason = correctionReasonLateInputRescheduled
}
```

这样 `lag 10ms drop 3%` 下偶发丢包/重传不会马上拉回。

### 3. 正常输入恢复时清理状态

当 batch 中有正常 `inputTick > lastAppliedTick` 的输入被接受，说明客户端又开始提前或准时发送输入，应清理：

```go
syncState.consecutiveLateInputReschedules = 0
if syncState.pendingCorrectionReason == correctionReasonLateInputRescheduled {
    syncState.pendingCorrectionReason = ""
}
```

这样关闭弱网后，只要客户端 TickSync 恢复，late correction 会停止。

### 4. 保持限频 correction

`sendPendingCorrection` 继续受 `CorrectionMinIntervalTicks` 限制。必要时可以把 late correction 的最小间隔提高到 snapshot 间隔，但第一版先使用连续阈值降低误触发。

## 错误处理和边界

- 只要有正常输入被接受，就清理 late 状态。
- 只有全部输入都迟到、最终发生重排时，才累计 late 次数。
- 太旧输入仍按 `stale_input` 处理，不参与这个连续计数。
- 太未来输入仍丢弃。
- 重复 tick 不覆盖先到输入。
- 如果发送 correction 失败，pending reason 保留，下次 tick 再尝试。

## 兼容性影响

- 不改协议。
- 客户端仍按现有 `MsgStateCorrection` 处理。
- `late_input_rescheduled` 出现频率会显著降低。
- 如果客户端长期没有 input lead，即使不开弱网，也可能连续迟到并触发 correction；这时说明客户端 TickSync 仍需调整，不是服务端误判。

## 性能考虑

- 每个玩家只新增几个整数字段。
- 不增加网络消息，反而减少 correction 数量。
- 不做服务端历史回滚。

## 验证方式

1. 运行：

```bash
go test ./src/roomserver/logic
```

2. 编译：

```bash
./scripts/build_all.sh
```

3. 重启所有服务。

4. 联调验证：

- 正常网络：不应出现持续 `late_input_rescheduled`。
- 开启 `lag 10ms drop 3%`：偶发迟到不应频繁拉回。
- 更明显弱网或客户端 tick 明显落后时：连续迟到后仍能收到 `late_input_rescheduled` 拉回。
- 关闭弱网后：正常输入恢复后 correction 停止。

## 自我审查

- 是否遗漏现有代码：迟到重排入口在 `handleInputBatch`，发送入口在 `sendPendingCorrection`，修改点集中。
- 是否过度设计：不引入服务端回滚和新协议，只加阈值和清理状态。
- 协议兼容风险：无。
- 错误处理风险：需要测试正常输入恢复会清 late 状态。
- 性能风险：低。
- 扩展风险：阈值是内部常量，后续可配置化。

## 最终方案

把 `late_input_rescheduled` 从“每次迟到重排都 correction”改为“连续迟到重排达到阈值才 correction”，并在正常输入恢复时清理 pending 和计数。这样既保留弱网兜底拉回，又避免 lag 10ms/drop 3% 或弱网关闭后的持续拉回。

等待确认后实施。
