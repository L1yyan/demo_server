# 删除 roomserver 批量输入帧方案

## 1. 需求理解

在已经移除客户端预测后，继续删除批量输入帧能力。最终客户端只发送单帧 `MsgPlayerInput`，服务端只接收 `roompb.PlayerInput`，不再支持 `MsgPlayerInputBatch`、`PlayerInputBatch`、`PlayerInputFrame`、`frames`、`max_input_batch_frames` 等批量输入相关结构。

## 2. 影响范围

预计修改范围：

- `pb/room/room.proto`：删除 `PlayerInputFrame` 和 `PlayerInputBatch`
- `gen/room/room.pb.go`、`gen/room/room_grpc.pb.go`：执行 `make proto` 重新生成
- `src/roomserver/protocol/message.go`：删除 `MsgPlayerInputBatch = 8`，保留消息号 8 为废弃不可复用说明
- `src/roomserver/config/config.go`、`config/config.yaml`、相关 config 测试：删除 `MaxInputBatchFrames` / `max_input_batch_frames`
- `src/roomserver/service/server.go`：删除 `handlePlayerInputBatch` 和 `MsgPlayerInputBatch` 分支
- `src/roomserver/logic/room_manager.go`：删除 `PushInputBatch`
- `src/roomserver/logic/room.go`：删除 `roomEventInputBatch`、`batch` 字段、`PushInputBatch`、`handleInputBatch`；单帧输入直接清洗并排队
- `src/roomserver/logic/sync.go`：删除 `inputFrameToPlayerInput`，`SyncConfig` 只保留 `MaxInputHoldTicks`
- `src/roomserver/logic/state_sync_test.go`：把批量输入测试改成单帧输入排队测试
- `src/roomserver/README.md` 和 `src/roomserver/learning/*.md`：删除批量输入文档，改为只描述单帧输入

## 3. 设计方案

### 3.1 协议设计

1. `PlayerInput` 保留，作为唯一输入消息 payload。
2. 删除 `PlayerInputFrame` 和 `PlayerInputBatch` message。
3. `MsgPlayerInputBatch = 8` 从代码常量中删除，消息号 8 和之前废弃的 9、10 一起标记为历史废弃号，不复用。
4. `.proto` 里无法像字段一样 reserved 顶层 message 名称，所以直接删除顶层 message；已发布字段号在相关 message 内继续保留已有 reserved。

### 3.2 服务层流程

`Server.HandleMessage` 只保留：

```text
MsgPlayerInput
  -> handlePlayerInput
  -> DecodeProto(roompb.PlayerInput)
  -> manager.PushInput
```

删除 `handlePlayerInputBatch`。客户端如果继续发送消息号 8，会走 unknown message，服务端返回 `MsgError{code: "unknown_message"}`。

### 3.3 logic 层输入排队

`Room.handleInput` 直接做完整输入处理：

```text
检查房间未结束
检查玩家存在且存活
sanitizePlayerInput
nextAvailableInputTick
acceptInput
```

`nextAvailableInputTick` 不再使用批量帧数量作为窗口。建议保留一个固定的小窗口或基于 `MaxInputHoldTicks` 计算排队上限，避免客户端高频输入把未来 tick 排得过远。推荐使用：

```text
maxTick = r.tick + max(1, MaxInputHoldTicks+1)
```

这样只允许排到短暂弱网缓冲范围内，行为和“最多沿用上一帧输入多少 tick”保持一致。

### 3.4 配置收敛

1. 删除 `MaxInputBatchFrames` 配置字段和 YAML。
2. `SyncConfig` 只保留 `MaxInputHoldTicks`。
3. `server.Start` 只向 logic 传 `MaxInputHoldTicks`。

## 4. 兼容性影响

这是破坏性协议简化：

1. 旧客户端如果继续发送 `MsgPlayerInputBatch = 8`，会收到 unknown message 错误。
2. 新客户端必须改成只发送 `MsgPlayerInput = 5`。
3. protobuf 删除 `PlayerInputBatch` / `PlayerInputFrame` 后，依赖这些生成类型的客户端和服务端代码都需要同步更新。
4. 消息号 8 不复用，避免旧客户端误发后被解释成其他含义。

## 5. 健壮性设计

1. 输入清洗继续拒绝 nil、NaN、Inf，并限制移动和视角范围。
2. 输入排队仍然只发生在房间 loop 内，不增加锁。
3. 使用有限未来排队窗口，避免单个客户端刷大量单帧输入导致 `syncState.inputs` 无界增长。
4. 房间结束、玩家不存在、玩家死亡时输入直接丢弃。
5. 开火和跳跃仍然只在精确输入帧触发；沿用输入时继续强制 `Fire=false`、`Jump=false`。

## 6. 性能考虑

1. 删除批量输入会让客户端高频移动时网络包数量增加，尤其是高 tickRate 或弱网环境。
2. 代码复杂度会下降，协议更简单，服务端少一次批量遍历和帧转换。
3. 如果后续出现包量问题，建议再加“输入压缩”或“输入采样”方案，但不要恢复预测相关结构。

## 7. 验证方式

实现后执行：

1. `make proto`
2. `gofmt` 修改过的 Go 文件
3. `go test ./src/roomserver/...`
4. `go test ./config ./src/roomserver/config`
5. `go test ./...`
6. 用 `rg` 检查 `PlayerInputBatch`、`PlayerInputFrame`、`MsgPlayerInputBatch`、`MaxInputBatchFrames`、`max_input_batch_frames` 是否只剩历史说明或完全消失

## 8. 自我审查

初始风险：

1. 只删除 proto message 不够，service 和 logic 里还会引用生成类型导致编译失败。
2. 直接删除 `MaxInputBatchFrames` 后，单帧输入仍需要一个有限未来排队窗口，否则高频输入可能排队过远。
3. 删除批量输入会增加网络包量，需要在最终说明里明确客户端必须按合理频率发送 `MsgPlayerInput`。
4. 顶层 proto message 没有 `reserved` 语法，不能伪造 reserved；只能删除 message，并保证消息号 8 不复用。

修正后的最终方案：彻底删除批量输入协议和服务端处理链路，只保留单帧 `PlayerInput`，并用 `MaxInputHoldTicks` 限制服务端未来输入排队窗口。

## 9. 等待确认

请确认是否按该方案实施。确认后再开始修改业务代码、协议和文档。
