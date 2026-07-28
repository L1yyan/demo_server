# Roomserver 全链路说明

本文档说明当前 `src/roomserver` 的完整链路，从玩家准备接入开始，一直到入房、输入、房间 tick、客户端预测校验、状态快照、纠偏和断线清理。

当前 roomserver 已接入 KCP、room token、logicserver 到 matchserver 再到 roomserver 的入房链路、房间 tick、AOI、PhysX 地图碰撞、出生点同步、客户端预测回滚协议和服务端权威纠偏。它还不是完整 FPS 战斗服，尚未接入完整玩法结算。

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
│   ├── sync.go              # 同步模式、回滚窗口和纠偏配置
│   ├── aoi.go               # AOI 可见性过滤接口和简化实现
│   └── physics.go           # 物理接口和简化实现
└── protocol
    ├── message.go           # KCP 业务帧、消息类型、JSON payload
    └── token.go             # room token 签发和校验
```

协议定义：

```text
pb/room/room.proto           # roomserver 协议文档和后续生成基础
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
  default_map_id: "map_001"
  map_collision_path: "configs/maps/map_001/collision.json"
  physics_hash: "sha256:e1328d5d97e68938b5c55f13a7c04553849cdefcdfed8a32ea288275464d9289"
  prediction_enabled: true
  rollback_window_ticks: 60
  future_input_window_ticks: 8
  prediction_keyframe_interval: 2
  position_tolerance: 0.15
  hard_position_tolerance: 0.5
  angle_tolerance: 2.0
  max_input_batch_frames: 8
  max_input_hold_ticks: 3
  correction_min_interval_ticks: 2
```

注意：`cmd/main.go` 当前仍使用 `roomconfig.DefaultConfig()` 启动，`config/config.yaml` 已补齐 roomserver 配置示例，后续需要接入统一 YAML 加载器。

## 2. 服务启动链路

启动入口在 `src/roomserver/cmd/main.go`。

启动流程：

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

当前默认监听地址是：

```text
:9001
```

也就是客户端可以连接 roomserver 的 UDP/KCP 端口 `9001`。

`server.Start(ctx)` 在 `src/roomserver/service/server.go` 中完成这些事：

1. 创建 `RoomManager`。
2. 调用 `kcp.ListenWithOptions` 监听 KCP。
3. 设置 KCP socket 读写缓冲。
4. 启动 `acceptLoop` 等待客户端连接。

简化链路：

```text
Server.Start
  -> logic.NewRoomManagerWithSync
  -> kcp.ListenWithOptions
  -> go acceptLoop
```

## 3. 玩家准备接入

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

当前已经可以从 logicserver 请求匹配，由 matchserver 分配 roomserver 和 room_id 并签发 room token。`protocol.GenerateRoomToken` 仍可用于本地或单元测试时临时生成 room token。

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

关键字段含义：

- `PlayerID`：玩家 ID。
- `RoomID`：玩家要进入的房间 ID。
- `ServerID`：目标 roomserver ID，当前默认是 `room-01`。
- `MatchID`：匹配批次或对局 ID，当前只预留。
- `Nonce`：随机串，当前只预留，后续可配合 Redis 做一次性 token。
- `ExpiresAt`：过期时间，防止旧 token 长期可用。

为什么 room token 要单独做：

- 登录 token 只能证明“用户已登录”。
- room token 证明“这个玩家被允许进入这个 roomserver 的这个房间”。
- room token 可以非常短期有效，例如 30 秒到 2 分钟。
- room token 可以绑定 `server_id` 和 `room_id`，避免拿到其他服务器乱用。

## 4. 客户端建立 KCP 连接

客户端拿到 room token 后，先与 roomserver 建立 KCP 连接。

服务端接收连接的位置：

```text
src/roomserver/service/server.go
  -> acceptLoop
```

`acceptLoop` 做的事情：

1. 调用 `listener.AcceptKCP()` 等待客户端连接。
2. 对连接设置 KCP 低延迟参数：
   - `SetNoDelay(1, 20, 2, 1)`
   - `SetStreamMode(true)`
   - `SetWriteDelay(false)`
3. 为连接生成 `session_id`。
4. 创建 `Session`。
5. 保存到 `Server.sessions`。
6. 启动 session 的读写循环。

简化链路：

```text
acceptLoop
  -> AcceptKCP
  -> NewSession
  -> sessions.Store
  -> session.Start
```

## 5. Session 是什么

`Session` 在 `src/roomserver/service/session.go`。

它表示一个客户端连接，保存：

- `id`：服务端生成的连接 ID。
- `conn`：KCP 连接对象。
- `sendCh`：服务端发送队列。
- `closeCh`：关闭信号。
- `playerID`：入房后绑定的玩家 ID。
- `roomID`：入房后绑定的房间 ID。

每个 session 启动两个 goroutine：

```text
readLoop   # 读客户端消息
writeLoop  # 写服务端消息
```

### 5.1 readLoop

`readLoop` 持续从 KCP 连接读取消息：

```text
readLoop
  -> SetReadDeadline
  -> protocol.ReadMessage
  -> Server.HandleMessage
```

如果读取失败、超时、消息非法，当前实现会关闭 session。

### 5.2 writeLoop

`writeLoop` 从 `sendCh` 读取待发送消息，再写回客户端：

```text
writeLoop
  -> <-sendCh
  -> protocol.WriteMessage
