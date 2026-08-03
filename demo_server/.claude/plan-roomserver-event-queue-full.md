# Roomserver 输入事件队列满修复方案

## 需求理解

Unity 客户端收到 `Roomserver error: input_failed room event queue full`，说明服务端在处理 `MsgPlayerInput` 或 `MsgPlayerInputBatch` 时，`RoomManager.PushInput/PushInputBatch` 投递到房间事件队列失败。当前房间事件队列是固定容量 `256` 的非阻塞 channel，输入属于高频消息，弱网恢复、客户端发送频率过高或房间 tick 中物理更新耗时偏高时，都可能让输入事件堆满队列并触发错误。

目标不是让客户端持续收到 `input_failed`，而是让高频输入路径具备削峰能力：控制类事件仍走房间事件队列，输入类事件尽量合并后由房间 loop 批量处理。

## 影响范围

预计修改或新增：

- `src/roomserver/logic/room.go`
  - 调整输入投递路径，增加待处理输入合并缓存和输入 flush 事件
  - 保持房间 loop 仍是唯一修改玩家状态、sync state、物理状态的地方
- `src/roomserver/logic/sync.go`
  - 如有必要，增加同步配置里的输入缓存上限字段或使用现有同步窗口计算上限
- `src/roomserver/logic/room_manager.go`
  - 保持对外 `PushInput/PushInputBatch` 语义，必要时传递队列或缓存配置
- `src/roomserver/config/config.go`
  - 如需要配置化，增加 `RoomEventQueueSize` 或 `PendingInputFrameLimit` 默认值与 Normalize
- `src/roomserver/logic/sync_test.go` 或新增 `room_input_queue_test.go`
  - 覆盖输入合并、队列满时不直接失败、重复 tick 不覆盖等行为
- `src/roomserver/service/server.go`
  - 预计只保留现有调用链，除非需要记录更清晰的输入投递失败原因
- 文档按需要更新 `src/roomserver/README.md` 或 learning 文档中的队列容量说明

## 设计方案

### 核心结构

1. 保留 `Room.events` 作为房间串行事件入口，但不再让每个输入包都占一个事件槽。
2. 在 `Room` 内新增受锁保护的待处理输入缓存，例如：
   - `pendingInputBatches map[uint64]protocol.PlayerInputBatch`：按玩家合并待处理输入
   - `inputMu sync.Mutex`：保护待处理输入缓存
   - `inputFlushQueued atomic.Bool`：保证队列里最多只有一个输入 flush 事件
3. 新增房间事件类型 `roomEventInputFlush`。
4. `Room.PushInputBatch` 改为：
   - 先把玩家输入帧合并到 `pendingInputBatches[playerID]`
   - 如果当前没有 flush 事件在队列中，则向 `Room.events` 投递一个 `roomEventInputFlush`
   - 如果 flush 事件已存在，直接返回成功，不重复占用事件队列
5. `Room.loop` 收到 `roomEventInputFlush` 时：
   - 一次性取出并清空待处理输入缓存
   - 在房间 goroutine 内调用现有 `handleInputBatch`，继续由原逻辑做窗口校验、sanitize、重复 tick 处理、late input reschedule、prediction correction

### 输入合并规则

1. 同一个玩家的多个 batch 合并为一个 batch。
2. 对同一 `client_tick` 的重复输入保持现有“先到优先”语义，后到重复 tick 不覆盖已缓存输入。
3. `client_tick == 0` 时继续沿用当前逻辑，把 `base_client_tick` 作为兜底 tick。
4. 为避免房间卡顿时待处理输入无界增长，会设置缓存上限：
   - 优先基于 `rollback_window_ticks + future_input_window_ticks + max_input_batch_frames` 计算合理上限
   - 超出上限时丢弃最旧输入帧，保留更接近当前服务端 tick 的输入
5. 旧版 `MsgPlayerInput` 保持兼容：可以继续走原 `handleInput` 逻辑，或在投递前包装成单帧 batch，但需要保留它对非法 tick 重排到 `r.tick + 1` 的行为。

### 调用流程

