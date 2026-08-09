# 阶段四：服务端权威移动和状态同步

本阶段目标：看懂 roomserver 为什么只信输入、不信客户端坐标，以及纯服务端权威状态同步链路如何落到代码上。

## 1. 总体思路

当前设计是：

```text
客户端：采集输入并发送给服务端
服务端：接收输入、校验输入、按服务端 tick 排帧、调用物理后端模拟、广播 Snapshot
客户端：只按服务端 Snapshot 更新显示状态
```

服务端不会把客户端上报的 `x/y/z` 当成最终坐标。客户端协议里也不再上传本地状态。

关键代码：

- 输入清洗：[../logic/movement.go](../logic/movement.go)
- 同步状态：[../logic/sync.go](../logic/sync.go)
- 房间更新和快照：[../logic/room.go](../logic/room.go)
- 协议字段：[../../../pb/room/room.proto](../../../pb/room/room.proto)

## 2. SyncConfig 字段说明

结构在 [../logic/sync.go](../logic/sync.go)。配置来源是 [../config/config.go](../config/config.go)，在 [../service/server.go](../service/server.go) `Start` 中组装后传给 RoomManager。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `MaxInputHoldTicks` | `int64` | 缺少当前帧输入时，上一帧移动输入最多沿用多少帧 |

`Normalize` 会补默认值。`MaxInputHoldTicks` 默认是 8。

## 3. playerSyncState 字段说明

结构在 [../logic/sync.go](../logic/sync.go)。这是每个玩家的同步运行时状态。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `inputs` | `map[int64]authoritativeInput` | 服务端已接受但还未应用的输入，key 是服务端执行 tick |
| `lastInput` | `authoritativeInput` | 最近一次应用过的输入，用于短时间缺帧沿用 |
| `hasLastInput` | `bool` | 是否有可沿用的上一帧输入 |
| `lastInputTick` | `int64` | `lastInput` 对应的服务端 tick |
| `lastAppliedTick` | `int64` | 服务端已经应用到的 tick |
| `lastQueuedInputTick` | `int64` | 已排队的最后一个输入执行 tick |

这个结构只在房间 loop 内使用，所以不需要单独加锁。

## 4. authoritativeInput 字段说明

结构在 [../logic/movement.go](../logic/movement.go)。它是服务端清洗后的输入。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `ClientTick` | `int64` | 服务端实际执行 tick，清洗后会覆盖为 executeTick |
| `MoveX` | `float64` | 清洗后的左右输入，范围 `[-1, 1]`，斜向会归一化 |
| `MoveZ` | `float64` | 清洗后的前后输入，范围 `[-1, 1]`，斜向会归一化 |
| `Yaw` | `float64` | 归一化后的水平视角，范围 `[-180, 180]` |
| `Pitch` | `float64` | 限制后的垂直视角，范围 `[-89, 89]` |
| `Fire` | `bool` | 当前帧是否请求开火 |
| `Jump` | `bool` | 当前帧是否请求跳跃 |

`sanitizePlayerInput` 会拒绝 nil、NaN 和 Inf，防止异常浮点数污染服务端物理计算。

## 5. 输入接收流程

客户端只发送 `MsgPlayerInput`。服务端入口是 [../service/server.go](../service/server.go) `handlePlayerInput`。

```text
检查 session.PlayerID != 0
  -> DecodeProto(roompb.PlayerInput)
  -> manager.PushInput
  -> room.PushInput
  -> Room.loop 中 handleInput
```

## 6. handleInput 做什么

函数在 [../logic/room.go](../logic/room.go)。核心逻辑：

```text
检查房间未结束
检查玩家存在且存活
sanitizePlayerInput
nextAvailableInputTick 按服务端收到顺序找后续执行 tick
acceptInput 写入 syncState.inputs[executeTick]
```

客户端 `client_tick` 只作为诊断字段，不决定服务端时间。服务端不会根据客户端 tick 历史状态，也不会按客户端 tick 判断 stale/future 输入。

