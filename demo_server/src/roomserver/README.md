# Roomserver 全链路说明

本文档说明当前 `src/roomserver` 的完整链路：客户端通过 KCP 入房，服务端只接收输入，房间固定 tick 推进权威状态，并按快照频率下发状态同步。

当前 roomserver 已接入 KCP、room token、logicserver 到 matchserver 再到 roomserver 的入房链路、房间 tick、AOI、PhysX 地图碰撞、出生点同步、服务端权威移动、开火命中、死亡复活和战绩查询。旧版本地模拟确认能力和对应消息已移除。

## 1. 当前代码包含什么

目录结构：

```text
src/roomserver
├── cmd
│   └── main.go              # roomserver 进程入口
├── config
│   └── config.go            # roomserver 默认配置
├── service
│   ├── server.go            # KCP 服务、消息分发、入房处理
│   └── session.go           # 单个客户端连接会话
├── logic
│   ├── room_manager.go      # 房间管理器
│   ├── room.go              # 单房间 tick 循环
│   ├── player.go            # 玩家状态和 session 抽象
│   ├── sync.go              # 输入排帧和状态同步配置
│   ├── movement.go          # 输入清洗和移动请求构造
│   ├── aoi.go               # AOI 可见性过滤接口和简化实现
│   └── physics.go           # 物理接口和简化实现
└── protocol
    ├── message.go           # KCP 业务帧、消息类型、protobuf payload 编解码
    └── token.go             # room token 签发和校验
```

协议定义：

```text
pb/room/room.proto           # roomserver protobuf 协议
```

配置示例：

```yaml
room_server_01:
  server_id: "room-01"
  listen_addr: ":9001"
  token_secret: "room-token-secret"
  max_rooms: 1000
  max_players_per_room: 2
  tick_rate: 20
  snapshot_rate: 10
  read_timeout: "10s"
  write_queue_size: 128
  max_payload_size: 65536
  physics_backend: "physx"
  player_capsule_radius: 0.35
  player_capsule_height: 1.8
  physics_ground_plane: true
  physx_pvd_enabled: false
  physx_pvd_host: "127.0.0.1"
  physx_pvd_port: 5425
  physx_pvd_timeout_ms: 100
  default_map_id: "mfps_arena"
  map_collision_path: "config/maps/mfps_arena/collision.json"
  physics_hash: "sha256:70921a6cda71319a1bb4e203d23cc60dd09b42854bd5a3785ff892e2ec9387d8"
  max_input_hold_ticks: 3
  game_duration: "3m"
```

注意：`src/roomserver/cmd/main.go` 当前仍使用 `roomconfig.DefaultConfig()` 启动，`config/config.yaml` 已补齐 roomserver 配置示例，后续需要接入统一 YAML 加载器。

## 2. 服务启动链路

启动入口在 `src/roomserver/cmd/main.go`。

```text
main
  -> 初始化 glog 日志
  -> 创建可取消 context
  -> 读取默认 roomserver 配置
  -> service.NewServer(cfg)
  -> server.Start(ctx)
  -> 等待退出信号
  -> server.Stop(...)
```

`server.Start(ctx)` 在 `src/roomserver/service/server.go` 中完成这些事：

1. 校验并加载地图碰撞元数据
2. 根据配置创建 PhysX 或 Simple 物理世界工厂
3. 创建 `RoomManager`
4. 调用 `kcp.ListenWithOptions` 监听 KCP
5. 设置 KCP socket 读写缓冲
6. 启动 `acceptLoop` 等待客户端连接

## 3. 玩家接入链路

玩家真正连接 roomserver 前，正常完整链路应该是：

```text
玩家客户端
  -> logicserver 登录
  -> logicserver 请求 matchserver
  -> matchserver 分配 roomserver 和 room_id
  -> matchserver 签发 room token
  -> logicserver 把 room token 返回给客户端
  -> 客户端用 room token 连接 roomserver
```

room token 的声明结构在 `src/roomserver/protocol/token.go`：

```go
type RoomTokenClaims struct {
    PlayerID uint64
    RoomID   string
    ServerID string
    MatchID  string
    Nonce    string
    jwt.StandardClaims
}
```

room token 证明“这个玩家被允许进入这个 roomserver 的这个房间”，并绑定 `server_id` 和 `room_id`，避免拿到其他服务器乱用。

## 4. KCP 业务帧和 protobuf payload

KCP 当前开启 stream mode，所以业务层自行定义消息边界：

```text
uint16 message_type      # 2 字节，大端序
uint32 payload_length    # 4 字节，大端序
bytes  payload           # protobuf payload
```

`protocol.ReadMessage` 读取帧头和 payload，`protocol.WriteMessage` 写出同样格式。业务 payload 使用 `pb/room/room.proto` 生成的 `roompb` 类型，通过：