```

`sendCh` 是有限长度队列，默认 `128`。如果队列满了，`Session.Send` 会返回 `false`。

这样做是为了防止某个慢客户端收不动消息，导致服务端内存无限堆积。

## 6. KCP 上的业务帧格式

KCP 只负责传输字节，不知道业务消息边界，所以当前代码在 `protocol/message.go` 里定义了自己的帧格式：

```text
uint16 message_type
uint32 payload_length
bytes payload
```

也就是每条消息前 6 个字节是头部：

```text
前 2 字节：消息类型
后 4 字节：payload 长度
```

然后再跟具体 payload。

当前 payload 使用 JSON，原因是第一版方便调试。后续如果要提高性能，可以把 `protocol` 包内部改成 protobuf 编解码，尽量不影响 `logic` 层。

当前消息类型：

```go
MsgJoinRoom         = 1  // 请求加入房间
MsgJoinRoomAck      = 2  // 加入房间响应
MsgHeartbeat        = 3  // 心跳请求
MsgHeartbeatAck     = 4  // 心跳响应
MsgPlayerInput      = 5  // 单帧玩家输入
MsgSnapshot         = 6  // 状态快照
MsgError            = 7  // 错误响应
MsgPlayerInputBatch = 8  // 批量玩家输入
MsgInputAck         = 9  // 输入处理确认
MsgStateCorrection  = 10 // 权威状态纠偏
```

## 7. 玩家请求入房

客户端连接成功后，第一条关键业务消息应该是 `MsgJoinRoom`。

payload 示例：

```json
{
  "token": "room token 字符串",
  "sync_version": 1,
  "prediction_enabled": true,
  "physics_hash": "sha256:e1328d5d97e68938b5c55f13a7c04553849cdefcdfed8a32ea288275464d9289"
}
```

旧客户端只传 `token` 也能入房，但服务端会按 `snapshot_only` 模式处理，不启用客户端预测。

服务端处理入口：

```text
Server.HandleMessage
  -> handleJoinRoom
```

`handleJoinRoom` 做这些校验：

1. JSON payload 是否能解析成 `JoinRoomRequest`。
2. token 是否能通过 `protocol.ParseRoomToken` 校验。
3. token 的 `server_id` 是否等于当前 roomserver 的 `cfg.ServerID`。
4. token 里是否有合法的 `room_id` 和 `player_id`。
5. 根据 `sync_version`、`prediction_enabled` 和 `physics_hash` 判断实际同步模式。
6. 调用 `RoomManager.JoinRoom` 加入房间。

简化链路：

```text
客户端 MsgJoinRoom
  -> Session.readLoop
  -> protocol.ReadMessage
  -> Server.HandleMessage
  -> Server.handleJoinRoom
  -> protocol.ParseRoomToken
  -> RoomManager.JoinRoom
```

如果失败，服务端发送 `MsgError`。

如果成功，最终房间会给客户端发送 `MsgJoinRoomAck`。

## 8. RoomManager 如何创建或查找房间

`RoomManager` 在 `src/roomserver/logic/room_manager.go`。

它维护两个 map：

```go
rooms       map[string]*Room   // room_id -> Room
playerRooms map[uint64]string  // player_id -> room_id
```

玩家加入房间时：

```text
RoomManager.JoinRoom
  -> getOrCreateRoom
  -> room.Join(player)
  -> 记录 player_id 和 room_id 的关系
```

如果房间不存在，`getOrCreateRoom` 会创建一个新房间：

```text
NewRoom
  -> room.Start
  -> 保存到 rooms map
```

当前 roomserver 仍保留“收到合法 room token 后按 room_id 自动创建房间”的实现。上游 matchserver 已负责分配 roomserver 和 room_id，后续如果要更严格控制房间生命周期，可以改成由 matchserver 或房间管理服务提前创建房间，roomserver 只接受已登记的合法房间。

## 9. Room 如何处理玩家加入

`Room` 在 `src/roomserver/logic/room.go`。

room 内部不是直接被外部 goroutine 修改状态，而是通过事件队列：

```go
events chan roomEvent
```

玩家加入时：

```text
RoomManager.JoinRoom
  -> room.Join(player)
  -> 写入 room.events
```

room 自己的 goroutine 在 `loop` 中消费事件：

```text
room.loop
  -> 收到 roomEventJoin
  -> handleJoin
```

`handleJoin` 做这些事：

1. 检查玩家是否为空。
2. 检查房间是否满员。
3. 检查玩家是否已在房间中。
4. 从地图出生点中选择未占用的位置：
   - 第 1 名玩家默认使用 `spawn_a`
   - 第 2 名玩家默认使用 `spawn_b`
5. 初始化玩家状态：
   - `RoomID`
   - `SpawnID`
   - `X/Y/Z/Yaw/Pitch`
   - `HP = 100`
   - `Alive = true`
6. 保存到 `players` map。
7. 发送 `MsgJoinRoomAck`，告知客户端当前玩家的出生点编号和初始位置。

成功响应 payload：

```json
{
  "ok": true,
  "room_id": "room-xxx",
  "content": "ok",
  "tick": 0,
  "spawn_id": "spawn_a",
  "x": -4,
  "y": 0.1,
  "z": 0,
  "yaw": 0,
  "pitch": 0,
  "tick_rate": 20,
  "snapshot_rate": 10,
  "server_time": 1785139200000,
  "sync_mode": "prediction_authoritative",
  "map_id": "map_001",
  "physics_hash": "sha256:e1328d5d97e68938b5c55f13a7c04553849cdefcdfed8a32ea288275464d9289",
  "rollback_window_ticks": 60,
  "future_input_window_ticks": 8,
  "prediction_keyframe_interval": 2,
  "position_tolerance": 0.15,
  "hard_position_tolerance": 0.5,
  "angle_tolerance": 2
}
```

## 10. 心跳链路

客户端可以定时发送 `MsgHeartbeat`。

当前服务端处理非常简单：

```text
Server.HandleMessage
  -> handleHeartbeat
  -> Send MsgHeartbeatAck
```

服务端返回：

```json
{
  "server_time": 1234567890
}
```

当前心跳主要用于保持连接和验证链路。后续可以增强：

- 记录 `last_seen`。
- 统计 RTT。
- 超时踢出。
- 判断弱网状态。

## 11. 玩家输入链路

玩家入房后，可以发送 `MsgPlayerInput`。支持预测回滚的新客户端推荐发送 `MsgPlayerInputBatch`，把多帧输入和关键帧预测状态一起上报。

payload 示例：

```json
{
  "client_tick": 100,
  "move_x": 0,
  "move_z": 1,
  "yaw": 30,
  "pitch": 5,
  "fire": false
}
```

服务端处理入口：

```text
Server.HandleMessage
  -> handlePlayerInput
