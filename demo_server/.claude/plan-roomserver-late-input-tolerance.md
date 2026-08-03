# Roomserver 弱网迟到输入容错修复方案

## 需求理解

当前弱网条件 `lag 100ms drop 3%` 下，客户端能看到远端玩家，但远端玩家不移动、不转视角。远端玩家自己的客户端有操作输入，说明本地预测正常，问题更可能出在服务端权威输入应用链路。

服务端当前批量输入逻辑要求输入 tick 必须落在尚未执行的服务端 tick 上：

```go
if inputTick <= syncState.lastAppliedTick {
    continue
}
```

20 tick/s 下 100ms 约等于 2 tick。若客户端没有足够 `inputLeadTicks`，输入到达服务端时经常已经 `<= lastAppliedTick`，会被直接丢弃。服务端权威状态不变，其他客户端从 snapshot 看到的远端玩家就不会动，也不会转视角。

## 影响范围

预计修改：

- `src/roomserver/logic/room.go`
  - 调整 `handleInputBatch` 对轻微迟到输入的处理。
  - 对已经错过服务端执行 tick、但仍在短迟到窗口内的输入，取最新一帧重排到下一可执行 tick。

- `src/roomserver/logic/sync_test.go`
  - 增加迟到输入测试，验证弱网晚到 1 到 3 tick 的输入不会被完全丢弃，而是能驱动服务端权威移动和视角更新。

可能不修改：

- 协议结构和消息 ID。
- 客户端文档。
- AOI 逻辑。

## 设计方案

在 `handleInputBatch` 中保留现有未来窗口和重复 tick 保护，同时新增“轻微迟到输入容错”。

### 现有行为

```text
inputTick < serverTick - rollbackWindow -> stale correction
inputTick > serverTick + futureWindow -> 丢弃
inputTick <= lastAppliedTick -> 丢弃
重复 inputTick -> 丢弃
其余输入 -> 按 inputTick 缓存，等待服务端 tick 应用
```

### 修正后行为

```text
1. 正常未来或未执行输入：仍按原 inputTick 缓存
2. 太旧输入：仍不改写历史
3. 轻微迟到输入：不做服务端回滚，取 batch 中最新一帧，重排到下一可执行 tick
```

轻微迟到判断建议：

```text
lastAppliedTick >= inputTick >= currentServerTick - maxInputHoldTicks
```

当前默认 `max_input_hold_ticks = 3`，可以覆盖 100ms 左右的弱网抖动。

重排规则：

```text
targetTick = max(currentServerTick + 1, lastAppliedTick + 1)
如果 targetTick 已有输入，则尝试 targetTick + 1，直到不超过 currentServerTick + futureInputWindowTicks
```

为了避免一个重复发送的 batch 把历史 8 帧全部排到未来造成输入延迟，只取 batch 中最新的一帧迟到输入做重排。连续输入下，每个新 batch 都会把最新操作续到接下来的服务端 tick。

### 为什么不是服务端回滚

服务端目前已经保存权威历史，但没有实现“迟到输入回滚重算服务端历史并广播修正其他玩家”的完整链路。直接改写历史会影响 snapshot、命中判定和 correction 语义，风险更高。当前 1v1 联调目标是弱网下远端玩家能持续移动和转向，因此轻微迟到输入重排到下一 tick 更贴合现阶段架构。

## 错误处理和边界

- `len(batch.Frames) == 0`：保持返回。
- `len(batch.Frames) > MaxInputBatchFrames`：保持拒绝。
- NaN/Inf 输入：仍由 `sanitizePlayerInput` 丢弃。
- 太未来输入：仍丢弃。
- 太旧输入：仍不改写服务端历史，必要时发送 stale correction。
- target tick 已被占用：向后找下一个可用 tick，但不能超过未来窗口。
- 找不到可用 target tick：丢弃该迟到输入，避免挤爆未来输入窗口。
- `Fire` 输入：迟到重排会把最新输入作为未来某 tick 的精确输入，因此 fire 可能被延后触发一次；重复 batch 不会覆盖同一 tick，避免同一点击多次触发。

## 兼容性影响

- 不改协议，客户端无需改字段。
- 正确实现了 input lead 的客户端仍按原逻辑走，不受影响。
- 没有 input lead 或弱网轻微延迟的客户端，不再因为输入晚到 1 到 3 tick 完全静止。
- `last_accepted_input_tick` 仍按服务端实际接受的 target tick 推进，符合当前服务端以 room tick 为权威时间线的实现。

## 性能考虑

- 每个 batch 最多 8 帧，新增扫描和少量 target tick 查找成本可忽略。
- 不引入 goroutine、锁或额外网络消息。
- 不做服务端历史回滚，避免高频重算和复杂状态一致性问题。

## 验证方式

1. 新增单测：迟到 1 tick 的输入被重排到下一 tick 后，玩家位置和 yaw 会更新。
2. 运行：

```bash
go test ./src/roomserver/logic
```

3. 编译：

```bash
./scripts/build_all.sh
```

4. 重启所有服务。
5. 弱网联调：`lag 100ms drop 3%` 下，双方 snapshot 中仍能看到远端玩家移动和转视角。

## 自我审查

- 是否遗漏已有结构：已确认输入链路是 `Server.handlePlayerInputBatch -> RoomManager.PushInputBatch -> Room.handleInputBatch -> updatePlayers -> inputForTick -> simulatePlayerTick`，问题集中在 `handleInputBatch` 对迟到 tick 的丢弃。
- 是否过度设计：没有引入服务端回滚或新协议，只做短迟到容错。
- 协议兼容风险：无协议变更。
- 错误处理风险：需要确保 target tick 不覆盖已有输入、不越过 future window。
- 性能风险：batch 很小，影响可忽略。
- 扩展风险：这不是完整服务器回滚方案，但符合当前架构和弱网联调目标。未来如果要严格还原客户端预测帧，可以再设计服务端历史回滚。

## 最终方案

在 `Room.handleInputBatch` 中新增轻微迟到输入重排逻辑：正常未来输入按原 tick 缓存；轻微迟到输入取 batch 最新帧，安排到下一可执行服务端 tick。新增单元测试覆盖迟到输入仍能驱动权威移动和视角更新。验证通过后编译并重启服务。

等待确认后实施。
