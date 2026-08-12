# Roomserver 状态同步策略与客户端适配说明

本文档面向客户端适配当前 roomserver 改动，重点说明新的状态同步策略、协议字段含义、消息收发顺序和客户端需要调整的逻辑。

## 1. 核心结论

当前 roomserver 已改为纯服务端权威同步：

```text
客户端：只采集输入并发送给服务端
服务端：校验输入、按服务端 tick 排帧、调用物理后端推进权威状态、下发 Snapshot
客户端：以服务端 Snapshot 作为最终显示状态
```

客户端不再上报本地坐标、速度或本地预测状态。服务端也不会读取客户端提供的 `x/y/z` 作为玩家真实位置。

客户端本地玩家和其他玩家都必须以 `MsgSnapshot` 中的 `PlayerState` 为准。客户端可以做纯显示层插值或短暂本地预测，但预测结果不能反向发送给服务端，也不能当作服务端认可的位置。

## 2. 传输帧格式

roomserver 使用 KCP 连接，业务层消息格式固定为：

```text
uint16 message_type      // 2 字节，大端序
uint32 payload_length    // 4 字节，大端序
bytes  payload           // protobuf payload
```

payload 使用 `pb/room/room.proto` 生成的 protobuf 类型。

## 3. 当前消息号

| ID | 名称 | 方向 | payload |
| ---: | --- | --- | --- |
| 1 | `MsgJoinRoom` | 客户端 -> 服务端 | `JoinRoomReq` |
| 2 | `MsgJoinRoomAck` | 服务端 -> 客户端 | `JoinRoomResp` |
| 3 | `MsgHeartbeat` | 客户端 -> 服务端 | `Heartbeat` |
| 4 | `MsgHeartbeatAck` | 服务端 -> 客户端 | `Heartbeat` |
| 5 | `MsgPlayerInput` | 客户端 -> 服务端 | `PlayerInput` |
| 6 | `MsgSnapshot` | 服务端 -> 客户端 | `Snapshot` |
| 7 | `MsgError` | 服务端 -> 客户端 | `ErrorResp` |
| 11 | `MsgGameStart` | 服务端 -> 客户端 | `GameStart` |
| 12 | `MsgGameOver` | 服务端 -> 客户端 | `GameOver` |
| 13 | `MsgPlayerStatsQuery` | 客户端 -> 服务端 | `PlayerStatsReq` |
| 14 | `MsgPlayerStatsResp` | 服务端 -> 客户端 | `PlayerStatsResp` |

消息号 8、9、10 曾用于旧版批量输入和预测确认消息，已经废弃，客户端不要继续发送，服务端也不会处理。

## 4. 入房协议变化

客户端连接 roomserver 后，第一条业务消息应发送 `MsgJoinRoom`。

`JoinRoomReq` 当前只需要携带 room token：

```proto
message JoinRoomReq {
  string token = 1; // 入房令牌
}
```

旧字段 `sync_version`、`prediction_enabled`、`physics_hash` 已 reserved，客户端不要再依赖这些字段完成入房协商。

入房成功后服务端返回 `JoinRoomResp`，客户端需要重点读取：

| 字段 | 含义 | 客户端用途 |
| --- | --- | --- |
| `status` | 是否入房成功 | false 时按 `content` 或错误处理 |
| `room_id` | 房间 ID | 绑定当前对局 |
| `tick` | 当前服务端房间帧号 | 初始化本地服务端时间基准 |
| `spawn_id` | 出生点 ID | 记录玩家出生点 |
| `x/y/z` | 初始权威位置 | 初始化本地玩家显示位置 |
| `yaw/pitch` | 初始权威视角 | 初始化视角 |
| `tick_rate` | 服务端逻辑帧率 | 建议按该频率或接近该频率发送输入 |
| `snapshot_rate` | 快照频率 | 估算插值间隔 |
| `server_time` | 服务端毫秒时间戳 | 时间同步和延迟估算 |
| `map_id` | 地图 ID | 加载匹配地图资源 |
| `physics_hash` | 服务端物理数据 hash | 校验客户端地图/碰撞版本 |
| `game_duration_seconds` | 单局时长秒数 | UI 倒计时 |
| `game_started` | 对局是否已经开始 | false 时等待 `GameStart` |
| `game_start_tick` | 对局开始 tick | 对局时间基准 |
| `game_end_tick` | 对局结束 tick | 倒计时和结束判断 |

