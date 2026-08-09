# 阶段二：消息协议和字段说明

本阶段目标：看懂客户端和 roomserver 之间的 KCP 业务消息格式，以及每个 protobuf 字段的业务含义。

## 1. 消息帧格式

代码在 [../protocol/message.go](../protocol/message.go)。

每条业务消息由 6 字节头和 payload 组成：

```text
0-1 字节：message type，uint16，大端
2-5 字节：payload length，uint32，大端
6+  字节：payload，protobuf 二进制
```

`ReadMessage` 会先读 6 字节头，再根据 payload 长度读取正文，并检查 `MaxPayloadSize`。

`WriteMessage` 会把 `Message.Type` 和 `Message.Payload` 封装成同样格式写入连接。

## 2. Message 字段

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `Type` | `uint16` | 消息类型，用来决定 payload 应该按哪个 protobuf 结构解析 |
| `Payload` | `[]byte` | protobuf 编码后的消息负载 |

辅助函数：

- `NewProtoMessage`：把 `proto.Message` marshal 成 protobuf payload
- `DecodeProto`：把 protobuf payload 解码到指定 `proto.Message`
- `ReadMessage`：从连接读取一条业务消息
- `WriteMessage`：向连接写出一条业务消息

## 3. 消息类型

| 类型 | 数值 | 方向 | payload 结构 | 含义 |
| --- | ---: | --- | --- | --- |
| `MsgJoinRoom` | `1` | 客户端 -> 服务端 | `roompb.JoinRoomReq` | 请求加入房间 |
| `MsgJoinRoomAck` | `2` | 服务端 -> 客户端 | `roompb.JoinRoomResp` | 入房结果和初始状态 |
| `MsgHeartbeat` | `3` | 客户端 -> 服务端 | `roompb.Heartbeat` | 心跳请求 |
| `MsgHeartbeatAck` | `4` | 服务端 -> 客户端 | `roompb.Heartbeat` | 心跳响应 |
| `MsgPlayerInput` | `5` | 客户端 -> 服务端 | `roompb.PlayerInput` | 单帧输入 |
| `MsgSnapshot` | `6` | 服务端 -> 客户端 | `roompb.Snapshot` | 状态快照 |
| `MsgError` | `7` | 服务端 -> 客户端 | `roompb.ErrorResp` | 错误响应 |
| `MsgGameStart` | `11` | 服务端 -> 客户端 | `roompb.GameStart` | 对局开始通知 |
| `MsgGameOver` | `12` | 服务端 -> 客户端 | `roompb.GameOver` | 对局结束通知 |
| `MsgPlayerStatsQuery` | `13` | 客户端 -> 服务端 | `roompb.PlayerStatsReq` | 查询玩家战绩 |
| `MsgPlayerStatsResp` | `14` | 服务端 -> 客户端 | `roompb.PlayerStatsResp` | 玩家战绩响应 |

消息号 8、9 和 10 曾用于旧版输入和预测确认消息，已经废弃且不可复用。

## 4. JoinRoomReq 字段

客户端发送 `MsgJoinRoom`。服务端处理入口是 [../service/server.go](../service/server.go) `handleJoinRoom`。

| proto 字段 | 类型 | 含义 |
| --- | --- | --- |
| `token` | `string` | matchserver 签发的短期入房令牌，包含 roomID、playerID、serverID 等声明 |

服务端会做这些校验：

```text
DecodeProto(roompb.JoinRoomReq)
  -> ParseRoomToken
  -> claims.ServerID == cfg.ServerID
  -> claims.RoomID != "" && claims.PlayerID != 0
  -> session.SetPlayer
  -> RoomManager.JoinRoom
```

## 5. JoinRoomResp 字段

服务端发送 `MsgJoinRoomAck`。构造位置是 [../logic/room.go](../logic/room.go) `buildJoinAck`。

| proto 字段 | 类型 | 含义 |
| --- | --- | --- |
| `status` | `bool` | 是否入房成功 |
| `content` | `string` | 响应说明，例如 `ok`、`room is full` |
| `room_id` | `string` | 房间 ID |
| `tick` | `int64` | 当前房间服务端帧号 |
| `spawn_id` | `string` | 当前玩家分配到的出生点 ID |
| `x` | `double` | 初始 X 坐标 |
| `y` | `double` | 初始 Y 坐标 |
| `z` | `double` | 初始 Z 坐标 |
| `yaw` | `double` | 初始水平视角，单位角度 |
| `pitch` | `double` | 初始垂直视角，单位角度 |
| `tick_rate` | `int32` | 房间逻辑帧率 |
| `snapshot_rate` | `int32` | 快照发送频率 |
| `server_time` | `int64` | 服务端时间戳，Unix 毫秒 |
| `map_id` | `string` | 服务端当前地图 ID |
| `physics_hash` | `string` | 服务端物理数据 hash |
| `game_duration_seconds` | `int64` | 对局时长秒数，当前默认 180 |
| `game_started` | `bool` | 当前房间是否已开始对局 |
| `game_start_tick` | `int64` | 对局开始服务端帧号，未开始为 0 |
| `game_end_tick` | `int64` | 对局结束服务端帧号，未开始为 0 |

客户端应先看 `status`。如果 `status=false`，只使用 `content` 展示或记录失败原因。只有 `status=true` 时才使用出生点、tick 和快照配置初始化本地房间状态。

## 6. Heartbeat 字段

客户端发送 `MsgHeartbeat`，服务端返回 `MsgHeartbeatAck`。处理位置是 [../service/server.go](../service/server.go) `handleHeartbeat`。