```

`handlePlayerInput` 做这些事：

1. 检查 session 是否已经绑定 `player_id`。
2. 解析 JSON 为 `protocol.PlayerInput`。
3. 调用 `RoomManager.PushInput`。
4. `RoomManager` 找到玩家所在房间。
5. 把输入写入房间事件队列。

简化链路：

```text
客户端 MsgPlayerInput
  -> Session.readLoop
  -> Server.HandleMessage
  -> Server.handlePlayerInput
  -> RoomManager.PushInput
  -> Room.PushInput
  -> room.events
```

批量输入 payload 示例：

```json
{
  "base_client_tick": 101,
  "last_received_server_tick": 95,
  "frames": [
    {
      "client_tick": 101,
      "move_x": 0,
      "move_z": 1,
      "yaw": 0,
      "pitch": 0,
      "fire": false,
      "predicted_state": {
        "x": -4,
        "y": 0.1,
        "z": 0.2,
        "yaw": 0,
        "pitch": 0,
        "state_hash": 0
      }
    }
  ]
}
```

批量输入会经过 `Server.handlePlayerInputBatch -> RoomManager.PushInputBatch -> Room.PushInputBatch -> room.events`。服务端只信任输入方向、视角和开火意图，客户端预测坐标只用于和服务端权威历史做误差校验。

注意：service 层只负责解析和投递，不直接改玩家坐标。这符合分层要求。

## 12. 房间 tick 循环

每个 `Room` 启动后，会有一个 goroutine 跑固定 tick。

代码位置：

```text
src/roomserver/logic/room.go
  -> Room.Start
  -> Room.loop
```

当前默认 `tick_rate = 20`，也就是每秒 20 帧：

```text
1 秒 / 20 = 50ms
```

room loop 同时处理两类事情：

1. 房间事件：加入、离开、输入。
2. 定时 tick：推进房间帧号，按频率广播快照。

简化结构：

```text
for {
  select {
    case event := <-events:
      handleEvent(event)
    case <-ticker.C:
      update()
  }
}
```

当前输入处理分两类：

- 单帧输入：兼容旧客户端，直接投递 `MsgPlayerInput`。
- 批量输入：预测客户端使用，服务端按 tick 缓存输入帧并在房间更新中按顺序应用。

移动推进由服务端权威执行，核心逻辑是根据输入方向、tick 步长和移动速度调用 `PhysicsWorld.MovePlayer`，再把 PhysX 修正后的坐标写回玩家状态。简化表达如下：

```text
input -> normalize move direction -> PhysicsWorld.MovePlayer -> update authoritative Player
player.Yaw = input.Yaw
player.Pitch = input.Pitch
```

如果 `input.Fire == true`，会调用 `PhysicsWorld.Raycast`。当前已默认使用 PhysX 后端，地图碰撞来自 `configs/maps/map_001/collision.json`。

后续真实玩法要在这里扩展：

- 权威移动。
- 碰撞检测。
- 开火频率校验。
- raycast 命中。
- 伤害计算。
- 死亡和复活。
- 战斗事件广播。

## 13. Snapshot 广播链路

当前默认：

```text
tick_rate = 20
snapshot_rate = 10
```

意思是房间每秒计算 20 次，但每秒只发 10 次快照。

`Room.update` 中会判断是否到达快照发送间隔：

```text
intervalTicks = tickRate / snapshotRate
```

到了时间就调用：

```text
broadcastAcks
broadcastSnapshots
```

广播流程：

1. 先给预测客户端发送 `MsgInputAck`，确认服务端已接受和已校验的输入 tick。
2. 收集当前房间所有玩家。
3. 对每个玩家单独计算 AOI 可见对象。
4. 生成该玩家自己的 snapshot。
5. 通过玩家的 session 发送 `MsgSnapshot`。

简化链路：

```text
Room.update
  -> broadcastAcks
  -> broadcastSnapshots
  -> AOIFilter.FilterVisible
  -> protocol.NewJSONMessage(MsgSnapshot, ...)
  -> player.Session.Send
  -> Session.writeLoop
  -> protocol.WriteMessage
  -> KCP 发给客户端