旧字段 `sync_mode`、`rollback_window_ticks`、`future_input_window_ticks`、`prediction_keyframe_interval`、`position_tolerance`、`hard_position_tolerance`、`angle_tolerance` 已 reserved，客户端不要再按旧预测/回滚模式做协议适配。

## 5. 玩家输入协议

入房成功后，客户端只发送 `MsgPlayerInput`：

```proto
message PlayerInput {
  int64 client_tick = 1; // 客户端帧号
  double move_x = 2; // 左右移动输入
  double move_z = 3; // 前后移动输入
  double yaw = 4; // 水平视角
  double pitch = 5; // 垂直视角
  bool fire = 6; // 是否开火
  bool jump = 7; // 是否跳跃
}
```

字段含义：

| 字段 | 服务端处理方式 |
| --- | --- |
| `client_tick` | 仅作为诊断字段，不决定服务端执行 tick |
| `move_x` | 限制到 `[-1, 1]` |
| `move_z` | 限制到 `[-1, 1]` |
| `yaw` | 归一化到 `[-180, 180]` |
| `pitch` | 限制到 `[-89, 89]` |
| `fire` | 只有精确输入帧会触发一次服务端开火判定 |
| `jump` | 只有精确输入帧会触发一次跳跃请求 |

如果 `move_x/move_z` 斜向长度超过 1，服务端会归一化，避免斜向移动更快。

如果字段里出现 NaN 或 Inf，服务端会丢弃该输入。

## 6. 服务端输入排帧策略

服务端不信任客户端 `client_tick` 来推进房间时间。每条输入按服务端收到顺序排到后续可执行 tick：

```text
收到 PlayerInput
  -> 校验玩家已入房且存活
  -> 清洗输入
  -> 选择 r.tick + 1 或 lastQueuedInputTick + 1
  -> 写入该玩家的待执行输入队列
```

单个玩家的未来输入队列窗口为：

```text
MaxInputHoldTicks + 1
```

代码默认 `MaxInputHoldTicks = 8`，如果线上配置覆盖，应以实际配置为准。窗口满时服务端会丢弃后续输入并记录日志，不会给客户端单独确认。

客户端适配建议：

1. 不要突发发送大量未来输入。
2. 建议按 `JoinRoomResp.tick_rate` 的节奏发送输入，例如当前默认 `20Hz`。
3. `client_tick` 可以本地递增，方便日志排查，但不要期待服务端按该 tick 回放。
4. 如果客户端渲染帧率高于服务端 tick，可在渲染层复用最近一次采集输入，但网络发送仍按固定输入频率节流。

## 7. 缺帧处理策略

每个服务端 tick，房间会尝试读取该 tick 的精确输入。

如果当前 tick 没有精确输入，服务端会在 `MaxInputHoldTicks` 范围内沿用上一帧移动输入：

```text
有当前 tick 精确输入：使用该输入
没有当前 tick 精确输入，但上一帧输入未超过 MaxInputHoldTicks：沿用上一帧移动和视角
超过 MaxInputHoldTicks：本 tick 没有移动输入
```

沿用输入时，服务端会强制：

```text
Fire = false
Jump = false
```

这样可以避免网络缺帧导致一次开火或一次跳跃被重复执行。

玩家如果处于空中，即使当前 tick 没有新输入，服务端仍会继续交给物理后端推进垂直速度和落地状态。

## 8. 移动坐标约定

客户端发送的是本地输入方向，不是世界坐标位移。

服务端根据 `yaw` 将本地输入转换为世界方向：

```text
yaw = 0：前方是 +Z，右方是 +X
yaw = 90：前方是 +X，右方是 -Z

worldMove = forward * move_z + right * move_x
```

当前服务端默认移动速度是 `4.0` 单位/秒。默认 `tick_rate = 20` 时，单 tick 最大水平移动距离约为 `0.2` 单位。

最终位置由服务端物理后端计算，包含地图碰撞、玩家胶囊体、重力、跳跃和落地状态。客户端不要自己计算后把结果上传给服务端。

## 9. Snapshot 下发策略

服务端按 `snapshot_rate` 下发权威快照。当前默认 `tick_rate = 20`、`snapshot_rate = 10`，也就是约每 2 个服务端 tick 下发一次快照。

`Snapshot` 结构：

