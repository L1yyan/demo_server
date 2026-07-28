# 阶段二：消息协议和字段说明

本阶段目标：看懂客户端和 roomserver 之间的 KCP 业务消息格式，以及每个 JSON 字段的业务含义。

## 1. 消息帧格式

代码在 [../protocol/message.go](../protocol/message.go)。

每条业务消息由 6 字节头和 payload 组成：

```text
0-1 字节：message type，uint16，大端
2-5 字节：payload length，uint32，大端
6+  字节：payload，当前是 JSON
```

对应常量：

```go
const messageHeaderSize = 6
```

`ReadMessage` 会先读 6 字节头，再根据 payload 长度读取正文，并检查 `MaxPayloadSize`。

`WriteMessage` 会把 `Message.Type` 和 `Message.Payload` 封装成同样格式写入连接。

## 2. Message 字段

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `Type` | `uint16` | 消息类型，用来决定 payload 应该按哪个结构解析 |
| `Payload` | `[]byte` | 消息负载，当前使用 JSON 编码 |

辅助函数：

- `NewJSONMessage`：把 Go 结构体 marshal 成 JSON payload
- `DecodeJSON[T]`：把 JSON payload 解码成指定结构体
- `ReadMessage`：从连接读取一条业务消息
- `WriteMessage`：向连接写出一条业务消息

## 3. 消息类型

| 类型 | 数值 | 方向 | payload 结构 | 含义 |
| --- | ---: | --- | --- | --- |
| `MsgJoinRoom` | `1` | 客户端 -> 服务端 | `JoinRoomRequest` | 请求加入房间 |
| `MsgJoinRoomAck` | `2` | 服务端 -> 客户端 | `JoinRoomAck` | 入房结果和同步参数 |
| `MsgHeartbeat` | `3` | 客户端 -> 服务端 | `Heartbeat` | 心跳请求 |
| `MsgHeartbeatAck` | `4` | 服务端 -> 客户端 | `Heartbeat` | 心跳响应 |
| `MsgPlayerInput` | `5` | 客户端 -> 服务端 | `PlayerInput` | 旧版单帧输入 |
| `MsgSnapshot` | `6` | 服务端 -> 客户端 | `Snapshot` | 状态快照 |
| `MsgError` | `7` | 服务端 -> 客户端 | `ErrorResponse` | 错误响应 |
| `MsgPlayerInputBatch` | `8` | 客户端 -> 服务端 | `PlayerInputBatch` | 新版批量输入 |
| `MsgInputAck` | `9` | 服务端 -> 客户端 | `InputAck` | 输入接受和校验进度 |
| `MsgStateCorrection` | `10` | 服务端 -> 客户端 | `StateCorrection` | 权威状态纠偏 |

[../../../pb/room/room.proto](../../../pb/room/room.proto) 也定义了同名业务结构，字段含义和当前 JSON payload 基本对应。当前 KCP 链路实际使用的是 [../protocol/message.go](../protocol/message.go) 里的 Go 结构体和 JSON 编码。

## 4. JoinRoomRequest 字段

客户端发送 `MsgJoinRoom`。服务端处理入口是 [../service/server.go](../service/server.go) `handleJoinRoom`。

| JSON 字段 | Go 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `token` | `Token` | `string` | matchserver 签发的短期入房令牌，包含 roomID、playerID、serverID 等声明 |
| `sync_version` | `SyncVersion` | `int` | 客户端同步协议版本，当前大于 0 才可能启用预测模式 |
| `prediction_enabled` | `PredictionEnabled` | `bool` | 客户端是否声明自己已实现预测、回滚和重放 |
| `physics_hash` | `PhysicsHash` | `string` | 客户端物理数据 hash，用于判断客户端和服务端地图碰撞是否一致 |

服务端会做这些校验：

```text
DecodeJSON
  -> ParseRoomToken
  -> claims.ServerID == cfg.ServerID
  -> claims.RoomID != "" && claims.PlayerID != 0
  -> session.SetPlayer
  -> RoomManager.JoinRoom
```

## 5. JoinRoomAck 字段

服务端发送 `MsgJoinRoomAck`。构造位置是 [../logic/room.go](../logic/room.go) `buildJoinAck`。