| proto 字段 | 类型 | 含义 |
| --- | --- | --- |
| `client_time` | `int64` | 客户端发送心跳时的本地时间戳 |
| `server_time` | `int64` | 服务端响应心跳时的 Unix 毫秒时间戳 |
| `server_tick` | `int64` | 玩家所在房间当前服务端帧号，未入房时为 0 |

心跳可以用于保持连接和估算 RTT。纯状态同步下，客户端不需要用它驱动预测 tick。

## 7. PlayerInput 字段

单帧输入，类型是 `MsgPlayerInput`。服务端会直接清洗并按收到顺序排到后续服务端 tick。

| proto 字段 | 类型 | 含义 |
| --- | --- | --- |
| `client_tick` | `int64` | 客户端本地逻辑帧号，仅用于诊断 |
| `move_x` | `double` | 左右移动输入，建议范围 `[-1, 1]` |
| `move_z` | `double` | 前后移动输入，建议范围 `[-1, 1]` |
| `yaw` | `double` | 水平视角，服务端会归一化到 `[-180, 180]` |
| `pitch` | `double` | 垂直视角，服务端会限制到 `[-89, 89]` |
| `fire` | `bool` | 当前输入帧是否开火 |
| `jump` | `bool` | 当前输入帧是否跳跃 |

服务端不会信任客户端传来的坐标。移动位置由 [../logic/movement.go](../logic/movement.go) 和物理后端计算。

## 8. PlayerState 和 Snapshot 字段

快照类型是 `MsgSnapshot`，构造位置是 [../logic/room.go](../logic/room.go) `broadcastSnapshots`。

`PlayerState`：

| proto 字段 | 类型 | 含义 |
| --- | --- | --- |
| `player_id` | `uint64` | 玩家 ID |
| `x` | `double` | 服务端权威 X 坐标 |
| `y` | `double` | 服务端权威 Y 坐标 |
| `z` | `double` | 服务端权威 Z 坐标 |
| `yaw` | `double` | 服务端认可的水平视角 |
| `pitch` | `double` | 服务端认可的垂直视角 |
| `hp` | `int32` | 生命值 |
| `spawn_id` | `string` | 玩家出生点 ID |
| `kill_count` | `int32` | 击杀数量 |
| `death_count` | `int32` | 死亡数量 |
| `invincible` | `bool` | 当前服务端帧是否处于无敌状态 |
| `invincible_until_tick` | `int64` | 无敌结束帧号 |

`Snapshot`：

| proto 字段 | 类型 | 含义 |
| --- | --- | --- |
| `server_tick` | `int64` | 这份快照对应的服务端帧号 |
| `players` | `repeated PlayerState` | 当前玩家自己和 AOI 可见玩家的状态 |

当前 `broadcastSnapshots` 会先把自己的状态放进 `players`，再追加 AOI 可见的其他玩家。

## 9. GameStart 字段

两名玩家都进入房间后，服务端发送 `MsgGameStart`。构造位置是 [../logic/room.go](../logic/room.go) `broadcastGameStart`。

| proto 字段 | 类型 | 含义 |
| --- | --- | --- |
| `room_id` | `string` | 房间 ID |
| `server_tick` | `int64` | 服务端当前帧号 |
| `start_tick` | `int64` | 对局开始帧号 |
| `end_tick` | `int64` | 对局结束帧号 |
| `duration_seconds` | `int64` | 对局时长秒数 |
| `server_time` | `int64` | 服务端时间戳，Unix 毫秒 |

## 10. GameOver 字段

对局达到限时后，服务端发送 `MsgGameOver`。构造位置是 [../logic/room.go](../logic/room.go) `broadcastGameOver`。

| proto 字段 | 类型 | 含义 |
| --- | --- | --- |
| `room_id` | `string` | 房间 ID |
| `server_tick` | `int64` | 服务端当前帧号 |
| `start_tick` | `int64` | 对局开始帧号 |
| `end_tick` | `int64` | 对局结束帧号 |
| `reason` | `string` | 结束原因，限时结束为 `time_limit` |
| `server_time` | `int64` | 服务端时间戳，Unix 毫秒 |
| `players` | `repeated PlayerState` | 结束时玩家权威状态 |

## 11. PlayerStatsReq 和 PlayerStatsResp 字段

客户端发送 `MsgPlayerStatsQuery` 查询玩家战绩。处理位置是 [../service/server.go](../service/server.go) `handlePlayerStatsQuery`。

`PlayerStatsReq`：

| proto 字段 | 类型 | 含义 |
| --- | --- | --- |
| `player_id` | `uint64` | 目标玩家 ID，为 0 或不传时查询自己 |

`PlayerStatsResp`：

| proto 字段 | 类型 | 含义 |
| --- | --- | --- |
| `status` | `bool` | 是否查询成功 |
| `content` | `string` | 响应说明 |
| `room_id` | `string` | 房间 ID |
| `server_tick` | `int64` | 查询时房间服务端帧号 |
| `stats` | `PlayerStats` | 玩家战绩 |

`PlayerStats`：

| proto 字段 | 类型 | 含义 |
| --- | --- | --- |
| `player_id` | `uint64` | 玩家 ID |
| `kill_count` | `int32` | 击杀数量 |
| `death_count` | `int32` | 死亡数量 |

查询方必须已经入房。`player_id=0` 查询自己；指定玩家 ID 时只能查询当前房间内的玩家。

## 12. ErrorResp 字段

类型是 `MsgError`。构造位置是 [../service/server.go](../service/server.go) `sendError`。

| proto 字段 | 类型 | 含义 |
| --- | --- | --- |
| `code` | `string` | 错误码，例如 `bad_request`、`invalid_token`、`not_joined` |
| `content` | `string` | 错误说明 |