```proto
message Snapshot {
  int64 server_tick = 1; // 服务端帧号
  repeated PlayerState players = 2; // 可见玩家
}
```

`PlayerState` 结构：

```proto
message PlayerState {
  uint64 player_id = 1; // 玩家ID
  double x = 2; // X坐标
  double y = 3; // Y坐标
  double z = 4; // Z坐标
  double yaw = 5; // 水平视角
  double pitch = 6; // 垂直视角
  int32 hp = 7; // 生命值
  string spawn_id = 8; // 出生点ID
  int32 kill_count = 9; // 击杀数量
  int32 death_count = 10; // 死亡数量
  bool invincible = 11; // 是否无敌
  int64 invincible_until_tick = 12; // 无敌结束帧号
}
```

每个客户端收到的 `Snapshot.players` 不一定是全房间玩家列表。服务端会按接收者生成快照：

```text
players = [接收者自己] + AOI 可见的其他玩家
```

当前简化 AOI 至少保证快照包含接收者自己；其他玩家会按存活状态和距离过滤，当前默认可见距离为 `80` 单位。

客户端适配建议：

1. 对每个玩家按 `server_tick` 缓存快照。
2. 丢弃比当前已处理 tick 更旧的快照。
3. 本地玩家也以 Snapshot 纠正最终位置。
4. 远端玩家使用 Snapshot 插值显示，避免 10Hz 快照直接跳变。
5. 如果某个玩家本次快照没有出现，不要立刻判定其离房，可能只是 AOI 不可见。
6. 如果玩家位置发生明显大跳变，或 `spawn_id`、`hp`、`invincible` 出现复活特征，应清理该玩家旧插值/预测缓存，直接切到新权威状态。

服务端每个连接的快照发送队列只有 1 个槽位。慢连接会丢弃旧快照，只保留最新快照。因此客户端必须能接受快照 tick 不连续。

## 10. 开火、死亡和复活

开火命中完全由服务端结算：

```text
精确输入 Fire=true
  -> 服务端用当前权威位置和视角做 Raycast
  -> 命中有效存活目标
  -> 扣血
  -> HP 归零时增加击杀/死亡计数
  -> 目标在原出生点复活并获得短暂无敌
```

当前服务端简化规则：

| 配置 | 当前值 |
| --- | --- |
| 玩家默认 HP | `100` |
| 单次开火伤害 | `20` |
| 开火最大距离 | `100` |
| 开火视点高度 | `0.9` |
| 复活无敌时间 | `5s` |

复活后，服务端会清理该玩家尚未执行的未来输入，避免死亡前排队的旧输入继续影响复活后的状态。

客户端需要从 Snapshot 的 `hp`、`kill_count`、`death_count`、`invincible`、`invincible_until_tick` 直接刷新 UI 和表现。当前没有单独的受击事件协议，命中和复活表现需要先从快照状态变化推导。

## 11. 对局开始和结束

房间人数达到上限后，服务端会关闭入房并广播 `MsgGameStart`：

```proto
message GameStart {
  string room_id = 1;
  int64 server_tick = 2;
  int64 start_tick = 3;
  int64 end_tick = 4;
  int64 duration_seconds = 5;
  int64 server_time = 6;
}
```

默认单局时长是 `3m`。达到结束 tick 后，服务端广播 `MsgGameOver`：

```proto
message GameOver {
  string room_id = 1;
  int64 server_tick = 2;
  int64 start_tick = 3;
  int64 end_tick = 4;
  string reason = 5;
  int64 server_time = 6;
  repeated PlayerState players = 7;
}
```

当前结束原因主要是：

```text
time_limit
```

客户端收到 `GameOver` 后应停止发送对局输入，展示结算状态，并以 `GameOver.players` 作为结束时状态。

## 12. 心跳和时间同步

客户端可以发送 `MsgHeartbeat`：

```proto
message Heartbeat {
  int64 client_time = 1;
  int64 server_time = 2;
  int64 server_tick = 3;
}
```

服务端响应 `MsgHeartbeatAck` 时会回填：

| 字段 | 含义 |
| --- | --- |
| `client_time` | 原样返回客户端时间 |
| `server_time` | 服务端当前毫秒时间戳 |
| `server_tick` | 玩家所在房间当前 tick，未入房时为 0 |

客户端可以用心跳估算 RTT、服务端时间偏移和当前 server tick，但心跳不参与服务端权威模拟。

## 13. 错误响应