| JSON 字段 | Go 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `ok` | `OK` | `bool` | 是否入房成功 |
| `room_id` | `RoomID` | `string` | 房间 ID |
| `content` | `Content` | `string` | 响应说明，例如 `ok`、`room is full` |
| `tick` | `Tick` | `int64` | 当前房间服务端帧号 |
| `spawn_id` | `SpawnID` | `string` | 当前玩家分配到的出生点 ID |
| `x` | `X` | `float64` | 初始 X 坐标 |
| `y` | `Y` | `float64` | 初始 Y 坐标 |
| `z` | `Z` | `float64` | 初始 Z 坐标 |
| `yaw` | `Yaw` | `float64` | 初始水平视角，单位角度 |
| `pitch` | `Pitch` | `float64` | 初始垂直视角，单位角度 |
| `tick_rate` | `TickRate` | `int` | 房间逻辑帧率 |
| `snapshot_rate` | `SnapshotRate` | `int` | 快照发送频率 |
| `server_time` | `ServerTime` | `int64` | 服务端时间戳，Unix 毫秒 |
| `sync_mode` | `SyncMode` | `string` | 当前玩家实际同步模式，可能是 `snapshot_only` 或 `prediction_authoritative` |
| `map_id` | `MapID` | `string` | 服务端当前地图 ID |
| `physics_hash` | `PhysicsHash` | `string` | 服务端物理数据 hash |
| `rollback_window_ticks` | `RollbackWindowTicks` | `int64` | 服务端保留的回滚历史窗口 |
| `future_input_window_ticks` | `FutureInputWindowTicks` | `int64` | 客户端输入允许领先服务端的最大帧数 |
| `prediction_keyframe_interval` | `PredictionKeyframeInterval` | `int64` | 服务端校验预测状态的关键帧间隔 |
| `position_tolerance` | `PositionTolerance` | `float64` | 普通位置误差阈值 |
| `hard_position_tolerance` | `HardPositionTolerance` | `float64` | 强制纠偏位置误差阈值 |
| `angle_tolerance` | `AngleTolerance` | `float64` | 视角误差阈值 |

客户端应先看 `ok`。如果 `ok=false`，只使用 `content` 展示或记录失败原因。只有 `ok=true` 时才使用出生点、tick、同步配置等字段初始化本地房间状态。

## 6. Heartbeat 字段

客户端发送 `MsgHeartbeat`，服务端返回 `MsgHeartbeatAck`。处理位置是 [../service/server.go](../service/server.go) `handleHeartbeat`。

| JSON 字段 | Go 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `client_time` | `ClientTime` | `int64` | 客户端发送心跳时的本地时间戳 |
| `server_time` | `ServerTime` | `int64` | 服务端响应心跳时的 Unix 毫秒时间戳 |
| `server_tick` | `ServerTick` | `int64` | 玩家所在房间当前服务端帧号，未入房时为 0 |

客户端可以用多次心跳估算服务端 tick 和本地 tick 的偏移。

## 7. PlayerInput 字段

旧版单帧输入，类型是 `MsgPlayerInput`。服务端会转换成只有一帧的 `PlayerInputBatch` 处理。

| JSON 字段 | Go 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `client_tick` | `ClientTick` | `int64` | 客户端本地逻辑帧号 |
| `move_x` | `MoveX` | `float64` | 左右移动输入，建议范围 `[-1, 1]` |
| `move_z` | `MoveZ` | `float64` | 前后移动输入，建议范围 `[-1, 1]` |
| `yaw` | `Yaw` | `float64` | 水平视角，服务端会归一化到 `[-180, 180]` |
| `pitch` | `Pitch` | `float64` | 垂直视角，服务端会限制到 `[-89, 89]` |
| `fire` | `Fire` | `bool` | 当前输入帧是否开火 |

服务端不会信任客户端传来的坐标。移动位置由 [../logic/movement.go](../logic/movement.go) 和物理后端计算。

## 8. PlayerInputBatch 字段

新版批量输入，类型是 `MsgPlayerInputBatch`。服务端处理入口是 [../service/server.go](../service/server.go) `handlePlayerInputBatch` 和 [../logic/room.go](../logic/room.go) `handleInputBatch`。

| JSON 字段 | Go 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `base_client_tick` | `BaseClientTick` | `int64` | 批量输入起始帧号，某帧未填 `client_tick` 时可作为兜底 |
| `frames` | `Frames` | `[]PlayerInputFrame` | 输入帧列表，长度不能超过 `MaxInputBatchFrames` |
| `last_received_server_tick` | `LastReceivedServerTick` | `int64` | 客户端最后收到的服务端帧号，当前主要预留用于后续节奏控制 |

## 9. PlayerInputFrame 字段

