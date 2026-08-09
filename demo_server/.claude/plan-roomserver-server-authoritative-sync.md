# Roomserver 纯服务端权威状态同步改造方案

## 1. 需求理解

将当前 roomserver 从“客户端预测 + 服务端权威纠偏”改成“纯服务端权威状态同步”。客户端只发送输入指令，不做本地移动预测、不维护预测历史、不做 rollback，也不处理 InputAck / StateCorrection。服务端继续按固定 tick 清洗输入、推进物理和玩法结算，并按快照频率下发 Snapshot，客户端只以 Snapshot 作为显示状态来源。

补充修正：当前 KCP 业务帧的 payload 已经通过 protobuf 序列化和反序列化。`protocol.Message` 只负责 `message_type + payload_length + payload` 的帧封装；业务 payload 应以 `roompb` 生成类型为准，通过 `protocol.NewProtoMessage` 和 `protocol.DecodeProto` 处理，不再按 JSON payload 结构设计。

## 2. 影响范围

预计修改或删除以下范围：

- `pb/room/room.proto`：删除预测相关字段和消息，按规范 reserved 已发布字段号
- `gen/room/room.pb.go`、`gen/room/room_grpc.pb.go`：通过 `make proto` 重新生成
- `src/roomserver/protocol/message.go`：只保留 KCP 帧、消息号、proto 编解码和读写；删除 JSON payload 结构和 JSON 编解码
- `src/roomserver/config/config.go`、`config/config.yaml`、`config/config_test.go`、`src/roomserver/config/config_test.go`：删除预测配置，保留输入批量和弱网输入沿用配置
- `src/roomserver/service/server.go`：入房不再读取客户端预测能力；单帧和批量输入都用 `roompb` proto 解码
- `src/roomserver/logic/player.go`：删除玩家上的预测能力、同步版本、客户端物理 hash 和预测模式字段
- `src/roomserver/logic/sync.go`：删除预测模式、回滚窗口、预测状态历史、误差计算、纠偏辅助结构；保留状态快照转换和输入帧转换所需的非预测逻辑
- `src/roomserver/logic/room.go`、`src/roomserver/logic/room_manager.go`、`src/roomserver/logic/movement.go`：改为使用 `roompb.PlayerInput` / `roompb.PlayerInputBatch` 作为输入消息类型，移除预测校验、InputAck 广播、StateCorrection 发送、回滚窗口和迟到重排
- `src/roomserver/service/session_test.go`、roomserver logic/service 测试：更新预测消息相关测试，新增纯状态同步行为测试
- `src/roomserver/README.md`、`src/roomserver/learning/*.md`：更新文档；删除 `src/roomserver/CLIENT_PREDICTION_ROLLBACK.md`

## 3. 设计方案

### 3.1 协议设计

1. `JoinRoomReq` 只保留 `token`。
2. `JoinRoomResp` 保留入房结果、出生点、tick、tick_rate、snapshot_rate、server_time、map_id、physics_hash、对局时长和开始/结束 tick。
3. 删除预测协商字段：`sync_version`、`prediction_enabled`、请求侧 `physics_hash`、`sync_mode`、`rollback_window_ticks`、`future_input_window_ticks`、`prediction_keyframe_interval`、`position_tolerance`、`hard_position_tolerance`、`angle_tolerance`。
4. `PlayerInputFrame` 删除 `predicted_state`，只保留输入字段：`client_tick`、`move_x`、`move_z`、`yaw`、`pitch`、`fire`、`jump`。
5. 删除 `PredictedPlayerState`、`InputAck`、`StateCorrection`。
6. 自定义 KCP 消息号中删除 `MsgInputAck = 9` 和 `MsgStateCorrection = 10` 的业务使用，后续不复用这两个编号；`MsgGameStart = 11` 等已有编号不改动。
7. `.proto` 删除字段时使用 `reserved` 保留字段号和字段名，避免未来误复用。