```go
protocol.NewProtoMessage(messageType, &roompb.Xxx{})
protocol.DecodeProto(message, &roompb.Xxx{})
```

当前消息类型：

| ID | 名称 | 方向 | payload |
| ---: | --- | --- | --- |
| 1 | `MsgJoinRoom` | 客户端 -> 服务端 | `roompb.JoinRoomReq` |
| 2 | `MsgJoinRoomAck` | 服务端 -> 客户端 | `roompb.JoinRoomResp` |
| 3 | `MsgHeartbeat` | 客户端 -> 服务端 | `roompb.Heartbeat` |
| 4 | `MsgHeartbeatAck` | 服务端 -> 客户端 | `roompb.Heartbeat` |
| 5 | `MsgPlayerInput` | 客户端 -> 服务端 | `roompb.PlayerInput` |
| 6 | `MsgSnapshot` | 服务端 -> 客户端 | `roompb.Snapshot` |
| 7 | `MsgError` | 服务端 -> 客户端 | `roompb.ErrorResp` |
| 11 | `MsgGameStart` | 服务端 -> 客户端 | `roompb.GameStart` |
| 12 | `MsgGameOver` | 服务端 -> 客户端 | `roompb.GameOver` |
| 13 | `MsgPlayerStatsQuery` | 客户端 -> 服务端 | `roompb.PlayerStatsReq` |
| 14 | `MsgPlayerStatsResp` | 服务端 -> 客户端 | `roompb.PlayerStatsResp` |

消息号 8、9 和 10 曾用于旧版输入和预测确认消息，已经废弃且不可复用。

## 5. 入房流程

客户端连接成功后，第一条关键业务消息应该是 `MsgJoinRoom`，payload 只需要携带 room token：

```proto
JoinRoomReq {
  token: "room token"
}
```

服务端处理入口是 `Server.handleJoinRoom`：

```text
DecodeProto(roompb.JoinRoomReq)
  -> ParseRoomToken
  -> 校验 ServerID / RoomID / PlayerID
  -> session.SetPlayer
  -> 构造 logic.Player
  -> manager.JoinRoom
```

房间内入房逻辑在 `Room.handleJoinEvent`：

```text
检查房间是否已开始或已结束
  -> 检查房间人数和重复入房
  -> nextSpawnPoint 选择出生点
  -> 初始化 Player 权威状态
  -> physics.AddPlayer 创建物理玩家
  -> 写入 players 和 syncStates
  -> 发送 JoinRoomAck
```

`JoinRoomResp` 会返回出生点、初始位置、tickRate、snapshotRate、serverTime、mapID、physicsHash 和对局时间信息。

## 6. 纯服务端权威同步

当前同步模型是：

```text
客户端：采集输入并发送给服务端
服务端：清洗输入、按服务端 tick 排帧、调用物理后端推进权威状态、下发 Snapshot
客户端：只按 Snapshot 更新本地显示状态
```

服务端不会接收客户端坐标，也不会要求客户端上传本地状态。客户端本地玩家和其他玩家都应以 `MsgSnapshot` 中的 `PlayerState` 为准。为了降低 10Hz 快照的显示抖动，客户端可以做插值显示，但不能用插值结果反向影响服务端状态。

## 7. 玩家输入链路

客户端入房后只发送 `MsgPlayerInput`。

`PlayerInput` 字段：

| 字段 | 含义 |
| --- | --- |
| `client_tick` | 客户端本地帧号，仅用于诊断，不驱动服务端时间 |
| `move_x` | 左右移动输入，服务端限制到 `[-1, 1]` |
| `move_z` | 前后移动输入，服务端限制到 `[-1, 1]` |
| `yaw` | 水平视角，服务端归一化 |
| `pitch` | 垂直视角，服务端限制到 `[-89, 89]` |
| `fire` | 是否请求开火 |
| `jump` | 是否请求跳跃 |

单帧输入会按服务端收到顺序排到后续可执行 tick。`client_tick` 仅用于诊断，不驱动服务端时间。服务端使用 `max_input_hold_ticks + 1` 作为未来输入排队窗口，窗口满时会丢弃输入并记录日志。

服务端输入处理链路：

```text
Server.handlePlayerInput
  -> DecodeProto(roompb.PlayerInput)
  -> RoomManager.PushInput
  -> Room.PushInput
  -> Room.loop 处理输入事件
  -> sanitizePlayerInput
  -> nextAvailableInputTick
  -> syncState.inputs[executeTick] = authoritativeInput
```

## 8. 房间 tick 和权威移动

每个房间由自己的 goroutine 串行处理事件和 tick：

```text
Room.loop
  -> room events: join / leave / input
  -> ticker.C: update
```

`Room.update` 每 tick 做：

```text
r.tick++
currentTick.Store(r.tick)
updatePlayers
按 snapshotRate 判断是否广播 Snapshot
```