```

snapshot payload 示例：

```json
{
  "server_tick": 120,
  "players": [
    {
      "player_id": 1001,
      "x": 1.2,
      "y": 0,
      "z": 4.8,
      "yaw": 30,
      "pitch": 5,
      "hp": 100
    }
  ]
}
```

当前 snapshot 至少会包含玩家自己，另外再包含 AOI 判断可见的其他玩家。

`InputAck` payload 示例：

```json
{
  "server_tick": 120,
  "last_accepted_input_tick": 118,
  "last_verified_input_tick": 116
}
```

客户端收到后可以删除已确认输入历史，同时保留回滚窗口内仍可能用于重放的输入和预测状态。

## 14. AOI 当前如何工作

AOI 代码在 `src/roomserver/logic/aoi.go`。

接口：

```go
type AOIFilter interface {
    FilterVisible(self *Player, candidates []*Player) []*Player
}
```

当前实现是 `SimpleAOIFilter`。

过滤规则：

1. 排除空对象。
2. 排除自己。
3. 排除死亡玩家。
4. 按距离过滤，默认 `80`。
5. 按水平视野角度过滤，默认 `120` 度。

当前没有做墙体遮挡，因为真实遮挡需要地图碰撞和物理查询。后续可以把 `PhysicsWorld.Raycast` 接进 AOI，用 raycast 判断某个目标是否被墙挡住。

## 15. 物理接口当前如何工作

物理接口代码在 `src/roomserver/logic/physics.go`，PhysX cgo 后端在 `src/roomserver/physx`。

接口：

```go
type PhysicsWorld interface {
    AddPlayer(playerID uint64, position Vector3) error
    RemovePlayer(playerID uint64) error
    MovePlayer(MovePlayerRequest) (MovePlayerResult, error)
    GetPlayerPosition(playerID uint64) (Vector3, error)
    SetPlayerPosition(playerID uint64, position Vector3) error
    Raycast(RaycastRequest) (RaycastHit, error)
    BatchRaycast([]RaycastRequest) ([]RaycastHit, error)
    SpawnPoints() []SpawnPoint
    Close() error
}
```

当前默认后端是 PhysX：

```text
PhysicsBackend = "physx"
```

默认构建 roomserver 时需要启用 `physx` build tag，并准备本地 PhysX SDK：

```bash
scripts/setup_physx.sh
scripts/build_all.sh
```

`setup_physx.sh` 会把第三方源码和构建产物放到 `third_party` 下：

```text
third_party/PhysX
third_party/physx-sdk
third_party/tools
```

`Room` 仍然只依赖 `PhysicsWorld` 接口，不直接调用 cgo。每个房间通过 `PhysicsWorldFactory` 创建独立 PhysX scene，避免不同房间玩家发生碰撞串扰。PhysX world 创建时会加载默认地图碰撞：

```text
configs/maps/map_001/collision.json
```

这份文件是 Unity 导出的服务端物理地图，当前支持 `box` 静态碰撞体，用于墙体、地面、建筑和障碍物阻挡。房间入场时会按 `spawn_points` 顺序分配出生点，所以两人房默认第 1 名玩家在 `spawn_a`，第 2 名玩家在 `spawn_b`。

需要特别注意：

- 默认构建会依赖 PhysX SDK，缺少 SDK 时应先运行 `scripts/setup_physx.sh`。
- `collision.json` 丢失、格式错误或存在暂不支持的实体 shape 时，PhysX world 创建会失败。
- 高频 raycast 应优先批量调用 `BatchRaycast`。
- 玩家移动当前按房间 tick 调用 `MovePlayer`，后续人数增加时可扩展批量移动。
- 预测纠偏时可通过 `SetPlayerPosition` 把服务端权威坐标同步回物理世界。
- PhysX 对象生命周期由房间集中管理，玩家加入创建 actor，离房和停房释放 actor/world。
- Go 状态和 PhysX 状态的同步点在房间 tick 中。

## 16. 玩家断线和离房

如果客户端断线、读失败、写失败或服务关闭，`Session.Close` 会关闭连接。

`readLoop` 退出时会调用：

```text
Server.HandleSessionClosed
```

处理流程：

```text
HandleSessionClosed
  -> sessions.Delete(session.ID)
  -> RoomManager.LeaveRoom(playerID)
  -> Room.Leave(playerID)
  -> room.events
  -> Room.handleLeave
  -> delete(players, playerID)
```

这样玩家会从房间中移除。

当前第一版没有做房间空了自动销毁，后续可以在 `RoomManager` 里增加房间生命周期管理。

## 17. 错误响应链路

服务端遇到业务错误时，会调用：

```text
Server.sendError
```

它会发送 `MsgError`。

payload 示例：

```json
{
  "code": "invalid_token",
  "content": "room token expired"
}
```

当前常见错误：

- `bad_request`：请求 payload 不合法。
- `invalid_token`：room token 为空、非法或过期。
- `server_mismatch`：token 的 server_id 和当前 roomserver 不一致。
- `join_failed`：加入房间失败。
- `not_joined`：还没入房就发输入。
- `input_failed`：输入投递到房间失败。
- `unknown_message`：未知消息类型。

## 18. 当前完整时序图

```text
客户端
  |
  | 1. 从 logic/match 获取 room token
  |
  | 2. KCP 连接 :9001
  v
roomserver acceptLoop
  |
  | 3. 创建 Session
  v
Session.readLoop / writeLoop
  |
  | 4. 客户端发送 MsgJoinRoom(token)
  v
Server.HandleMessage
  |
  | 5. handleJoinRoom
  |    - 解析 JSON
  |    - 校验 room token
  |    - 校验 server_id
  v
RoomManager.JoinRoom
  |
  | 6. getOrCreateRoom
  |    - 不存在则 NewRoom + Start
  v
Room.events
  |
  | 7. Room.handleJoin
  |    - 初始化玩家
  |    - 保存到 players
  |    - 发送 MsgJoinRoomAck
  v
客户端入房成功
  |
  | 8. 客户端循环发送 MsgPlayerInput 或 MsgPlayerInputBatch
  v
Server.handlePlayerInput / Server.handlePlayerInputBatch
  |
  | 9. RoomManager.PushInput / RoomManager.PushInputBatch
  v
Room.events
  |
  | 10. Room 缓存或应用输入，按 tick 推进服务端权威状态
  |
  | 11. Room.update 固定 tick，校验预测状态并清理历史
  |
  | 12. Room.broadcastAcks + Room.broadcastSnapshots
  v
Session.writeLoop
  |
  | 13. KCP 发送 MsgInputAck / MsgSnapshot / MsgStateCorrection
  v
客户端确认输入、显示服务端状态，必要时回滚重放
```

## 19. 关键调用链参数说明

这一节按真实代码调用顺序，把主要函数调用和传入参数含义展开说明。你看代码时可以对照这一节理解“为什么这里要传这些参数”。

### 19.1 启动调用链

```text
main
  -> roomconfig.DefaultConfig()
  -> service.NewServer(cfg)
  -> server.Start(ctx)
