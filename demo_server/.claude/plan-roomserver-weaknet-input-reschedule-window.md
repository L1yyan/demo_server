# Roomserver 弱网输入晚到后远端静止修复方案

## 需求理解

当前现象：开启 `lag 50ms drop 3%` 后，被观测玩家本机仍有输入和本地预测，但远端玩家看到它直接不动；关闭弱网后，被观测玩家会被 correction 拉回到开启弱网前的位置。

这说明 correction 链路已经能工作，但弱网开启期间服务端权威状态没有持续被输入推进。远端玩家只看服务端 snapshot，所以服务端不动，远端也不动；弱网关闭后 correction 到达，本机被拉回服务端停留的位置。

主要原因是当前服务端迟到输入策略仍偏保守：

- 20 tick/s 下 1 tick = 50ms。
- 客户端 `inputLeadTicks` 最多 2 tick。
- 服务端当前 `MaxInputHoldTicks = 3`，只接受非常轻微的迟到输入重排。
- KCP 是可靠有序，3% 丢包会造成底层包重传和后续业务输入排队，即使基础 lag 只有 50ms，输入到达服务端时也可能晚于 3 tick。
- 超过窗口后服务端不会回滚历史，也不会用这些输入继续推进权威状态，导致远端看到静止。

## 影响范围

预计修改：

- `src/roomserver/config/config.go`
  - 调整默认 `MaxInputHoldTicks`，提高弱网下输入沿用和迟到重排容忍度。

- `src/roomserver/logic/sync.go`
  - 如有必要，在 `SyncConfig.Normalize` 中同步调整默认值。

- `src/roomserver/logic/room.go`
  - 扩大迟到输入重排判断范围：迟到但仍在 rollback window 内的输入，不再只限 `MaxInputHoldTicks`。
  - 保留“只取 batch 中最新迟到输入重排”的策略，避免把一堆历史输入排到未来造成更大延迟。
  - 增加输入处理诊断日志，记录 accepted、late_rescheduled、late_dropped、future_dropped、stale_correction 等分支。

- `src/roomserver/logic/sync_test.go`
  - 增加“超过旧 3 tick、但仍在 rollback window 内的迟到输入会被重排并推进服务端状态”的测试。
  - 保留迟到重排后 predicted_state 能触发 correction 的测试。

不修改协议，不修改客户端代码。

## 设计方案

### 1. 扩大缺帧沿用窗口

将默认 `MaxInputHoldTicks` 从 3 提高到 8。

含义：如果服务端短时间没有收到新输入，会继续沿用上一帧移动输入最多 8 tick，也就是 400ms。

这样 KCP 重传期间，远端不会立刻停住。输入恢复后，服务端再用最新输入继续推进，并通过 correction 拉回本机预测误差。

### 2. 扩大迟到输入重排范围

当前 `canRescheduleLateInput` 只允许：

```text
lastAppliedTick - inputTick <= MaxInputHoldTicks
```

修正为：

```text
inputTick > lastAcceptedInputTick
且 inputTick >= currentServerTick - rollbackWindowTicks
```

也就是：

- 太旧到 rollback window 外的输入仍然不重排。
- 已经确认过或更旧的重复输入不重排。
- 仍在 rollback window 内、且比上次接受输入更新的迟到输入，可以取最新一帧重排到下一可执行 tick。

这不是完整服务器历史回滚，而是“晚到输入取最新意图继续执行”。它更适合当前 1v1 联调目标：弱网下服务端权威状态不要停住。

### 3. 保持重排只取最新迟到帧

一个 KCP 重传恢复后可能一次到达多帧历史输入。服务端不应该把这些历史输入全部塞到未来，否则会把玩家操作延迟排队播放。

策略保持：

- batch 中正常未来输入优先按原 tick 接受。
- 如果没有正常输入，取最新迟到帧重排。
- 重排时同步写入该帧 predicted_state，并强制校验，保持 correction 闭环。

### 4. 输入诊断日志

增加节流诊断，方便下一轮弱网测试判断服务端到底有没有吃输入：

- `room input accepted`
- `room late input rescheduled`
- `room input dropped`

字段包含：

- room_id
- player_id
- input_tick
- target_tick
- server_tick
- last_applied_tick
- last_accepted_input_tick
- reason

日志需要节流，避免每 tick 高频刷屏。

## 兼容性

- 不改协议字段、消息类型和客户端请求格式。
- snapshot-only 客户端不受影响。
- prediction-authoritative 客户端无需更新即可受益。
- 服务端仍不接受 rollback window 外的过旧输入。
- `last_accepted_input_tick` 语义保持为服务端实际接受并排入权威时间线的 tick。

## 健壮性

- NaN/Inf 输入仍由 `sanitizePlayerInput` 丢弃。
- 超过 future input window 的输入仍丢弃。
- rollback window 外的 stale 输入仍触发当前状态纠偏，不重写历史。
- target tick 已占用时继续向后找，不超过 future input window。
- 重排找不到 target tick 时记录 dropped，不挤爆输入窗口。
- Fire 输入仍只在精确输入 tick 触发，沿用输入时会清掉 Fire，避免重复开火。

## 性能考虑

- 每包最多 8 帧，新增判断和日志节流成本很小。
- 没有新增 goroutine、锁竞争或网络消息类型。
- 沿用输入窗口从 3 到 8 会让服务端在短断输入时继续模拟玩家，属于每 tick 已有路径，不增加额外循环。
- 不做服务端历史回滚，避免每次迟到输入重算多帧物理。

## 验证方式

1. 运行逻辑测试：

```bash
go test ./src/roomserver/logic
```

2. 运行 service 测试：

```bash
go test ./src/roomserver/service
```

3. 编译：

```bash
./scripts/build_all.sh
```

4. 重启服务并确认端口：

- matchserver `:8090`
- roomserver UDP `:9001`
- logicserver `:8080`

5. 弱网验证：

- `lag 50ms drop 3%` 下远端玩家不应长时间静止。
- roomserver 日志应能看到迟到输入被 `late_rescheduled` 或新输入被 `accepted`。
- 若仍静止，日志可区分是输入没到服务端、被 future/stale 丢弃，还是房间事件队列拥塞。

## 自我审查

- 已确认当前 correction 能拉回，问题已从“纠偏不执行”转为“弱网期间服务端权威状态没有输入推进”。
- 只调客户端拉回不会解决远端静止，因为远端显示的是服务端 snapshot。
- 直接完整服务端回滚更严格，但需要重算历史物理、重发修正并处理命中判定，当前改动风险较高。
- 扩大重排和沿用窗口是现有架构下更小、更直接的修复。
- 需要避免无限沿用输入导致断线后角色一直跑，所以窗口只提到 8 tick，不做无界沿用。

## 最终方案

服务端先改两点：默认 `MaxInputHoldTicks` 提到 8；迟到输入只要还在 rollback window 内且比上次接受输入更新，就取 batch 最新帧重排到下一可执行 tick，并同步 predicted_state 强制校验。补充诊断日志和单测后编译重启服务。

等待确认后实施。
