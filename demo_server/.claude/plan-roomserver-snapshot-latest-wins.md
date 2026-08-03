# Roomserver Snapshot 弱网位置不一致排查与修复方案

## 排查结论

当前问题不是 `late_input_rescheduled` correction 继续触发。该 reason 已从服务端代码移除，当前 roomserver 代码不会再生成这个 correction。

新的现象是：弱网环境下，本机预测位置与远端客户端看到的该玩家位置不一致。服务端日志中出现大量：

```text
room snapshot send dropped
```

并且后续出现 session read timeout。这说明至少有一个客户端连接在弱网期间收包或读包跟不上，服务端 `Session.Send` 的发送队列被填满，新的 snapshot 被丢弃。远端玩家只依赖 snapshot 显示其他玩家，所以它看到的位置会落后于本机预测和服务端最新权威位置。

当前代码行为：

- `Room.broadcastSnapshots` 每个 snapshot tick 都调用 `player.Session.Send(message)`。
- `Session.Send` 是有限 channel，满了就直接返回 false。
- 弱网时 KCP 是可靠有序流，旧 snapshot 会排队等待发送或重传，新 snapshot 可能被丢弃。
- 这会让客户端继续消费旧状态，远端表现自然与本机预测不一致。

## 是否客户端问题

目前服务端已有明确证据：snapshot 发送队列会满并丢包，因此不能简单归因客户端。

但客户端也必须做两件事，否则服务端修完后仍可能表现异常：

1. 对远端玩家 snapshot 必须按 `server_tick` 丢弃旧包：`snapshot.server_tick <= lastRemoteSnapshotTick` 不能再应用。
2. 远端玩家只能用 snapshot 插值或短外推，不能用迟到 snapshot 直接覆盖到旧位置造成回退。

当前仓库里没有 Unity 客户端源码，无法直接验证 `RoomSessionBehaviour.cs` 的实现。如果客户端没有做以上两点，也会造成远端玩家位置回退或长期落后。

## 服务端修复目标

对 snapshot 采用 “latest wins” 策略：弱网下旧 snapshot 不应堵住队列，新 snapshot 应覆盖旧 snapshot。Correction、JoinAck、Error 等关键消息仍保持可靠发送语义。

## 影响范围

预计修改：

- `src/roomserver/service/session.go`
  - 增加 snapshot 专用发送策略，避免队列满时继续保留旧 snapshot。
  - 关键消息仍走现有 `Send`。

- `src/roomserver/logic/room.go`
  - `broadcastSnapshots` 使用新的 snapshot 发送方法。
  - 可保留队列满日志，但需要节流，避免弱网时日志刷屏影响性能。

可能新增测试：

- `src/roomserver/service/session_test.go`
  - 验证 snapshot 队列满时不会阻塞关键消息。
  - 验证 snapshot 可以被丢弃或覆盖，而不是让旧 snapshot 无限堆积。

## 设计方案

优先方案：在 `Session` 增加独立的 latest snapshot 槽。

```text
control messages: Send -> sendCh
snapshot messages: SendSnapshot -> latestSnapshot
writeLoop:
  1. 优先写 control messages
  2. 空闲时取 latestSnapshot 写出
  3. 如果 latestSnapshot 被新 snapshot 覆盖，只发送最新的
```

这样：

- snapshot 不再挤占 correction / error / join ack 的发送队列。
- 弱网恢复后不会把一堆旧 snapshot 依次发给客户端。
- 远端显示最多丢中间帧，但会更快追到最新权威状态。

如果改动要更小，可以先做次优方案：snapshot 队列满时直接丢弃新 snapshot，并降低日志。但这只能减少压力，不能解决旧 snapshot 堵队列的问题，所以不推荐作为最终修复。

## 兼容性

- 不改协议字段。
- 不改客户端消息解析。
- 客户端仍收到 `MsgSnapshot = 6`。
- 弱网下 snapshot 可能丢更多中间帧，但这是状态同步正确策略；远端表现靠插值和短外推平滑。

## 验证方式

1. 运行单测：

```bash
go test ./src/roomserver/...
```

2. 编译：

```bash
./scripts/build_all.sh
```

3. 重启所有服务。

4. 联调：

- 正常网络：远端玩家位置持续更新，无明显回退。
- `lag 10ms drop 3%`：远端玩家可短暂插值滞后，但不应长期停在旧位置。
- 关闭弱网后：远端玩家应快速追到最新 snapshot，不应继续播放旧 snapshot 队列。
- roomserver 日志中 `room snapshot send dropped` 应明显减少或消失，不能持续刷屏。

## 最终方案

服务端修 snapshot 发送策略为 latest-wins；同时要求客户端确认按 `server_tick` 丢弃旧 snapshot。服务端修复后再用弱网场景验证。如果仍不一致，再需要看 Unity 客户端 `RoomSessionBehaviour` 的 snapshot 应用代码。

等待确认后实施。
