# 阶段三：RoomManager、Room 和房间固定帧循环

本阶段目标：看懂玩家如何进入房间、房间如何被创建、为什么房间状态由单个 loop 串行推进。

## 1. 总调用链

从网络消息到房间业务的链路是：

```text
Server.HandleMessage
  -> handleJoinRoom / handlePlayerInput
  -> RoomManager.JoinRoom / PushInput
  -> Room.Join / PushEvent
  -> Room.loop 从事件队列取事件
  -> handleJoinEvent / handleInput / update
```

对应代码：

- 网络分发：[../service/server.go](../service/server.go)
- 房间管理：[../logic/room_manager.go](../logic/room_manager.go)
- 房间循环：[../logic/room.go](../logic/room.go)
- 玩家状态：[../logic/player.go](../logic/player.go)

## 2. RoomManager 职责

`RoomManager` 是“房间索引和创建器”，不是房间逻辑本体。结构在 [../logic/room_manager.go](../logic/room_manager.go)。

它负责：

- 保存 `roomID -> *Room` 映射
- 保存 `playerID -> roomID` 映射
- 玩家入房时按需创建房间
- 玩家离线时找到房间并投递离开事件
- 输入到达时找到玩家所在房间并投递输入事件
- 停服时停止所有房间

## 3. RoomManager 字段说明

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `ctx` | `context.Context` | roomserver 生命周期 context，新建 Room 时传给 `room.Start` |
| `mu` | `sync.RWMutex` | 保护 `rooms` 和 `playerRooms` 两个 map |
| `rooms` | `map[string]*Room` | 房间表，key 是 roomID |
| `playerRooms` | `map[uint64]string` | 玩家所在房间表，key 是 playerID，value 是 roomID |
| `maxRooms` | `int` | 当前进程最大房间数 |
| `maxPlayersPerRoom` | `int` | 新建房间的最大玩家数 |
| `tickRate` | `int` | 新建房间的逻辑帧率 |
| `snapshotRate` | `int` | 新建房间的快照发送频率 |
| `syncConfig` | `SyncConfig` | 弱网输入沿用和未来排队窗口配置 |
| `mapID` | `string` | 新建房间使用的地图 ID，会下发给客户端 |
| `physicsHash` | `string` | 服务端物理数据 hash，会下发给客户端 |
| `gameDuration` | `time.Duration` | 单局对局时长 |
| `aoi` | `AOIFilter` | AOI 过滤器，决定快照里哪些玩家对当前玩家可见 |
| `physicsFactory` | `PhysicsWorldFactory` | 物理世界工厂，每个房间创建一个独立物理 world |

## 4. RoomManager 方法说明

| 方法 | 作用 | 关键点 |
| --- | --- | --- |
| `NewRoomManager` | 创建默认同步配置的管理器 | 内部调用 `NewRoomManagerWithSync` |
| `NewRoomManagerWithSync` | 创建带同步参数的管理器 | 补齐 maxRooms、maxPlayers、tickRate、snapshotRate、physicsFactory 默认值 |
| `JoinRoom` | 玩家加入房间 | 先 `getOrCreateRoom`，再向房间投递 join 事件，最后记录 `playerRooms` |
| `LeaveRoom` | 玩家离开房间 | 删除 `playerRooms` 映射，再向房间投递 leave 事件 |
| `PushInput` | 投递单帧输入 | 查玩家所在房间，然后调用 `room.PushInput` |
| `RoomTick` | 查询玩家所在房间当前帧号 | 心跳响应里用来返回 `server_tick` |
| `QueryPlayerStats` | 查询同房间玩家战绩 | 通过房间事件队列串行读取玩家状态 |
| `Stop` | 停止所有房间 | 复制 rooms 列表后逐个 `room.Stop` |
| `playerRoom` | 根据 playerID 找房间 | 找不到会返回 `player room not found` |
| `getOrCreateRoom` | 获取或创建房间 | 超过 `maxRooms` 返回 `ErrRoomLimitReached`；每个房间独立创建物理 world |

`getOrCreateRoom` 是关键函数：