### 3.2 Proto payload 处理

1. `service` 层所有入站业务消息用 `protocol.DecodeProto(message, &roompb.Xxx{})` 解码。
2. `service` / `logic` 层所有出站业务消息用 `protocol.NewProtoMessage(type, &roompb.Xxx{})` 编码。
3. `protocol/message.go` 删除原 JSON payload Go 结构体和 `NewJSONMessage` / `DecodeJSON`，避免代码误以为当前链路仍在用 JSON。
4. `logic` 层内部需要输入参数时直接接收 `roompb.PlayerInput` / `roompb.PlayerInputBatch`，或在函数边界转换为内部 `authoritativeInput`。不再使用 `protocol.PlayerInputBatch` 这类 JSON payload 结构。
5. 战绩查询内部结果改成 logic 自有结构或直接转成 `roompb.PlayerStats`，不再依赖 `protocol.PlayerStats`。

### 3.3 服务端运行配置

1. 删除预测配置字段：`PredictionEnabled`、`RollbackWindowTicks`、`FutureInputWindowTicks`、`PredictionKeyframeInterval`、`PositionTolerance`、`HardPositionTolerance`、`AngleTolerance`、`CorrectionMinIntervalTicks`。
2. 保留非预测输入配置：`MaxInputBatchFrames` 和 `MaxInputHoldTicks`。
3. `SyncConfig` 收敛成状态同步/输入同步配置，只表达输入批量上限和缺帧沿用窗口；为减少不必要改动，优先保留 `SyncConfig` 名称但删除预测字段。

### 3.4 输入处理流程

纯状态同步下，客户端不再需要把 `client_tick` 对齐服务端预测帧，因此服务端不应再按回滚窗口判断 stale/future 输入。新的流程：

1. 单帧 `MsgPlayerInput`：清洗输入后排到当前房间的下一可执行 tick。
2. 批量 `MsgPlayerInputBatch`：按 frames 顺序清洗输入，最多接受 `MaxInputBatchFrames` 帧，依次排到后续可执行 tick。
3. `client_tick` 只作为客户端诊断字段，不驱动服务端时间，也不作为回滚校验依据；内部 `authoritativeInput.ClientTick` 可设置为实际执行 tick。
4. 如果玩家死亡、房间结束、输入非法或批量超长，直接丢弃或返回 bad_request，不触发纠偏消息。
5. 复活时清理尚未执行的未来输入，避免复活后旧输入继续生效。
6. 为避免恶意或异常客户端把输入排到无限未来，输入排队最多保留 `MaxInputBatchFrames` 个未来 tick；超过窗口的输入丢弃并记录节流日志。

### 3.5 服务端状态推进和下发

1. `Room.updatePlayers` 每 tick 取当前 tick 已排队输入。
2. 有输入、存在短时间沿用输入、或玩家处于空中时，调用 `simulatePlayerTick`。
3. 物理后端仍然是唯一位置来源，客户端坐标不会参与服务端状态。
4. 移除 `saveAuthoritativeState`、`verifyPredictedState`、`sendCurrentCorrection`、`sendCorrection`、`broadcastAcks`、迟到输入重排纠偏等逻辑。
5. `broadcastSnapshots` 保持 AOI 过滤，并继续用 `Session.SendSnapshot` 的“只保留最新快照”策略。

### 3.6 客户端配合约定

服务端改造后，客户端需要：

1. 入房只发送 token。
2. 持续发送输入，不发送预测状态。
3. 不处理 `MsgInputAck` 和 `MsgStateCorrection`。
4. 本地玩家和其他玩家都只按 `MsgSnapshot` 更新显示状态。
5. 为降低 10Hz 快照的抖动，客户端建议做快照插值；如果手感需要更低延迟，服务端可把 `snapshot_rate` 提到 `tick_rate`。

## 4. 兼容性影响