```

`roomconfig.DefaultConfig()`：

- 无入参。
- 返回 roomserver 默认配置，例如 `ServerID=room-01`、`ListenAddr=:9001`、`TickRate=20`。
- 当前还没有接 YAML 加载器，所以先用默认值启动。

`service.NewServer(cfg)`：

- `cfg`：roomserver 配置对象。
- 里面包含监听地址、token 密钥、最大房间数、单房间最大人数、tick 频率、snapshot 频率等。
- `NewServer` 会调用 `cfg.Normalize()`，把空值或非法值补成默认值。

`server.Start(ctx)`：

- `ctx`：服务生命周期 context。
- 当进程收到退出信号时，`ctx.Done()` 会被触发，accept loop、room loop 等可以感知退出。
- `Start` 内部会创建 `RoomManager`，然后启动 KCP 监听。

### 19.2 Server.Start 内部调用链

```text
Server.Start(ctx)
  -> logic.NewRoomManagerWithSync(ctx, maxRooms, maxPlayersPerRoom, tickRate, snapshotRate, syncConfig, mapID, physicsHash, aoi, physicsFactory)
  -> kcp.ListenWithOptions(listenAddr, nil, 10, 3)
  -> listener.SetReadBuffer(4 * 1024 * 1024)
  -> listener.SetWriteBuffer(4 * 1024 * 1024)
  -> go acceptLoop(ctx)
```

`logic.NewRoomManagerWithSync(ctx, maxRooms, maxPlayersPerRoom, tickRate, snapshotRate, syncConfig, mapID, physicsHash, aoi, physicsFactory)`：

- `ctx`：房间管理器和房间 loop 共用的生命周期控制。
- `maxRooms`：当前 roomserver 进程最多允许创建多少房间，防止无限创建房间。
- `maxPlayersPerRoom`：单个房间最大玩家数，当前玩法目标是 2 人对局。
- `tickRate`：房间每秒逻辑更新次数，例如 20 表示每 50ms 更新一次。
- `snapshotRate`：每秒发送快照次数，例如 10 表示每 100ms 发一次快照。
- `syncConfig`：预测同步配置，包含回滚窗口、未来输入窗口、误差阈值和纠偏间隔。
- `mapID`：当前房间使用的地图 ID，会在入房响应中下发。
- `physicsHash`：服务端物理数据 hash，用于和客户端地图碰撞数据做一致性判断。
- `aoi`：AOI 过滤器。当前传 `logic.NewSimpleAOIFilter()`，用于按距离和视角过滤可见玩家。
- `physicsFactory`：物理世界工厂。当前默认按配置创建 PhysX world，每个房间独立一个物理场景。

`kcp.ListenWithOptions(listenAddr, nil, 10, 3)`：

- `listenAddr`：监听地址，当前默认是 `:9001`。
- `nil`：KCP 加密配置。这里传 nil 表示暂时不启用 KCP 内置加密。
- `10`：FEC 的 data shards 数量。FEC 是前向纠错，用额外冗余包提高弱网恢复能力。
- `3`：FEC 的 parity shards 数量，也就是冗余分片数量。
- 第一版先使用 kcp-go 常见参数，后续要结合实际压测调整。

`listener.SetReadBuffer(4 * 1024 * 1024)`：

- 设置 UDP socket 读缓冲区为 4MB。
- 读缓冲太小，在高并发或突发包较多时更容易丢包。

`listener.SetWriteBuffer(4 * 1024 * 1024)`：

- 设置 UDP socket 写缓冲区为 4MB。
- 写缓冲太小，在瞬时发送较多快照时可能更容易阻塞或丢包。

`go acceptLoop(ctx)`：

- `ctx`：用于服务退出时停止接收新连接。
- 这里开 goroutine 是因为 `AcceptKCP()` 会阻塞等待客户端连接，不能卡住 `Start` 返回。

### 19.3 接收客户端连接调用链

```text
acceptLoop(ctx)
  -> listener.AcceptKCP()
  -> conn.SetNoDelay(1, 20, 2, 1)
  -> conn.SetStreamMode(true)
  -> conn.SetWriteDelay(false)
  -> newSessionID(conn.RemoteAddr().String(), sequence)
  -> NewSession(sessionID, conn, cfg, server)
  -> sessions.Store(sessionID, session)
  -> session.Start(ctx)
```

`listener.AcceptKCP()`：

- 无业务入参。
- 阻塞等待一个新的 KCP 客户端连接。
- 返回的 `conn` 是一个客户端连接，后续读写都基于它。

`conn.SetNoDelay(1, 20, 2, 1)`：

- 第 1 个参数 `1`：开启 nodelay，降低延迟。
- 第 2 个参数 `20`：KCP 内部刷新间隔，单位毫秒。
- 第 3 个参数 `2`：快速重传参数，丢包时更快补发。
- 第 4 个参数 `1`：关闭常规拥塞控制，更偏低延迟。
- 这组参数适合先做低延迟联调，但不是最终压测参数。

`conn.SetStreamMode(true)`：

- `true`：开启流模式。
- 开启后更像 TCP 字节流，所以代码里必须自己用消息头区分业务消息边界。
- 当前 `protocol.ReadMessage` 就是用 `message_type + payload_length` 来解决边界问题。

`conn.SetWriteDelay(false)`：

- `false`：写消息时尽量不延迟合并，降低发送延迟。
- 对实时游戏更友好，但可能增加包数量。

`newSessionID(remoteAddr, sequence)`：

- `remoteAddr`：客户端远端地址，例如 `127.0.0.1:xxxxx`。
- `sequence`：服务端递增序号，避免同一个地址重连时 session_id 冲突。
- 返回一个服务端内部使用的连接 ID。

`NewSession(sessionID, conn, cfg, server)`：

- `sessionID`：当前连接的唯一 ID。
- `conn`：KCP 连接对象，负责底层读写。
- `cfg`：roomserver 配置，session 需要里面的读超时、发送队列长度、最大 payload 大小。
- `server`：消息处理器。`Session` 读到消息后会回调 `server.HandleMessage`。

`sessions.Store(sessionID, session)`：

- `sessionID`：map key。
- `session`：map value。
- 存起来后，服务端可以在关闭、统计或后续管理时找到这个连接。

`session.Start(ctx)`：

- `ctx`：session 读写循环的生命周期控制。
- 内部会启动 `readLoop(ctx)` 和 `writeLoop(ctx)` 两个 goroutine。

### 19.4 Session 读消息调用链

```text
Session.readLoop(ctx)
  -> conn.SetReadDeadline(time.Now().Add(cfg.ReadTimeout))
  -> protocol.ReadMessage(conn, cfg.MaxPayloadSize)
  -> handler.HandleMessage(ctx, session, message)