服务端错误统一下发 `MsgError`：

```proto
message ErrorResp {
  string code = 1;
  string content = 2;
}
```

常见错误码：

| code | 场景 |
| --- | --- |
| `bad_request` | protobuf 解码失败或请求格式错误 |
| `invalid_token` | room token 无效 |
| `server_mismatch` | token 绑定的 server_id 与当前 roomserver 不一致 |
| `join_failed` | 入房失败，例如房间满、对局已开始、出生点不可用 |
| `not_joined` | 未入房就发送输入或查询 |
| `input_failed` | 输入投递到房间失败 |
| `stats_failed` | 战绩查询失败 |
| `unknown_message` | 未知消息号 |

## 14. 客户端推荐处理流程

```text
连接 KCP
  -> 发送 MsgJoinRoom(JoinRoomReq{token})
  -> 等待 MsgJoinRoomAck
  -> 如果 status=false，展示失败并断开或返回匹配
  -> 如果 status=true：
       记录 room_id / tick_rate / snapshot_rate / map_id / physics_hash
       用 x/y/z/yaw/pitch 初始化本地玩家
       校验或加载地图资源
       启动固定频率输入发送
       启动消息接收循环

输入发送循环：
  -> 按 tick_rate 采样当前控制输入
  -> 发送 MsgPlayerInput(client_tick, move_x, move_z, yaw, pitch, fire, jump)

消息接收循环：
  -> MsgSnapshot：按 server_tick 更新权威状态缓存
  -> MsgHeartbeatAck：更新延迟和 server tick 估算
  -> MsgGameStart：开始倒计时和战斗 UI
  -> MsgGameOver：停止输入并展示结算
  -> MsgError：按 code 处理错误
```

## 15. 客户端必须删除或调整的旧逻辑

1. 删除“上传本地坐标/速度/完整玩家状态”的逻辑。
2. 删除对消息号 8、9、10 的依赖。
3. 删除对 `JoinRoomReq.sync_version`、`JoinRoomReq.prediction_enabled`、`JoinRoomReq.physics_hash` 的依赖。
4. 删除对 `JoinRoomResp.sync_mode`、`rollback_window_ticks`、`future_input_window_ticks`、`prediction_keyframe_interval`、`position_tolerance`、`hard_position_tolerance`、`angle_tolerance` 的依赖。
5. 不要用 `client_tick` 要求服务端按客户端历史帧回滚或确认。
6. 不要把本地预测位置当作权威位置。
7. 快照 tick 可能不连续，客户端不要要求每个服务端 tick 都有 Snapshot。
8. `Snapshot.players` 不是全量房间玩家列表，不能用“某玩家没出现在一次快照里”直接判定离房。

## 16. 当前服务端默认参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `listen_addr` | `:9001` | KCP 监听地址 |
| `tick_rate` | `20` | 房间逻辑帧率 |
| `snapshot_rate` | `10` | 状态快照频率 |
| `max_players_per_room` | `2` | 当前单房间人数上限 |
| `default_map_id` | `mfps_arena` | 默认地图 |
| `max_input_hold_ticks` | `8` | 缺帧时移动输入最大沿用帧数 |
| `game_duration` | `3m` | 单局时长 |
| `player_capsule_radius` | `0.35` | 玩家胶囊体半径 |
| `player_capsule_height` | `1.8` | 玩家胶囊体高度 |

如果部署配置覆盖这些值，客户端应优先使用 `JoinRoomResp` 和实际服务端配置返回的信息。

## 17. 相关代码位置

| 内容 | 文件 |
| --- | --- |
| room protobuf 协议 | `pb/room/room.proto` |
| KCP 消息号和帧格式 | `src/roomserver/protocol/message.go` |
| KCP 服务和消息分发 | `src/roomserver/service/server.go` |
| 单连接发送队列和快照覆盖策略 | `src/roomserver/service/session.go` |
| 房间 tick、输入排帧、快照广播 | `src/roomserver/logic/room.go` |
| 输入清洗和移动方向计算 | `src/roomserver/logic/movement.go` |
| 同步配置和玩家同步状态 | `src/roomserver/logic/sync.go` |

## 18. 一句话给客户端

现在客户端只负责发输入，服务端负责算最终状态；客户端收到 Snapshot 后按 `server_tick` 更新显示，本地预测只能作为表现层优化，不能再作为协议状态上传或要求服务端确认。
