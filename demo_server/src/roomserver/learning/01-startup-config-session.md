# 阶段一：启动、配置、Server 和 Session

本阶段目标：看懂 roomserver 怎么启动、监听 KCP、创建会话，以及 Session 为什么能调用 Server 的业务方法。

## 1. 启动入口

入口文件是 [../cmd/main.go](../cmd/main.go)。核心流程：

```text
glog.Init
  -> signal.NotifyContext
  -> roomconfig.DefaultConfig
  -> service.NewServer
  -> server.Start
  -> 等待退出信号
  -> server.Stop
```

关键代码：

```go
cfg := roomconfig.DefaultConfig()
server := service.NewServer(cfg)
if err := server.Start(ctx); err != nil {
    glog.Fatal(ctx, "start roomserver failed", glog.Err(err))
}
```

当前入口直接使用 roomserver 默认配置。全局配置文件里的 `room_server_01` 已经有对应字段，但当前这个入口还没有统一读取 [../../../config/config.yaml](../../../config/config.yaml)。

## 2. Config 字段说明

结构定义在 [../config/config.go](../config/config.go)。`DefaultConfig` 提供默认值，`Normalize` 用来补齐非法或空值。

| 字段 | 默认值 | 含义 | 主要使用位置 |
| --- | --- | --- | --- |
| `ServerID` | `room-01` | 当前 roomserver 的唯一 ID，入房 token 里的目标 server 必须匹配 | [../service/server.go](../service/server.go) `handleJoinRoom` |
| `ListenAddr` | `:9001` | KCP 监听地址 | [../service/server.go](../service/server.go) `Start` |
| `TokenSecret` | `room-token-secret` | room token HMAC 签名密钥 | [../service/server.go](../service/server.go) `handleJoinRoom` |
| `MaxRooms` | `1000` | 当前进程最多创建多少个房间 | [../logic/room_manager.go](../logic/room_manager.go) `getOrCreateRoom` |
| `MaxPlayersPerRoom` | `2` | 单个房间最大玩家数 | [../logic/room.go](../logic/room.go) `handleJoinEvent` |
| `TickRate` | `20` | 房间逻辑帧率，每秒更新多少次 | [../logic/room.go](../logic/room.go) `loop` |
| `SnapshotRate` | `10` | 状态快照发送频率，每秒广播多少次 | [../logic/room.go](../logic/room.go) `update` |
| `ReadTimeout` | `10s` | 连接读超时，长时间无消息会断开 | [../service/session.go](../service/session.go) `readLoop` |
| `WriteQueueSize` | `128` | 单连接控制消息发送队列长度 | [../service/session.go](../service/session.go) `NewSession` |
| `MaxPayloadSize` | `64KB` | 单条业务消息最大 payload | [../protocol/message.go](../protocol/message.go) `ReadMessage` / `WriteMessage` |
| `PhysicsBackend` | `physx` | 物理后端类型，支持 `physx` 或 `simple` | [../service/server.go](../service/server.go) `newPhysicsWorldFactory` |
| `PlayerCapsuleRadius` | `0.35` | 玩家胶囊体半径 | [../physx/world.go](../physx/world.go) `AddPlayer` |
| `PlayerCapsuleHeight` | `1.8` | 玩家胶囊体高度 | [../physx/world.go](../physx/world.go) `AddPlayer` |
| `PhysicsGroundPlane` | `true` | 是否创建 y=0 默认地面 | [../physx/world.go](../physx/world.go) `NewWorld` |
| `PhysXPVDEnabled` | `false` | 是否启用 PhysX Visual Debugger | [../service/server.go](../service/server.go) `newPhysicsWorldFactory` |
| `PhysXPVDHost` | `127.0.0.1` | PVD 监听地址 | [../service/server.go](../service/server.go) `newPhysicsWorldFactory` |
| `PhysXPVDPort` | `5425` | PVD 监听端口 | [../service/server.go](../service/server.go) `newPhysicsWorldFactory` |
| `PhysXPVDTimeoutMS` | `100` | PVD 连接超时毫秒数 | [../service/server.go](../service/server.go) `newPhysicsWorldFactory` |
| `DefaultMapID` | `mfps_arena` | 默认地图 ID | [../service/server.go](../service/server.go) `resolveMapCollisionMetadata` |
| `MapCollisionPath` | `config/maps/mfps_arena/collision.json` | 服务端地图碰撞 JSON 路径 | [../physx/world.go](../physx/world.go) `loadMapCollision` |
| `PhysicsHash` | `sha256:...` | 服务端物理数据 hash，会在入房响应中下发给客户端 | [../logic/room.go](../logic/room.go) `buildJoinAck` |
| `MaxInputHoldTicks` | `8` | 丢输入时，上一帧移动输入最多沿用多少帧 | [../logic/room.go](../logic/room.go) `inputForTick` |
| `GameDuration` | `3m` | 单局对局时长 | [../logic/room.go](../logic/room.go) `markGameStarted` |