```

`conn.SetReadDeadline(time.Now().Add(cfg.ReadTimeout))`：

- 入参是一个未来时间点。
- 如果超过这个时间还没有读到数据，读操作会超时返回。
- `cfg.ReadTimeout` 当前默认是 10 秒，用来发现长时间没有消息的连接。

`protocol.ReadMessage(conn, cfg.MaxPayloadSize)`：

- `conn`：实现了 `io.Reader` 的 KCP 连接。
- `cfg.MaxPayloadSize`：允许读取的最大消息负载，当前默认 65536 字节。
- 这个限制是为了防止恶意客户端发超大 payload 占用内存。
- 返回 `protocol.Message`，里面有 `Type` 和 `Payload`。

`handler.HandleMessage(ctx, session, message)`：

- `ctx`：服务生命周期 context。
- `session`：消息来自哪个客户端连接。
- `message`：已经读出来的业务消息。
- 当前 `handler` 实际上就是 `Server`，所以会进入 `Server.HandleMessage`。

### 19.5 Server 消息分发调用链

```text
Server.HandleMessage(ctx, session, message)
  -> handleJoinRoom(ctx, session, message)
  -> handleHeartbeat(session)
  -> handlePlayerInput(ctx, session, message)
  -> handlePlayerInputBatch(ctx, session, message)
  -> sendError(session, code, content)
```

`Server.HandleMessage(ctx, session, message)`：

- `ctx`：服务生命周期和日志 context。
- `session`：发送这条消息的客户端。
- `message`：业务消息，`message.Type` 决定进入哪个处理分支。

`handleJoinRoom(ctx, session, message)`：

- `ctx`：用于日志记录和生命周期传递。
- `session`：当前请求入房的客户端连接。
- `message`：类型必须是 `MsgJoinRoom`，payload 应该是 `JoinRoomRequest` JSON。

`handleHeartbeat(session)`：

- `session`：发心跳的客户端连接。
- 当前不需要 `ctx`，因为它只是回一个 `MsgHeartbeatAck`。

`handlePlayerInput(ctx, session, message)`：

- `ctx`：用于日志记录。
- `session`：发送输入的客户端连接，必须已经入房并绑定 player_id。
- `message`：类型必须是 `MsgPlayerInput`，payload 应该是 `PlayerInput` JSON。

`handlePlayerInputBatch(ctx, session, message)`：

- `ctx`：用于日志记录。
- `session`：发送输入的客户端连接，必须已经入房并绑定 player_id。
- `message`：类型必须是 `MsgPlayerInputBatch`，payload 应该是 `PlayerInputBatch` JSON。

`sendError(session, code, content)`：

- `session`：错误消息要发给哪个客户端。
- `code`：机器可读的错误码，例如 `invalid_token`。
- `content`：人可读的错误信息，例如 `room token expired`。

### 19.6 入房处理调用链

```text
handleJoinRoom(ctx, session, message)
  -> protocol.DecodeJSON[JoinRoomRequest](message)
  -> protocol.ParseRoomToken(cfg.TokenSecret, request.Token)
  -> session.SetPlayer(claims.PlayerID, claims.RoomID)
  -> logic.Player{ID: claims.PlayerID, RoomID: claims.RoomID, Session: session}
  -> manager.JoinRoom(claims.RoomID, player)
```

`protocol.DecodeJSON[JoinRoomRequest](message)`：

- `message`：客户端发来的 `MsgJoinRoom` 消息。
- 泛型类型 `JoinRoomRequest`：告诉函数要把 JSON payload 解析成什么结构。
- 解析成功后可以拿到 `request.Token`。

`protocol.ParseRoomToken(cfg.TokenSecret, request.Token)`：

- `cfg.TokenSecret`：roomserver 用来验签的密钥，必须和签发 token 的服务一致。
- `request.Token`：客户端传来的 room token 字符串。
- 返回 `claims`，里面有 `PlayerID`、`RoomID`、`ServerID` 等。

`session.SetPlayer(claims.PlayerID, claims.RoomID)`：

- `claims.PlayerID`：把 session 和玩家 ID 绑定。
- `claims.RoomID`：把 session 和房间 ID 绑定。
- 绑定后，后续输入消息才能知道是哪个玩家发来的。

`logic.Player{ID: claims.PlayerID, RoomID: claims.RoomID, Session: session}`：

- `ID`：玩家 ID。
- `RoomID`：玩家目标房间。
- `Session`：玩家连接抽象，用于后续给玩家发送消息。
- 这里传的是接口类型，logic 层不需要知道底层是 KCP。

`manager.JoinRoom(claims.RoomID, player)`：

- `claims.RoomID`：要加入的房间 ID。
- `player`：要加入房间的玩家对象。
- RoomManager 会查找或创建房间，再把玩家投递给房间事件队列。

### 19.7 RoomManager 入房调用链

```text
RoomManager.JoinRoom(roomID, player)
  -> getOrCreateRoom(roomID)
  -> room.Join(player)
  -> playerRooms[player.ID] = roomID
```

`RoomManager.JoinRoom(roomID, player)`：

- `roomID`：目标房间 ID。
- `player`：玩家对象，里面包含玩家 ID、房间 ID 和 session。

`getOrCreateRoom(roomID)`：

- `roomID`：要查找或创建的房间 ID。
- 如果房间已存在，直接返回。
- 如果房间不存在，会检查 `maxRooms`，未超限才创建新房间。

`room.Join(player)`：

- `player`：要加入该房间的玩家。
- 不直接修改房间 map，而是写入 `room.events`，让房间自己的 goroutine 处理。

`playerRooms[player.ID] = roomID`：

- `player.ID`：玩家 ID。
- `roomID`：玩家所在房间。
- 这个映射用于后续输入消息快速找到玩家所在房间。

### 19.8 创建房间调用链

```text
getOrCreateRoom(roomID)
  -> physicsFactory.NewWorld(roomID)
  -> NewRoomWithSync(roomID, maxPlayersPerRoom, tickRate, snapshotRate, aoi, physicsWorld, syncConfig, mapID, physicsHash)
  -> room.Start(ctx)