| JSON 字段 | Go 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `client_tick` | `ClientTick` | `int64` | 该输入对应的客户端逻辑帧号 |
| `move_x` | `MoveX` | `float64` | 左右移动输入 |
| `move_z` | `MoveZ` | `float64` | 前后移动输入 |
| `yaw` | `Yaw` | `float64` | 水平视角 |
| `pitch` | `Pitch` | `float64` | 垂直视角 |
| `fire` | `Fire` | `bool` | 是否开火 |
| `predicted_state` | `PredictedState` | `*PredictedPlayerState` | 客户端本地预测状态，可为空；服务端只用它做误差检测 |

## 10. PredictedPlayerState 字段

| JSON 字段 | Go 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `x` | `X` | `float64` | 客户端预测 X 坐标 |
| `y` | `Y` | `float64` | 客户端预测 Y 坐标 |
| `z` | `Z` | `float64` | 客户端预测 Z 坐标 |
| `yaw` | `Yaw` | `float64` | 客户端预测水平视角 |
| `pitch` | `Pitch` | `float64` | 客户端预测垂直视角 |
| `state_hash` | `StateHash` | `uint32` | 预测状态 hash，当前预留，后续可用于快速比较状态 |

服务端会先检查这些值是不是有限浮点数，然后按 tick 暂存到 `predictedStates`。

## 11. PlayerState 和 Snapshot 字段

快照类型是 `MsgSnapshot`，构造位置是 [../logic/room.go](../logic/room.go) `broadcastSnapshots`。

`PlayerState`：

| JSON 字段 | Go 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `player_id` | `PlayerID` | `uint64` | 玩家 ID |
| `spawn_id` | `SpawnID` | `string` | 玩家出生点 ID |
| `x` | `X` | `float64` | 服务端权威 X 坐标 |
| `y` | `Y` | `float64` | 服务端权威 Y 坐标 |
| `z` | `Z` | `float64` | 服务端权威 Z 坐标 |
| `yaw` | `Yaw` | `float64` | 服务端认可的水平视角 |
| `pitch` | `Pitch` | `float64` | 服务端认可的垂直视角 |
| `hp` | `HP` | `int` | 生命值 |

`Snapshot`：

| JSON 字段 | Go 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `server_tick` | `ServerTick` | `int64` | 这份快照对应的服务端帧号 |
| `players` | `Players` | `[]PlayerState` | 当前玩家自己和 AOI 可见玩家的状态 |

当前 `broadcastSnapshots` 会先把自己的状态放进 `players`，再追加 AOI 可见的其他玩家。

## 12. InputAck 字段

预测模式下，服务端按快照频率发送 `MsgInputAck`。构造位置是 [../logic/room.go](../logic/room.go) `broadcastAcks`。

| JSON 字段 | Go 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `server_tick` | `ServerTick` | `int64` | 服务端当前帧号 |
| `last_accepted_input_tick` | `LastAcceptedInputTick` | `int64` | 服务端最后接受的客户端输入帧号 |
| `last_verified_input_tick` | `LastVerifiedInputTick` | `int64` | 服务端最后做过预测误差校验的帧号 |

客户端收到 ack 后可以清理过旧输入历史，但不能立刻删除回滚窗口内所有预测状态。

## 13. StateCorrection 字段

预测误差超阈值时，服务端发送 `MsgStateCorrection`。构造位置是 [../logic/room.go](../logic/room.go) `sendCorrection`。

| JSON 字段 | Go 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `player_id` | `PlayerID` | `uint64` | 需要纠偏的玩家 ID |
| `rollback_tick` | `RollbackTick` | `int64` | 客户端应回滚到的服务端权威帧 |
| `server_tick` | `ServerTick` | `int64` | 发送纠偏时服务端当前帧 |
| `last_accepted_input_tick` | `LastAcceptedInputTick` | `int64` | 服务端最后接受的输入帧 |
| `state` | `State` | `PlayerState` | `rollback_tick` 对应的权威玩家状态 |
| `reason` | `Reason` | `string` | 纠偏原因，比如 `position_error`、`angle_error`、`stale_input` |
| `position_error` | `PositionError` | `float64` | 客户端预测位置和服务端权威位置的距离误差 |
| `angle_error` | `AngleError` | `float64` | 客户端预测视角和服务端权威视角的误差 |

客户端收到 correction 后，应该把本地玩家状态设置为 `state`，再从 `rollback_tick + 1` 开始用历史输入重放到当前本地帧。

## 14. ErrorResponse 字段

类型是 `MsgError`。构造位置是 [../service/server.go](../service/server.go) `sendError`。

| JSON 字段 | Go 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `code` | `Code` | `string` | 错误码，例如 `bad_request`、`invalid_token`、`not_joined` |
| `content` | `Content` | `string` | 错误说明 |
