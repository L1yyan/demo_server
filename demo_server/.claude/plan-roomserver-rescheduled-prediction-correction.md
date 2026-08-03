# Roomserver 重排输入误纠偏修复方案

## 需求理解

现在弱网下已经能拉回，但不开弱网也会频繁纠错和拉回。原因大概率是上一轮修复把迟到输入的 `predicted_state` 也挂到了重排后的服务端 `targetTick`。

这会造成帧语义错位：

```text
客户端 predicted_state 表示 client_tick 原始帧模拟后的状态
服务端重排输入后，在 targetTick 执行这帧输入
targetTick != client_tick 时，拿 predicted_state 和 targetTick 权威状态比较是不成立的
```

所以正常网络下只要客户端输入略晚 1 tick，也可能被服务端错误判定为预测误差，然后发送 `StateCorrection`，客户端就被拉回。

## 影响范围

预计修改：

- `src/roomserver/logic/room.go`
  - 重排迟到输入时，不再把原 `predicted_state` 写到重排后的 `targetTick`。
  - 迟到输入重排只作为弱网输入容错，不能参与同 tick 预测误差校验。
  - 可增加轻量诊断日志，记录 late input reschedule 的原 tick、目标 tick、迟到 tick 数。

- `src/roomserver/logic/sync_test.go`
  - 修改上一轮新增的测试：迟到输入被重排后应驱动服务端移动，但不应因为原 `predicted_state` 触发同 tick correction。
  - 保留正常同 tick 预测误差会纠偏的测试。

不修改：

- 协议字段和消息 ID。
- AOI 逻辑。
- correction 消息结构。

## 设计方案

### 1. 区分两种输入

正常输入：

```text
inputTick > lastAppliedTick
服务端按 inputTick 缓存
predicted_state 可以绑定 inputTick
verifyPredictedState(inputTick) 可以做严格比较
```

重排迟到输入：

```text
inputTick <= lastAppliedTick 且迟到不超过 MaxInputHoldTicks
服务端把输入重排到 targetTick
只重排 input，不重排 predicted_state
```

### 2. 为什么不重排 predicted_state

`predicted_state` 是客户端在原始 `client_tick` 的预测结果，不是 targetTick 的预测结果。除非客户端重新按 targetTick 上报预测状态，否则服务端没有能力把这个状态“平移”到 targetTick。

因此重排后的输入只能保证远端玩家继续移动，不能作为 prediction verification 的输入。

### 3. 客户端拉回的正确触发方式

客户端要稳定收到 correction，需要满足：

```text
client_tick 与服务端 room tick 对齐
输入提前 1 到 2 tick 发送，避免被服务端重排
predicted_state 绑定同一个 client_tick
```

这样服务端会在同 tick 下比较客户端预测和服务端权威，误差超过阈值才发 correction。

服务端重排迟到输入是兜底，不应该作为正常同步模式。

## 错误处理和边界

- 太旧输入仍发送 stale correction 或丢弃，不改写历史。
- 太未来输入仍丢弃。
- 重复 tick 输入仍不覆盖先到输入。
- 轻微迟到输入仍重排到下一可执行 tick，保证远端可见移动。
- 重排输入不再写 `predictedStates[targetTick]`，避免正常网络下误纠偏。

## 兼容性影响

- 不改协议。
- 正确带 input lead 的客户端仍能正常触发 correction。
- input lead 不足的客户端，服务端仍能让远端玩家移动，但 prediction correction 会减少；这时应修客户端 tick lead，而不是让服务端用错帧预测状态强拉。

## 性能考虑

- 减少无效 correction 和客户端回滚，弱网和正常网络下都会更稳。
- 不增加额外网络消息。
- 诊断日志如增加，应节流或只在 reschedule 时按需输出，避免刷屏。

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

- 正常网络：不应频繁收到 `StateCorrection`，不应明显拉回。
- 弱网 `lag 100ms drop 3%`：远端玩家仍应移动和转视角。
- 客户端如果保持合理 `inputLeadTicks=1..2` 并上报同 tick predicted_state，真实误差仍应触发 correction。

## 自我审查

- 是否遗漏已有代码结构：问题集中在 `handleInputBatch` 的迟到输入重排和 `verifyPredictedState` 的同 tick 校验，修改范围明确。
- 是否过度设计：不新增服务端回滚、不改协议，只修正错误的 predicted_state 绑定。
- 协议兼容风险：无。
- 错误处理风险：需要确保测试覆盖“迟到输入能移动但不纠错”。
- 性能风险：更少 correction，风险降低。
- 后续扩展：如果要做完整弱网权威回滚，需要单独设计服务端历史重算；当前不混入这次修复。

## 最终方案

删除“迟到输入重排时同步重排 predicted_state”的逻辑。保留迟到 input 重排，让远端玩家能动；但只有未重排、同 tick 的 predicted_state 才参与 correction 校验。更新测试，验证正常网络不再因为错帧预测状态被拉回。

等待确认后实施。
