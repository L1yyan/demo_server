# 移除 zero-error late_input_rescheduled 强制纠偏方案

## 需求理解

当前客户端日志：

```text
Prediction correction replayed. reason=late_input_rescheduled rollback=653 server=653 posErr=0.000 angleErr=0.000
```

说明服务端没有检测到预测误差，而是在“迟到输入重排”后主动发送了一个零误差 `StateCorrection`。这会导致：

- 开弱网时，服务端不断把迟到输入重排，客户端频繁收到 `late_input_rescheduled`。
- 关闭弱网后，客户端可能还有积压输入、TickSync 估算还没马上恢复，服务端继续短时间判断输入迟到。
- 因为 correction 是零误差兜底，不代表客户端真的错了，但客户端仍按 correction 回滚重放，于是表现为不必要的拉回。

结论：`late_input_rescheduled` 作为 correction reason 设计不合理。迟到输入重排只应该让服务端权威继续前进，不能作为“无误差依据”的回滚信号。

## 影响范围

预计修改：

- `src/roomserver/logic/sync.go`
  - 删除 `correctionReasonLateInputRescheduled`。
  - 删除 `lateInputCorrectionThreshold`。
  - 删除 `playerSyncState` 中 late correction 相关字段。

- `src/roomserver/logic/room.go`
  - 保留迟到输入重排逻辑。
  - 删除 `markLateInputRescheduled` / `clearLateInputCorrection` / `sendPendingCorrection`。
  - 正常输入不再维护 late correction 状态。

- `src/roomserver/logic/sync_test.go`
  - 保留“迟到输入能被重排执行”的测试。
  - 删除“连续迟到触发 late_input_rescheduled correction”的测试。
  - 增加或保留测试：迟到输入重排即使携带 predicted_state，也不会发送 correction。

不修改：

- 协议结构和消息 ID。
- AOI 逻辑。
- 普通 `position_error` / `angle_error` correction。

## 设计方案

### 1. 迟到输入重排只负责服务端权威继续移动

保留：

```text
inputTick <= lastAppliedTick 且迟到不超过 MaxInputHoldTicks
  -> 取 batch 最新迟到输入
  -> 排到下一可执行 targetTick
```

这样弱网下远端玩家仍能看到该玩家移动和转视角。

### 2. 不再发送 zero-error correction

删除：

```text
pendingCorrectionReason = late_input_rescheduled
sendPendingCorrection(...)
```

原因：没有同 tick 预测误差时，服务端不知道客户端模拟层是否真的需要回滚。发 `posErr=0 angleErr=0` 的 correction 会制造无意义拉回。

### 3. 真正拉回只保留两类

- `position_error`：同 tick 客户端预测状态和服务端权威位置误差超过阈值。
- `angle_error`：同 tick 预测视角误差超过阈值。
- `stale_input`：输入太旧时服务端明确要求客户端重新对齐。

这样 correction 都有明确语义，不再把“网络迟到”直接等同于“客户端预测错误”。

## 弱网下本机与远端看到的位置为什么还会短暂不一致

这是预测同步的正常现象：

```text
本机：本地预测，立即响应输入
远端：看服务端 snapshot，带网络延迟和服务端权威节奏
```

两者不会在任意时刻完全一致。需要拉回的是“本机预测与同 tick 服务端权威偏差超过阈值”的情况，而不是所有视觉上的短暂不同。

如果弱网下分叉持续很大，优先应检查客户端：

- `client_tick` 是否稳定领先服务端 1 到 2 tick。
- `predicted_state` 是否按 `prediction_keyframe_interval` 上报在正确 tick。
- correction 是否按服务端 `rollback_tick` 覆盖模拟层并重放。
- 远端玩家是否只做 snapshot 插值，不要把远端表现拿来反推本机模拟层。

## 兼容性影响

- 客户端不再收到 `reason = late_input_rescheduled`。
- 普通 `position_error` / `angle_error` correction 不受影响。
- 弱网下远端玩家仍会移动，因为迟到输入重排保留。
- 关闭弱网后，不会再因为 late correction pending 或迟到计数导致持续拉回。

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

4. 联调：

- 正常网络：不应出现 `late_input_rescheduled`。
- `lag 10ms drop 3%`：不应出现 `late_input_rescheduled` 拉回。
- 如果客户端预测真的偏离，仍应收到 `position_error` 或 `angle_error`。
- 远端玩家在弱网下仍应通过迟到输入重排继续移动。

## 自我审查

- 是否遗漏已有代码：late correction 相关逻辑集中在 `sync.go` 和 `room.go`，测试在 `sync_test.go`。
- 是否过度设计：这是回退不合理兜底逻辑，保留输入容错，不引入新协议。
- 协议兼容风险：无结构变更，只少一个 reason。
- 错误处理风险：弱网下不再强制拉回，需要客户端正确上报 predicted_state 才能触发真实 correction。
- 性能风险：减少 correction 和回滚，性能更好。

## 最终方案

删除 `late_input_rescheduled` correction 机制，保留迟到输入重排。服务端只在有真实同 tick 预测误差或 stale input 时发送 correction。

等待确认后实施。