```text
先读锁查 rooms
  -> 不存在则写锁二次检查
  -> 检查房间数量上限
  -> physicsFactory.NewWorld(roomID)
  -> NewRoomWithOptions
  -> room.Start
  -> 保存到 rooms
```

这个设计保证同一个 roomID 不会被并发创建两次。

## 5. Room 职责

`Room` 是单局房间的状态机。结构在 [../logic/room.go](../logic/room.go)。

它负责：

- 维护房间玩家状态
- 维护玩家待执行输入
- 固定 tick 推进权威状态
- 调用物理后端移动玩家和 raycast
- 按快照频率发送 snapshot
- 处理入房、离房、输入事件
- 处理对局开始、限时结束、开火、复活和战绩查询

房间状态不暴露给多个 goroutine 直接改。外部只能通过 `Join`、`Leave`、`PushInput` 投递事件，真正修改发生在 `Room.loop` 所在 goroutine。

## 6. Room 字段说明

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `id` | `string` | 房间 ID |
| `maxPlayers` | `int` | 房间最大玩家数 |
| `tickRate` | `int` | 房间逻辑帧率，每秒 tick 次数 |
| `snapshotRate` | `int` | 每秒发送快照次数 |
| `syncConfig` | `SyncConfig` | 弱网输入沿用和未来排队窗口配置 |
| `mapID` | `string` | 当前房间地图 ID，下发给客户端 |
| `physicsHash` | `string` | 服务端物理数据 hash，下发给客户端 |
| `gameStarted` | `bool` | 对局是否已开始 |
| `gameEnded` | `bool` | 对局是否已结束 |
| `gameStartTick` | `int64` | 对局开始帧号 |
| `gameEndTick` | `int64` | 对局结束帧号 |
| `currentTick` | `atomic.Int64` | 对外可读的当前房间帧号，用于心跳查询 |
| `aoi` | `AOIFilter` | 快照可见性过滤器 |
| `physics` | `PhysicsWorld` | 房间独立物理世界，负责碰撞、移动、射线 |
| `events` | `chan roomEvent` | 房间事件队列，当前容量 256 |
| `stop` | `chan struct{}` | 房间停止信号 |
| `players` | `map[uint64]*Player` | 房间内玩家状态表 |
| `syncStates` | `map[uint64]*playerSyncState` | 每个玩家的待执行输入和上一帧输入 |
| `tick` | `int64` | 房间 loop 内部使用的当前帧号 |
| `lastSnapshotAt` | `int64` | 上一次广播快照的帧号 |

## 7. roomEvent 字段说明

`roomEvent` 是外部 goroutine 和房间 loop 之间的消息。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `typeID` | `roomEventType` | 事件类型：join、leave、input、statsQuery |
| `player` | `*Player` | 入房事件携带的玩家对象 |
| `playerID` | `uint64` | 离房、输入和查询事件携带的玩家 ID |
| `targetID` | `uint64` | 战绩查询目标玩家 ID |
| `input` | `*roompb.PlayerInput` | 单帧输入 |
| `statsResp` | `chan playerStatsQueryResult` | 战绩查询响应通道 |
| `joinReserved` | `bool` | 是否已在 Join 阶段预占入房名额 |

事件类型：

| 常量 | 含义 |
| --- | --- |
| `roomEventJoin` | 玩家加入 |
| `roomEventLeave` | 玩家离开 |
| `roomEventInput` | 单帧输入 |
| `roomEventPlayerStatsQuery` | 玩家战绩查询 |

## 8. Player 字段说明