```text
Session.readLoop
  -> Server.handlePlayerInputBatch
  -> RoomManager.PushInputBatch
  -> Room.PushInputBatch
  -> merge pending input frames
  -> schedule one roomEventInputFlush
  -> Room.loop
  -> handleInputFlush
  -> handleInputBatch
  -> syncState.inputs / predictedStates
  -> Room.update 按 tick 消费输入
```

## 错误处理和健壮性

1. `session.PlayerID == 0`、JSON 解析失败、batch 空或超长仍由 service 层按现有逻辑返回错误。
2. 如果输入 flush 事件投递失败，说明队列已被控制事件或其他事件占满：
   - 不丢失已经合并到 pending 的输入
   - 后续输入到达时继续尝试调度 flush
   - 必要时记录节流日志，避免刷屏
3. 对非法浮点、过旧、过未来、重复 tick 的处理继续复用 `handleInputBatch`，避免新增一套不一致校验。
4. pending 缓存有上限，避免弱网恢复或异常客户端导致内存无限增长。
5. 房间结束后输入 flush 会自然被 `handleInputBatch` 的 `gameEnded` 分支丢弃。
6. join、leave、stats query 等低频控制事件仍走原事件队列，避免输入洪峰挤占每一个队列槽。

## 兼容性影响

1. 不修改 room JSON 协议字段，不修改消息类型编号。
2. `MsgPlayerInputBatch` 客户端不需要改协议即可受益。
3. `MsgInputAck`、`MsgSnapshot`、`MsgStateCorrection` 返回结构不变。
4. 行为变化：输入洪峰下服务端更倾向于合并和丢弃过旧输入，而不是直接返回 `input_failed room event queue full`。这符合实时对战同步预期。
5. 如果新增配置字段，会提供默认值，现有代码使用 `DefaultConfig()` 启动不需要额外配置。

## 性能考虑

1. 高频输入不再一包一个 channel 事件，能显著降低 `Room.events` 压力。
2. 合并缓存按玩家维度处理，当前默认 2 人房间开销很小。
3. pending 合并需要一次短锁，锁内只做 slice/map 合并，不做物理、JSON 或网络发送。
4. 房间 goroutine 仍串行执行权威模拟，不引入跨 goroutine 修改玩家状态的数据竞争。
5. 缓存上限避免异常客户端造成内存增长；丢弃策略优先保留更可能仍在服务端输入窗口内的输入。

## 验证方式

1. 运行格式化：

```bash
gofmt -w src/roomserver/logic/room.go src/roomserver/logic/sync.go src/roomserver/logic/room_manager.go src/roomserver/config/config.go
```

2. 运行单元测试：

```bash
go test ./src/roomserver/...
```

3. 重点新增或更新测试：

- 队列中已有一个 input flush 事件时，多次 `PushInputBatch` 不继续增加 `Room.events` 长度
- `Room.events` 接近满时，输入仍能被合并为一个 flush 事件
- 同一玩家重复 `client_tick` 不覆盖先到输入
- pending 超出上限时不会无界增长
- 现有预测纠偏、迟到输入重排、射击测试不回归

## 自我审查

1. 只扩大 `Room.events` 容量虽然简单，但无法解决弱网恢复或客户端发送频率异常导致的持续输入洪峰，因此不作为主要方案。
2. 直接在 service 层丢输入会破坏分层：service 不应理解房间 tick、sync state 和迟到输入规则，所以输入窗口判断仍应留在 logic 层。
3. 让多个 goroutine 直接写 `syncState.inputs` 会引入数据竞争，必须保持房间 goroutine 为唯一权威状态写入者。
4. pending 合并如果无上限，会把 channel 满问题转成内存增长问题，因此必须设置上限。
5. 旧版 `MsgPlayerInput` 有特殊 tick 重排逻辑，不能在包装成 batch 时无意改变兼容行为。
6. `map[uint64]` 遍历顺序不稳定，但输入只是写入每个玩家自己的 tick 缓存，射击结算仍发生在 `updatePlayers`，不新增比现状更强的顺序依赖。

## 修正后的最终方案

采用“输入合并 + 单个 flush 事件 + 有界 pending 缓存”的方案。实现时先保持协议和 service 层基本不变，只在 logic 层削峰；如需要配置化，只增加默认生效的配置字段。测试重点覆盖队列削峰和现有同步行为不回归。

等待用户确认后再修改业务代码。