```

`NewRoomWithSync(roomID, maxPlayersPerRoom, tickRate, snapshotRate, aoi, physicsWorld, syncConfig, mapID, physicsHash)`：

- `roomID`：房间唯一 ID。
- `maxPlayersPerRoom`：该房间最大人数，当前默认 2。
- `tickRate`：房间每秒逻辑更新次数。
- `snapshotRate`：房间每秒快照发送次数。
- `aoi`：AOI 过滤器，用于决定每个玩家能看到谁。
- `physicsWorld`：该房间独立的物理世界，用于移动碰撞、raycast 和物理位置同步。
- `syncConfig`：该房间使用的预测同步配置。
- `mapID` / `physicsHash`：入房响应下发给客户端，用于确定地图和物理数据一致性。

`room.Start(ctx)`：

- `ctx`：房间生命周期 context。
- 内部会启动 `go room.loop(ctx)`。

### 19.9 Room 事件处理调用链

```text
room.loop(ctx)
  -> handleEvent(ctx, event)
  -> handleJoin(ctx, event.player)
  -> handleLeave(ctx, event.playerID)
  -> handleInput(ctx, event.playerID, event.input)
  -> handleInputBatch(ctx, event.playerID, event.inputBatch)
```

`room.loop(ctx)`：

- `ctx`：房间生命周期控制。
- 退出服务时，`ctx.Done()` 会让房间 loop 停止。

`handleEvent(ctx, event)`：

- `ctx`：日志 context。
- `event`：房间事件，可能是加入、离开或输入。

`handleJoin(ctx, event.player)`：

- `ctx`：日志 context。
- `event.player`：要加入房间的玩家对象。
- 这个函数会检查满员、重复加入，并初始化玩家状态。

`handleLeave(ctx, event.playerID)`：

- `ctx`：日志 context。
- `event.playerID`：要离开房间的玩家 ID。
- 这个函数会从房间 `players` map 删除玩家。

`handleInput(ctx, event.playerID, event.input)`：

- `event.playerID`：输入属于哪个玩家。
- `event.input`：玩家输入，包括移动方向、视角、是否开火。
- 兼容旧客户端的单帧输入，会立即转换成一帧同步输入并应用。

`handleInputBatch(ctx, event.playerID, event.inputBatch)`：

- `event.playerID`：输入属于哪个玩家。
- `event.inputBatch`：批量输入，包含多帧客户端输入和可选预测状态。
- 服务端会校验批量大小、输入 tick 窗口和玩家同步模式，再缓存到玩家同步状态中等待 tick 应用。

### 19.10 玩家输入调用链

```text
handlePlayerInput(ctx, session, message)
  -> protocol.DecodeJSON[PlayerInput](message)
  -> manager.PushInput(session.PlayerID(), input)
  -> room.PushInput(playerID, input)

handlePlayerInputBatch(ctx, session, message)
  -> protocol.DecodeJSON[PlayerInputBatch](message)
  -> manager.PushInputBatch(session.PlayerID(), batch)
  -> room.PushInputBatch(playerID, batch)
```

`protocol.DecodeJSON[PlayerInput](message)`：

- `message`：客户端发来的输入消息。
- 泛型类型 `PlayerInput`：表示要解析成玩家输入结构。

`manager.PushInput(session.PlayerID(), input)`：

- `session.PlayerID()`：当前连接绑定的玩家 ID。
- `input`：玩家输入数据。
- RoomManager 会根据 player_id 找到该玩家所在房间。

`room.PushInput(playerID, input)`：

- `playerID`：玩家 ID。
- `input`：玩家输入。
- 它会把输入封装成 `roomEventInput` 写入房间事件队列。

`room.PushInputBatch(playerID, batch)`：

- `playerID`：玩家 ID。
- `batch`：批量输入，最多包含 `max_input_batch_frames` 帧。
- 它会把输入封装成 `roomEventInputBatch` 写入房间事件队列。

### 19.11 Snapshot 发送调用链

```text
Room.update(ctx)
  -> broadcastAcks(ctx)
  -> broadcastSnapshots(ctx)
  -> aoi.FilterVisible(player, players)
  -> protocol.NewJSONMessage(MsgSnapshot, snapshot)
  -> player.Session.Send(message)
  -> protocol.WriteMessage(conn, message, maxPayloadSize)
