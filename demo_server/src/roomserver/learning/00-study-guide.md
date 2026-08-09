# roomserver 学习路线

这组文档按阅读阶段拆分 roomserver。建议先读调用链，再读字段，再回到代码里逐个函数跟踪。

## 1. 当前 roomserver 做什么

roomserver 是战斗房间进程，当前主要负责：

- 接收客户端 KCP 连接和 protobuf 业务消息
- 校验入房 token，把玩家放入指定房间
- 为每个房间维护固定 tick 循环
- 接收玩家输入，只把输入作为请求，不信任客户端坐标
- 调用物理后端推进玩家权威坐标
- 按快照频率广播玩家状态
- 加载地图碰撞和出生点
- 处理服务端权威开火、死亡、复活和战绩查询

一句话调用链：

```text
cmd/main.go
  -> service.NewServer / Server.Start
  -> acceptLoop 接收 KCP 连接
  -> NewSession(..., handler = *Server)
  -> Session.readLoop 读取消息
  -> Server.HandleMessage 分发业务
  -> RoomManager 找到或创建 Room
  -> Room.loop 固定 tick 更新
  -> PhysicsWorld 计算权威物理结果
  -> Session.writeLoop 写回 snapshot / game event / error
```

## 2. 推荐阅读顺序

1. [01-startup-config-session.md](01-startup-config-session.md)

   先看启动入口、配置、Server 和 Session。目标是理解“服务怎么启动、连接怎么进来、为什么 Session 能回调 Server”。

2. [02-protocol-fields.md](02-protocol-fields.md)

   再看消息协议和每个字段。目标是知道客户端和服务端到底交换了哪些 protobuf payload。

3. [03-room-manager-and-room-loop.md](03-room-manager-and-room-loop.md)

   然后看 RoomManager 和 Room。目标是理解房间创建、入房、离房、事件队列和固定帧循环。

4. [04-authoritative-movement-and-sync.md](04-authoritative-movement-and-sync.md)

   接着看服务端权威移动、输入排帧、状态快照和为什么服务端只信输入。

5. [05-physics-and-map-collision.md](05-physics-and-map-collision.md)

   最后看物理接口、Simple 后端、PhysX 后端、地图碰撞 JSON 和出生点加载。

## 3. 分层关系

roomserver 当前主要分三层：

| 层级 | 目录 | 职责 |
| --- | --- | --- |
| cmd | [../cmd](../cmd) | 进程启动入口，初始化日志、context、server |
| service | [../service](../service) | 网络接入层，负责 KCP 连接、读写消息、基础校验、调用 logic |
| logic | [../logic](../logic) | 房间业务层，负责房间、玩家、tick、输入、同步、AOI、物理抽象 |
| physx | [../physx](../physx) | 物理后端实现层，封装 cgo/PhysX 和地图碰撞加载 |
| protocol | [../protocol](../protocol) | 房间内 KCP 业务帧和 protobuf 编解码 |
| config | [../config](../config) | roomserver 运行配置和默认值 |

依赖方向是：

```text
service -> logic -> protocol
service -> physx -> logic
logic -> protocol
```

logic 层只依赖 `PhysicsWorld` 接口，不直接 import cgo。这样以后替换物理后端、做单元测试、或者扩展服务端权威移动时，业务层不需要知道 PhysX 的 C++ 细节。

## 4. 读代码时抓住三条主线

### 4.1 连接主线

```text
Server.acceptLoop
  -> NewSession
  -> Session.Start
  -> readLoop / writeLoop
```

连接主线在 [../service/server.go](../service/server.go) 和 [../service/session.go](../service/session.go)。它回答的问题是：“客户端消息怎么进来，服务端消息怎么出去”。

### 4.2 房间主线

```text
Server.handleJoinRoom
  -> RoomManager.JoinRoom
  -> Room.Join 投递事件
  -> Room.handleJoin 真正入房
  -> Room.update 每 tick 推进
```

房间主线在 [../logic/room_manager.go](../logic/room_manager.go) 和 [../logic/room.go](../logic/room.go)。它回答的问题是：“玩家属于哪个房间，房间如何串行处理状态变化”。

### 4.3 同步主线

```text
PlayerInput
  -> sanitizePlayerInput
  -> nextAvailableInputTick
  -> inputForTick
  -> simulatePlayerTick
  -> Snapshot
```

同步主线在 [../logic/movement.go](../logic/movement.go)、[../logic/sync.go](../logic/sync.go)、[../logic/room.go](../logic/room.go)。它回答的问题是：“为什么服务端只信输入，不信客户端位置”。

## 5. 最重要的设计点

- Session 只负责网络读写，不处理业务细节。
- Server 实现 `MessageHandler`，作为 Session 的业务回调对象。
- RoomManager 管玩家到房间的映射，并负责按需创建 Room。
- Room 用单 goroutine 固定 tick 循环处理事件，避免房间状态被多 goroutine 直接并发修改。
- 客户端发送输入，服务端基于输入、tickRate、物理后端计算权威状态。
- 客户端不预测，客户端本地玩家和其他玩家都以服务端 Snapshot 为准。
- PhysX 通过 `PhysicsWorld` 接口接入 logic，地图碰撞和出生点在 physx 包里加载。