为了避免输入无限排到未来，单个玩家可排队窗口由 `MaxInputHoldTicks + 1` 限制。窗口满时输入会被丢弃并记录日志。

## 7. updatePlayers 如何推进权威状态

每个服务端 tick，`Room.update` 调用 [../logic/room.go](../logic/room.go) `updatePlayers`。

```text
遍历 r.players
  -> clearExpiredInvincibility
  -> ensureSyncState
  -> inputForTick(syncState, r.tick)
  -> 有当前帧输入、可沿用上一帧输入或玩家处于空中时，simulatePlayerTick
  -> syncState.lastAppliedTick = r.tick
  -> cleanupSyncState
```

这里有两个重点：

- 服务端按自己的 `r.tick` 推进，不由客户端 tick 驱动房间时间。
- 服务端只保存待执行输入和上一帧输入，不保存预测历史。

## 8. inputForTick 的缺帧处理

函数在 [../logic/room.go](../logic/room.go)。

逻辑：

```text
如果 syncState.inputs[tick] 存在：
  -> 使用精确输入
  -> 记录 lastInput / lastInputTick
否则如果有 lastInput 且 tick - lastInputTick <= MaxInputHoldTicks：
  -> 沿用 lastInput，但 ClientTick 改成当前 tick，Fire 和 Jump 强制 false
否则：
  -> 当前 tick 无输入
```

为什么 `Fire` 和 `Jump` 沿用时强制 false：避免网络缺帧导致一次开火或跳跃输入被服务端重复当成多次触发。

## 9. simulatePlayerTick 如何移动玩家

函数在 [../logic/room.go](../logic/room.go)。

```text
applyViewRotation
  -> buildMovePlayerRequest
  -> physics.MovePlayer
  -> 把物理结果写回 player.X/Y/Z
  -> 如果精确输入里 Fire=true，执行 Raycast
```

`buildMovePlayerRequest` 在 [../logic/movement.go](../logic/movement.go)：

```text
movementDirection(input.Yaw, input.MoveX, input.MoveZ)
  -> 根据 yaw 把本地输入转换成世界方向
Distance = defaultPlayerMoveSpeed * (1 / tickRate)
DeltaTime = 1 / tickRate
Jump = 当前帧跳跃请求
VerticalVelocity/Grounded = 玩家上一帧垂直状态
```

当前默认移动速度是 `4.0` 单位/秒，20 tick 下每 tick 最大移动距离是 `0.2`。

## 10. movementDirection 坐标关系

函数在 [../logic/movement.go](../logic/movement.go)。

```text
yaw = 0：forward 是 +Z，right 是 +X
yaw = 90：forward 是 +X，right 是 -Z
```

计算公式：

```text
worldMove = forward * moveZ + right * moveX
```

这意味着客户端只需要发送“本地输入方向”和视角，服务端自己换算成世界移动方向。

## 11. Snapshot 广播

快照广播在 [../logic/room.go](../logic/room.go) `broadcastSnapshots`。

```text
Room.update
  -> 到达 snapshotRate 间隔
  -> broadcastSnapshots
  -> AOIFilter.FilterVisible
  -> protocol.NewProtoMessage(MsgSnapshot, roompb.Snapshot)
  -> player.Session.SendSnapshot
```

每份快照至少包含接收者自己，也会包含 AOI 可见的其他玩家。`Session.SendSnapshot` 使用单槽队列，慢连接会丢弃旧快照保留最新快照。

## 12. 为什么这套设计能防很多移动外挂

因为服务端不接受“我现在在这里”作为真相。客户端能影响服务端的主要是：

```text
move_x / move_z / yaw / pitch / fire / jump
```

服务端会做：

- 输入范围限制
- 斜向归一化
- 视角范围限制
- 按服务端收到顺序排帧
- 固定 tick 推进
- 物理后端碰撞计算
- 权威 Snapshot 下发

如果客户端伪造坐标，服务端协议不会读取这些坐标，无法直接改写 `player.X/Y/Z`。