```

`Room.update(ctx)`：

- `ctx`：日志 context。
- 每个 tick 调用一次。
- 它会递增 `r.tick`，应用当前 tick 可用输入，保存权威历史，校验客户端预测状态，清理超出回滚窗口的同步历史，并判断是否到了发送 ack 和 snapshot 的时机。

`broadcastAcks(ctx)`：

- `ctx`：日志 context。
- 给每个预测同步玩家发送 `MsgInputAck`，包含服务端当前 tick、最后接受输入 tick 和最后校验输入 tick。

`broadcastSnapshots(ctx)`：

- `ctx`：日志 context。
- 为每个玩家单独构造快照，因为每个玩家通过 AOI 看到的对象可能不同。

`aoi.FilterVisible(player, players)`：

- `player`：当前要接收 snapshot 的玩家。
- `players`：房间内全部候选玩家。
- 返回值是当前玩家可见的其他玩家。

`protocol.NewJSONMessage(MsgSnapshot, snapshot)`：

- `MsgSnapshot`：消息类型，告诉客户端这是状态快照。
- `snapshot`：要发送的快照结构，包含 `server_tick` 和玩家状态列表，玩家状态中会携带 `spawn_id`。
- 返回 `protocol.Message`，后续交给 session 发送。

`player.Session.Send(message)`：

- `message`：要发给玩家的业务消息。
- 实际上只是放入该玩家 session 的发送队列，真正写网络由 `writeLoop` 做。

`protocol.WriteMessage(conn, message, maxPayloadSize)`：

- `conn`：KCP 连接，负责把字节写到客户端。
- `message`：业务消息。
- `maxPayloadSize`：最大 payload 限制，避免发送异常大包。

### 19.12 物理 Raycast 调用参数

```text
physics.Raycast(RaycastRequest{
  Origin:      Vector3{X: player.X, Y: player.Y, Z: player.Z},
  Direction:   Vector3{Z: 1},
  MaxDistance: 100,
})
```

`Origin`：

- 射线起点。
- 当前简化为玩家当前位置。
- 后续真实 FPS 应该使用玩家摄像机或枪口位置。

`Direction`：

- 射线方向。
- 当前写死为 `Z: 1`，只是占位。
- 后续应该根据玩家 `Yaw/Pitch` 计算真实朝向。

`MaxDistance`：

- 射线最大检测距离。
- 当前是 100。
- 后续应按武器射程配置。

`Mask`：

- 当前没有传，默认是 0。
- 后续可用来表示检测哪些对象，例如墙体、玩家、可破坏物。

## 20. 如何手动测试当前链路

当前还没有测试客户端。可以先写一个临时 KCP 客户端做联调，步骤是：

1. 启动 roomserver：

```bash
go run ./src/roomserver/cmd
```

2. 客户端用相同密钥生成 room token：

```go
token, err := protocol.GenerateRoomToken(
    "room-token-secret",
    protocol.RoomTokenClaims{
        PlayerID: 1001,
        RoomID:   "room-1001",
        ServerID: "room-01",
        MatchID:  "match-1",
        Nonce:    "test-nonce",
    },
    time.Minute,
)
```

这里 3 个入参含义是：

- `"room-token-secret"`：签名密钥，必须和 roomserver 的 `cfg.TokenSecret` 一致。
- `RoomTokenClaims{...}`：token 携带的业务信息，告诉 roomserver 玩家是谁、要进哪个房间、目标服务是谁。
- `time.Minute`：token 有效期，这里表示 1 分钟后过期。

3. 客户端 KCP 连接：

```text
127.0.0.1:9001
```

4. 发送 `MsgJoinRoom`：

```json
{"token":"上面生成的 token","sync_version":1,"prediction_enabled":true,"physics_hash":"sha256:..."}
```

5. 收到 `MsgJoinRoomAck` 后，旧客户端循环发送 `MsgPlayerInput`，预测客户端循环发送 `MsgPlayerInputBatch`。

6. 客户端应能收到 `MsgInputAck` 和 `MsgSnapshot`；预测误差超阈值时还会收到 `MsgStateCorrection`。

## 21. 客户端预测和服务端权威纠偏

当前 roomserver 已支持第一版预测同步协议：

```text
MsgPlayerInputBatch = 8
MsgInputAck = 9
MsgStateCorrection = 10
```

入房时客户端可在 `JoinRoomRequest` 中声明：

```json
{"sync_version":1,"prediction_enabled":true,"physics_hash":"sha256:..."}
```

服务端会在 `JoinRoomAck` 中下发 `tick_rate`、`snapshot_rate`、`server_time`、`sync_mode`、`map_id`、`physics_hash`、回滚窗口和误差阈值。只有 `sync_mode` 返回 `prediction_authoritative` 时，客户端才应启用本地预测；否则按旧的 `snapshot_only` 模式同步。

服务端处理原则：

- 只信任客户端输入，不信任客户端坐标。
- 每 tick 按输入帧推进权威状态。
- 保存最近 `rollback_window_ticks` 的玩家权威历史。
- 对客户端上报的关键帧预测状态做位置和角度误差校验。
- 超阈值时发送 `StateCorrection`，由客户端回滚重放。
- 迟到太久的输入不会改写服务端历史。

客户端接入细节已拆到：

```text
src/roomserver/CLIENT_PREDICTION_ROLLBACK.md
```

## 22. 当前限制

当前代码还有这些限制：

- 没有统一读取 `config/config.yaml`，当前进程入口仍使用默认配置启动。
- JSON payload 只是第一版调试方案。
- 没有完整客户端测试工具。
- 已接入 `map_001` 的 box 静态地图碰撞，但还没有 mesh、sphere、capsule 和 trigger 区域。
- 没有真实武器、伤害、死亡、结算逻辑。
- 已有服务端侧预测校验和纠偏协议，但客户端预测、插值、回滚和重放仍需要客户端实现。
- 没有房间空闲销毁。
- 没有 token nonce 一次性消费。

## 23. 后续推荐开发顺序

建议按下面顺序继续做，避免一次性铺太大：

1. 写一个最小 KCP 测试客户端。
2. 跑通 `JoinRoom -> JoinRoomAck -> PlayerInputBatch -> InputAck -> Snapshot`。
3. 把 roomserver 配置接入统一 YAML 加载。
4. 增加房间空闲关闭和玩家重复登录处理。
5. 把 JSON payload 替换或兼容 protobuf。
6. 客户端接入预测、插值、回滚和重放。
7. 加入基础移动规则和更完整的服务端校验。
8. 扩展地图 mesh、trigger 区域和 PhysX raycast 命中结算。
9. 实现武器、伤害、死亡、结算。

## 24. 一句话总结

当前 roomserver 的核心链路是：

```text
KCP 连接进入 Session，Session 读到业务消息后交给 Server，Server 校验 token 和同步能力并调用 RoomManager，RoomManager 把玩家和输入投递给 Room，Room 在自己的 tick goroutine 中用 PhysX 推进权威状态、校验客户端预测、必要时发送纠偏，再按 AOI 生成 Ack 和 Snapshot，通过 Session 写回客户端。
```