`updatePlayers` 对每个存活玩家：

```text
clearExpiredInvincibility
  -> inputForTick(syncState, r.tick)
  -> simulatePlayerTick
  -> cleanupSyncState
```

`simulatePlayerTick` 只使用服务端清洗后的输入：

```text
applyViewRotation
  -> buildMovePlayerRequest
  -> physics.MovePlayer
  -> 将物理结果写回 Player.X/Y/Z、VerticalVelocity、Grounded
  -> 精确输入 Fire=true 时执行服务端 raycast 命中
```

如果当前 tick 没收到新输入，服务端可以在 `MaxInputHoldTicks` 范围内沿用上一帧移动输入，但会强制 `Fire=false`、`Jump=false`，避免网络缺帧导致重复开火或重复跳跃。

## 9. Snapshot 广播

`Room.broadcastSnapshots` 对每个玩家单独生成快照：

```text
players := 当前房间所有玩家
visible := AOI.FilterVisible(receiver, players)
states := [receiver 自己] + visible
NewProtoMessage(MsgSnapshot, roompb.Snapshot{...})
receiver.Session.SendSnapshot(message)
```

`Session.SendSnapshot` 使用单槽快照队列。队列满时会丢弃旧快照保留最新快照，避免慢连接堆积过期状态。

`Snapshot` 至少包含接收者自己，也会包含 AOI 可见的其他玩家。

## 10. 开火、死亡和复活

开火命中完全由服务端结算：

```text
精确输入 Fire=true
  -> PhysicsWorld.Raycast
  -> 命中有效存活目标
  -> applyFireDamage
  -> HP 归零时增加击杀/死亡计数
  -> respawnPlayerAtSpawn
```

复活会把玩家放回原出生点，重置 HP、视角、垂直速度和落地状态，并设置短暂无敌时间。复活后会清理尚未执行的未来输入，避免死亡前的旧输入继续影响复活后的状态。

## 11. AOI 和物理

AOI 代码在 `src/roomserver/logic/aoi.go`。当前简化实现会排除自己、空对象、死亡玩家，并按距离过滤可见玩家。

物理接口在 `src/roomserver/logic/physics.go`。Room 只依赖 `PhysicsWorld` 接口，不直接 import cgo。PhysX 后端在 `src/roomserver/physx`，每个房间创建独立 PhysX scene，避免不同房间玩家发生碰撞串扰。

默认地图碰撞文件：

```text
config/maps/mfps_arena/collision.json
```

## 12. 断线和离房

如果客户端断线、读失败、写失败或服务关闭，`Session.Close` 会关闭连接。`readLoop` 退出时调用：

```text
Server.HandleSessionClosed
  -> sessions.Delete
  -> RoomManager.LeaveRoom
  -> Room.Leave
  -> Room.handleLeave
  -> delete players / syncStates
  -> physics.RemovePlayer
```

离房后出生点会释放，因为 `nextSpawnPoint` 只根据当前 `players` 里的 `SpawnID` 判断占用。

## 13. 手动测试思路

1. 启动 roomserver：

```bash
go run ./src/roomserver/cmd
```

2. 客户端用相同密钥生成 room token。

3. KCP 连接 `127.0.0.1:9001`。

4. 发送 `MsgJoinRoom`，payload 为 protobuf `JoinRoomReq{token}`。

5. 收到 `MsgJoinRoomAck` 后循环发送 `MsgPlayerInput`。

6. 客户端应持续收到 `MsgSnapshot`，并以快照里的 `PlayerState` 作为权威显示状态。

## 14. 当前限制和后续建议

当前限制：

- `src/roomserver/cmd/main.go` 还没有统一读取 `config/config.yaml`
- 没有完整客户端测试工具
- 已接入 `mfps_arena` 的 box 静态地图碰撞，但还没有 mesh、sphere、capsule 和 trigger 区域
- 武器系统、伤害规则和结算仍是简化版
- 没有房间空闲销毁
- 没有 token nonce 一次性消费

建议后续顺序：

1. 写一个最小 KCP protobuf 测试客户端
2. 跑通 `JoinRoom -> JoinRoomAck -> PlayerInput -> Snapshot`
3. 把 roomserver 配置接入统一 YAML 加载
4. 增加房间空闲关闭和玩家重复登录处理
5. 增加客户端快照插值和平滑显示
6. 扩展武器、伤害、死亡、结算和战斗事件广播

## 15. 一句话总结

当前 roomserver 的核心链路是：KCP 连接进入 Session，Session 读到 protobuf 业务消息后交给 Server，Server 校验 token 并调用 RoomManager，RoomManager 把玩家和输入投递给 Room，Room 在自己的 tick goroutine 中用 PhysX 推进服务端权威状态，再按 AOI 生成 Snapshot，通过 Session 写回客户端。