结构在 [../logic/player.go](../logic/player.go)。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `ID` | `uint64` | 玩家 ID，来自入房 token |
| `RoomID` | `string` | 玩家所在房间 ID |
| `X` | `float64` | 服务端权威 X 坐标 |
| `Y` | `float64` | 服务端权威 Y 坐标 |
| `Z` | `float64` | 服务端权威 Z 坐标 |
| `Yaw` | `float64` | 服务端认可的水平视角 |
| `Pitch` | `float64` | 服务端认可的垂直视角 |
| `HP` | `int` | 生命值，当前入房默认 100 |
| `KillCount` | `int` | 击杀数量 |
| `DeathCount` | `int` | 死亡数量 |
| `SpawnID` | `string` | 占用的出生点 ID，比如 `spawn_a` 或 `spawn_b` |
| `Session` | `Session` | logic 层依赖的连接抽象，用来发送消息 |
| `Alive` | `bool` | 玩家是否存活 |
| `InvincibleUntilTick` | `int64` | 无敌结束帧号 |
| `VerticalVelocity` | `float64` | 垂直速度 |
| `Grounded` | `bool` | 是否处于地面 |

`ToState` 会把 `Player` 转成协议层 `PlayerState`，用于 snapshot 和 game over。

## 9. 入房流程

网络入口是 [../service/server.go](../service/server.go) `handleJoinRoom`：

```text
DecodeProto(roompb.JoinRoomReq)
  -> ParseRoomToken
  -> 校验 ServerID / RoomID / PlayerID
  -> session.SetPlayer
  -> 构造 logic.Player
  -> manager.JoinRoom
```

房间内真正入房逻辑在 [../logic/room.go](../logic/room.go) `handleJoinEvent`：

```text
player nil 检查
  -> 房间是否已开始或结束检查
  -> 房间满员检查
  -> 重复入房检查
  -> nextSpawnPoint 选择出生点
  -> 写入 Player 初始状态
  -> physics.AddPlayer 创建物理玩家
  -> 写入 players 和 syncStates
  -> 发送 JoinRoomAck
```

这里顺序很重要：先创建物理玩家，成功后才写入 `r.players`。否则会出现 logic 里有玩家、物理世界里没有玩家的状态不一致。

## 10. 出生点分配

函数是 [../logic/room.go](../logic/room.go) `nextSpawnPoint`。

逻辑：

```text
spawnPoints := r.physics.SpawnPoints()
  -> 收集当前 players 已占用的 SpawnID
  -> 按 spawnPoints 顺序找第一个未占用的点
  -> 找不到返回 false
```

当前 PhysX 后端从地图 collision JSON 的 `spawn_points` 读取出生点；Simple 后端返回默认 `spawn_a` 和 `spawn_b`。

## 11. 固定帧循环

`Room.Start` 会启动 `go r.loop(ctx)`。

`loop` 的核心 select：

```text
ctx.Done    -> 停止房间
r.stop      -> 停止房间
events      -> handleEvent
ticker.C    -> update
```

`ticker` 间隔由 `tickRate` 决定。如果 `tickRate = 20`，就是每 50ms 执行一次 `update`。

## 12. update 做什么

代码在 [../logic/room.go](../logic/room.go) `update`。

```text
r.tick++
currentTick.Store(r.tick)
updatePlayers
到达 snapshotRate 间隔时 broadcastSnapshots
```

`snapshotRate` 和 `tickRate` 的关系：

```text
intervalTicks = tickRate / snapshotRate
```

当前默认 `tickRate=20`、`snapshotRate=10`，所以每 2 个服务端 tick 广播一次快照。

## 13. 离房流程

外部调用 `RoomManager.LeaveRoom(playerID)`，它会删除 `playerRooms` 映射并向房间投递 leave 事件。

房间内 [../logic/room.go](../logic/room.go) `handleLeave` 做：

```text
检查玩家是否存在
  -> delete players
  -> delete syncStates
  -> physics.RemovePlayer
  -> 记录日志
```

离房后出生点会被释放，因为 `nextSpawnPoint` 只根据当前 `players` 里的 `SpawnID` 判断占用。

## 14. AOI 快照

AOI 代码在 [../logic/aoi.go](../logic/aoi.go)。当前是简化 AOI。

`broadcastSnapshots` 对每个玩家单独生成一份快照：

```text
states = append(states, player.ToStateAt(r.tick))
visible := r.aoi.FilterVisible(player, players)
for visiblePlayer in visible:
    states = append(states, visiblePlayer.ToStateAt(r.tick))
```

也就是说快照里一定包含自己，然后才是当前玩家可见的其他玩家。