这是一次破坏性协议简化。旧的预测客户端会不兼容，因为服务端不再返回预测协商字段，也不再发送 InputAck / StateCorrection。

兼容性处理方式：

1. `.proto` 已删除字段使用 `reserved`，不复用字段号和字段名。
2. KCP 消息号 9 和 10 不再使用，后续也不复用。
3. `GameStart = 11`、`GameOver = 12`、战绩查询消息号保持不变。
4. `JoinRoomResp.physics_hash` 保留，用于客户端确认地图/资源版本，不再用于预测启用判断。
5. 老客户端发送的未知 proto 字段会被新服务端忽略，但老客户端如果依赖被删除的响应字段和纠偏消息，需要同步升级。

## 5. 健壮性设计

1. 输入清洗继续拒绝 NaN / Inf，并限制移动向量、yaw、pitch。
2. 输入批量继续限制最大帧数，避免单包过大或房间事件队列被刷爆。
3. 服务端不接受客户端位置，防止客户端直接篡改坐标。
4. 输入排帧使用房间 loop 串行状态，不引入额外锁。
5. 复活、死亡、无敌、开火命中仍全部由服务端结算。
6. 物理移动失败只记录日志并等待后续快照，不再发送纠偏消息。
7. 快照队列满时仍丢弃旧快照保留最新快照，避免慢连接堆积过期状态。

## 6. 性能考虑

1. 删除预测历史、预测误差计算、纠偏消息后，服务端每 tick 的内存占用和 CPU 开销会下降。
2. 如果将 `snapshot_rate` 提高到 `tick_rate`，带宽会增加；当前 2 人房间影响可控，后续多人房间需要结合 AOI、delta snapshot 或压缩评估。
3. 输入批量仍保留，可以减少客户端高频小包数量。
4. 不增加跨语言 PhysX 调用频率，仍然只在服务端 tick 中按玩家移动需要调用物理后端。

## 7. 验证方式

实现后执行：

1. `make proto`
2. `gofmt` 涉及的 Go 文件
3. `go test ./src/roomserver/...`
4. `go test ./config ./src/roomserver/config`
5. 如环境允许，再执行 `go test ./...`

重点新增/更新测试：

1. 入房请求只依赖 token，响应仍包含出生点和快照配置。
2. 单帧输入在没有预测 tick 对齐的情况下能驱动服务端移动。
3. 批量输入不带 predicted_state，按顺序排到后续 tick。
4. 纯状态同步下不会发送 `MsgInputAck` 和 `MsgStateCorrection`。
5. 复活后未来输入被清理，后续状态通过 Snapshot 下发。

## 8. 自我审查

初始方案中有一个错误：把 `protocol/message.go` 里的 JSON payload 结构当成了当前实际传输结构。根据当前代码和你的反馈，实际业务 payload 应该全部走 proto。因此修正为：`protocol` 包只保留帧封装和 proto 编解码，业务结构以 `pb/room/room.proto` 和 `gen/room` 为准。

其他风险点：

1. 只把 `prediction_enabled` 改成 false 不够彻底，预测结构、纠偏消息和历史状态仍会留在代码里，不符合“全部删掉”的目标。
2. 直接删除 `PlayerInputBatch` 不合适，因为批量输入不是预测专属能力，保留它可以减少网络包数量。
3. 纯状态同步后不能继续依赖客户端 tick 做 stale/future 判断，否则客户端停止预测 tick 后输入可能被错误丢弃。
4. 删除 proto 字段时必须 reserved，不能复用已经发布过的字段号。

修正后的最终方案：删除所有客户端预测、回滚、纠偏、误差校验和预测协商内容；保留 proto payload、客户端输入上报、服务端权威模拟、AOI Snapshot 下发和输入批量能力；输入由服务端按收到顺序排到后续 tick 执行。

## 9. 等待确认

请确认是否按该方案实施。确认后再开始修改业务代码、协议和文档。