配置文件对应字段在 [../../../config/config.yaml](../../../config/config.yaml) 的 `room_server_01` 下。

## 3. Server 字段说明

结构定义在 [../service/server.go](../service/server.go)。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `cfg` | `roomconfig.Config` | roomserver 运行配置，构造时会 Normalize |
| `manager` | `*logic.RoomManager` | 房间管理器，负责房间创建、玩家和房间关系、输入投递 |
| `listener` | `*kcp.Listener` | KCP 监听器，负责接收客户端连接 |
| `sessions` | `sync.Map` | 当前连接会话表，key 是 sessionID，value 是 `*Session` |
| `seq` | `atomic.Uint64` | 会话自增序号，用来生成唯一 sessionID |

`Server.Start` 做这些事：

```text
1. 校验地图碰撞元数据并绑定运行时 physics hash
2. 根据配置创建 PhysicsWorldFactory
3. 组装 SyncConfig 并创建 RoomManager
4. 监听 KCP 地址
5. 启动 acceptLoop
```

`newPhysicsWorldFactory` 根据 `PhysicsBackend` 选择：

- `physx`：使用 [../physx/world.go](../physx/world.go) `physx.NewFactory`
- `simple`：使用 [../logic/physics.go](../logic/physics.go) `logic.NewSimplePhysicsWorldFactory`

## 4. acceptLoop 如何创建连接

代码在 [../service/server.go](../service/server.go) `acceptLoop`。

```text
AcceptKCP
  -> 设置 KCP 低延迟参数
  -> sequence := s.seq.Add(1)
  -> sessionID := newSessionID(remoteAddr, sequence)
  -> session := NewSession(sessionID, conn, s.cfg, s)
  -> s.sessions.Store(sessionID, session)
  -> session.Start(ctx)
```

注意 `NewSession(..., s)` 的最后一个参数是 `*Server`。这就是 Session 后面能回调 Server 的原因。

## 5. Session 字段说明

结构定义在 [../service/session.go](../service/session.go)。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `id` | `string` | 会话 ID，由远端地址和自增序号组成 |
| `conn` | `*kcp.UDPSession` | KCP 连接对象 |
| `cfg` | `roomconfig.Config` | 会话需要的读超时、队列长度、payload 限制等配置 |
| `handler` | `MessageHandler` | 业务消息处理器，当前实际对象是 `*Server` |
| `sendCh` | `chan protocol.Message` | 服务端待发送控制消息队列 |
| `snapshotCh` | `chan protocol.Message` | 服务端快照单槽队列，只保留最新快照 |
| `closeCh` | `chan struct{}` | 会话关闭信号 |
| `closeMu` | `sync.Once` | 保证 Close 只执行一次，避免重复 close channel |
| `playerID` | `uint64` | 当前连接绑定的玩家 ID，入房成功前为 0 |
| `roomID` | `string` | 当前连接绑定的房间 ID，入房后设置 |

## 6. MessageHandler 接口

接口定义在 [../service/session.go](../service/session.go)：

```go
type MessageHandler interface {
    HandleMessage(context.Context, *Session, protocol.Message)
    HandleSessionClosed(context.Context, *Session)
}
```

`*Server` 实现了这两个方法，所以它自动满足接口：

- [../service/server.go](../service/server.go) `HandleMessage`
- [../service/server.go](../service/server.go) `HandleSessionClosed`

调用链是：

```text
Server.acceptLoop 创建 Session 时传入 handler = *Server
Session.readLoop 读到消息
Session 调用 s.handler.HandleMessage(ctx, s, message)
Go 运行时分派到 Server.HandleMessage(ctx, session, message)
```

## 7. readLoop 和 writeLoop

`Session.Start` 会启动两个 goroutine：

```go
go s.readLoop(ctx)
go s.writeLoop(ctx)
```

`readLoop` 负责：

```text
检查 ctx / closeCh
  -> 设置读超时
  -> protocol.ReadMessage
  -> handler.HandleMessage
  -> 退出时 Close 并通知 HandleSessionClosed
```

`writeLoop` 负责：

```text
等待 ctx / closeCh / sendCh / snapshotCh
  -> 从队列取出 protocol.Message
  -> protocol.WriteMessage 写入 KCP 连接
  -> 写失败则关闭会话
```

`Session.Send` 不直接写网络，而是把消息投递到 `sendCh`。`Session.SendSnapshot` 投递到单槽 `snapshotCh`，槽满时用最新快照覆盖旧快照。这样能防止慢客户端堆积大量过期快照。
