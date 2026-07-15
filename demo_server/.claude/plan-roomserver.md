# roomserver 第一版实现方案与基础讲解

## 0. 先说结论

第一版 roomserver 不建议一口气做成完整 FPS 战斗服。更稳的路线是先做一个“可启动、可编译、边界清楚、后续能扩展”的房间服务器骨架：

1. 客户端通过 KCP 连接 roomserver。
2. 客户端携带短期 room token 请求入房。
3. roomserver 校验 token，确认玩家是否允许进入指定房间。
4. 每个房间由一个 goroutine 运行固定 tick 循环。
5. 玩家输入进入房间队列，由房间 tick 统一处理。
6. 服务端生成权威状态快照，再按 AOI 过滤后发给玩家。
7. Raycast 和 PhysX 先做接口占位，后续再接真实物理引擎。

这样做的核心目的不是“少做功能”，而是先把网络、房间、状态同步、物理、AOI 的边界定好，避免后面所有逻辑纠缠在一起。

## 1. 需求理解

你描述的完整链路是：

```text
客户端
  -> nginx
  -> logic_server_01 / logic_server_02
  -> 登录成功
  -> logic 通过 RPC 请求 matchserver
  -> matchserver 分配 roomserver + room_id
  -> matchserver 生成入房 token
  -> logic 返回 token 给客户端
  -> 客户端使用 token 直连 roomserver
  -> roomserver 运行 FPS 10 人乱斗局内逻辑
```

本次你确认的第一版范围是：

- 先直接做 roomserver。
- 第一版做到骨架可编译。
- 网络传输优先用 KCP。
- PhysX 暂时只做接口占位。
- 后续再接 matchserver、logic 联调、真实物理和完整玩法。

## 2. 为什么 roomserver 要单独做

logicserver 通常负责账号、登录、背包、匹配入口、HTTP/gRPC 接口这类“请求响应型”业务。

roomserver 不一样，它是“长连接 + 高频实时计算型”服务：

- 玩家连接会持续存在。
- 每秒要处理多次输入和状态同步。
- 房间内部需要固定 tick 运行。
- 网络延迟、丢包、乱序会直接影响体验。
- 射击、碰撞、AOI 都是高频逻辑。

所以 roomserver 不适合混进 logicserver。它应该是独立进程、独立端口、独立日志、独立配置。

## 3. 当前项目现状

当前仓库中已有：

- `pkg/glog`：日志库，可以直接给 roomserver 用。
- `pkg/jwt`：登录 JWT 工具，但不建议直接拿来当入房 token 的业务语义。
- `pb/logic/logic.proto`：logic 登录、注册、验证码协议。
- `src/logicserver`：目前只有很轻的启动示例。
- `config/config.yaml`：已有 logic 相关配置示例。

当前仓库中还没有：

- matchserver。
- roomserver。
- 房间协议。
- 游戏 tick。
- KCP/UDP 网络层。
- AOI 模块。
- 物理模块。
- 配置加载主流程。

因此第一版 roomserver 会以独立目录新增，不强行改已有 logicserver。

## 4. 基础概念讲解

### 4.1 UDP 是什么

UDP 是一种网络传输协议。它的特点是：

- 延迟低。
- 没有连接概念。
- 不保证消息一定送达。
- 不保证消息顺序。
- 不自动重传。
- 不自动拥塞控制。

游戏实时同步常用 UDP，因为 FPS 对延迟很敏感。但是裸 UDP 太底层，很多事情都要自己做，例如：

- 怎么知道客户端是谁。
- 消息丢了要不要补发。
- 消息乱序怎么处理。
- 客户端断线怎么判断。
- 如何避免短时间发太多包。

所以第一版直接手写裸 UDP 成本比较高。

### 4.2 TCP 为什么不适合多数实时战斗

TCP 的优点是可靠、有序、自动重传。但问题也来自这里。

假设客户端连续收到服务端快照：

```text
snapshot 100
snapshot 101
snapshot 102
snapshot 103
```

如果 `snapshot 100` 在网络中丢了，TCP 会等待它重传。即使 `101/102/103` 已经到了，也不会先交给应用层。这叫队头阻塞。

对于实时游戏，旧状态价值很低。很多时候玩家更需要最新状态，而不是等一个过期状态补回来。

所以纯 TCP 做 FPS 状态同步容易出现卡顿感。

### 4.3 KCP 是什么

KCP 是构建在 UDP 之上的可靠传输协议。可以把它理解成：

```text
UDP 提供低延迟基础
KCP 在 UDP 上补了一层可靠、有序、重传、拥塞控制
```

它常用于游戏、实时音视频、弱网通信。

KCP 的优点：

- 比 TCP 更适合调低延迟。
- 可以通过参数控制重传和拥塞策略。
- 对应用层来说更像一个连接，使用成本比裸 UDP 低。
- 第一版可以先用 KCP 把链路跑通。

KCP 的缺点：

- 它仍然有可靠有序语义，如果所有游戏消息都走同一条 KCP 流，也可能因为某个包丢失造成后续消息等待。
- 高频快照其实不一定需要可靠传输，因为旧快照丢了也没必要补。
- 后续性能优化时，可能需要拆成双通道：可靠通道处理登录、入房、结算；不可靠 UDP 通道处理高频输入和快照。

第一版推荐 KCP 的原因：

- 你现在要先把 roomserver 架起来。
- KCP 能省掉大量裸 UDP 会话管理细节。
- 第一版房间人数只有 10 人，先用 KCP 足够验证架构。
- 等玩法链路稳定后，再考虑 KCP + 裸 UDP 双通道。

### 4.4 Tick 是什么

游戏服务器通常不是“玩家发一个消息就立刻完整计算一次世界”，而是按固定频率循环计算。

例如 tick_rate = 20，表示每秒计算 20 次：

```text
1 秒 / 20 = 50ms
```

也就是每 50ms 房间更新一次。

房间 tick 每次做类似这些事情：

```text
读取这一帧收到的玩家输入
更新玩家位置和状态
处理开火、命中、伤害
计算 AOI
生成状态快照
发送给客户端
```

这样做的好处：

- 所有玩家状态按统一节奏更新。
- 房间逻辑更可控。
- 不会因为某个玩家发包特别频繁就打乱服务器节奏。
- 方便后续做回放、调试、反作弊和性能统计。

### 4.5 状态同步是什么

多人实时游戏常见同步方式有两类：

1. 帧同步。
2. 状态同步。

你现在说的是状态同步。

状态同步的意思是：

- 客户端上报输入。
- 服务端计算权威状态。
- 服务端把状态快照发给客户端。
- 客户端根据服务端状态显示画面。

例如客户端上报：

```text
我按下 W
我的视角朝向 yaw=30 pitch=5
我点击开火
```

服务端不应该完全相信客户端说的最终位置或命中结果。服务端应该自己计算：

```text
玩家是否能移动
这一帧移动到哪里
射线是否命中目标
目标是否扣血
```

这样可以减少作弊风险。

第一版骨架只需要把“输入上报 -> 房间处理 -> 状态快照”的链路建好，不需要立即实现完整移动和射击。

### 4.6 Snapshot 是什么

Snapshot 就是服务端某一刻的世界状态快照。

例如：

```json
{
  "server_tick": 1024,
  "players": [
    {"player_id": 1001, "x": 1.2, "y": 0, "z": 5.8, "hp": 80},
    {"player_id": 1002, "x": 2.0, "y": 0, "z": 7.1, "hp": 100}
  ]
}
```

客户端收到后，用它来更新画面。

注意：snapshot 不一定每 tick 都发。比如：

- 房间 tick 20Hz。
- snapshot 10Hz。

意思是服务器每秒计算 20 次，但每秒只发 10 次快照。这样可以降低网络压力。

### 4.7 AOI 是什么

AOI 是 Area Of Interest，意思是“兴趣区域”。

在游戏中，它决定“某个玩家应该收到哪些对象的信息”。

如果房间有 10 个人，并不是每个玩家都一定要知道所有人的完整信息。比如：

- 太远的人可以不发。
- 背后的人可以低频发或不发。
- 被墙挡住的人可以不发精确信息。
- 已死亡或不可见对象可以按规则过滤。

AOI 的作用：

- 降低网络流量。
- 降低客户端处理压力。
- 减少外挂通过网络包获取全图信息的风险。

你提到“按照视野，向前贯穿地图进行 AOI”，这接近 FPS 中的视锥和遮挡判断。

第一版建议把 AOI 拆成两步：

```text
粗筛：距离、房间、状态
细筛：视野角度、遮挡 raycast
```

10 人乱斗第一版可以先简单遍历所有玩家，因为人数很少，不需要一开始就做九宫格、四叉树、BVH 等空间索引。

### 4.8 Raycast 是什么

Raycast 可以理解成“从一个点沿一个方向发出一条射线，看它碰到了什么”。

FPS 射击里很常见。

例如玩家开枪：

```text
起点：玩家摄像机位置
方向：玩家准星朝向
距离：100 米
```

服务器用 raycast 问物理世界：

```text
这条线最先打到了什么？
是墙？
是玩家？
命中点在哪里？
命中法线是什么？
```

如果是 hitscan 武器，比如步枪、手枪、狙击枪，通常可以认为子弹瞬间到达，这时 raycast 很适合。

如果是有飞行时间的子弹，比如火箭弹、榴弹，就不是单次 raycast 这么简单，而是需要模拟 projectile 的位置、速度、碰撞。

你说“子弹射击为实时到达”，所以第一版理解为 hitscan。后续真实实现时，开火逻辑大概是：

```text
收到玩家 Fire 输入
校验玩家是否能开火
根据玩家位置和视角构造射线
调用 Physics.Raycast
如果命中玩家，计算伤害
广播命中事件和状态变化
```

### 4.9 PhysX 是什么

PhysX 是 NVIDIA 的物理引擎。它可以做：

- 碰撞检测。
- 刚体模拟。
- 射线检测。
- 形状查询。
- 角色控制器。
- 场景物理模拟。

在 roomserver 中，PhysX 可能用于：

- 地图碰撞。
- 玩家胶囊体碰撞。
- 射击 raycast。
- 爆炸范围检测。
- 物理道具。

但 PhysX 是 C++ 引擎。Go 项目要接 PhysX，通常需要 cgo 或者独立物理进程。

这会带来几个问题：

1. 构建复杂。
   - 需要 PhysX SDK。
   - 需要 C++ 编译环境。
   - 需要处理动态库或静态库。

2. 调试复杂。
   - C++ 崩溃可能直接带崩 Go 进程。
   - 内存生命周期要非常小心。

3. 性能边界复杂。
   - Go 调 C++ 有跨语言调用成本。
   - 不能每 tick 对每个对象都频繁单次调用。
   - 应该尽量批量调用，例如 BatchRaycast。

4. 数据同步复杂。
   - Go 里有玩家状态。
   - PhysX 里也有物理对象状态。
   - 两边状态如何同步，需要设计清楚。

因此第一版不建议马上接 PhysX。更好的做法是：

```go
定义 PhysicsWorld 接口
第一版用 SimplePhysicsWorld 占位
后续用 PhysXPhysicsWorld 替换实现
```

这样 room 逻辑只依赖接口，不关心底层是简化 Go 实现还是 PhysX。

## 5. 第一版影响范围

预计新增目录和文件：

- `src/roomserver/cmd/main.go`
  - roomserver 启动入口。

- `src/roomserver/config/config.go`
  - roomserver 配置结构和默认配置。

- `src/roomserver/service/server.go`
  - KCP 监听、服务启动、服务关闭。

- `src/roomserver/service/session.go`
  - 单个客户端连接会话。

- `src/roomserver/logic/room_manager.go`
  - 房间管理器。

- `src/roomserver/logic/room.go`
  - 单房间 tick 循环。

- `src/roomserver/logic/player.go`
  - 玩家局内状态。

- `src/roomserver/logic/aoi.go`
  - AOI 接口和占位实现。

- `src/roomserver/logic/physics.go`
  - 物理接口和占位实现。

- `src/roomserver/protocol/message.go`
  - 消息类型、帧编码、帧解码。

- `src/roomserver/protocol/token.go`
  - room token claims、签发、校验。

- `pb/room/room.proto`
  - roomserver 消息协议定义，为后续生成代码和客户端对接做准备。

预计修改：

- `go.mod` / `go.sum`
  - 新增 `github.com/xtaci/kcp-go/v5`。

- `config/config.yaml`
  - 增加 roomserver 配置示例。

可能暂不修改：

- `config/config.go`
  - 当前项目没有完整配置加载器，第一版可以先让 `src/roomserver/config` 独立提供默认配置。

- `src/logicserver`
  - 本次不接 logic 到 roomserver 的完整链路。

- `pb/logic/logic.proto`
  - 本次不新增登录接口字段，不破坏现有协议。

## 6. 分层设计

按照项目要求，服务端代码要遵守 repo、logic、service 分层。

roomserver 第一版这样拆：

```text
cmd
  负责启动进程

service
  负责网络连接、KCP session、参数基础校验、调用 logic

logic
  负责房间、玩家、tick、AOI、物理调用、状态同步

protocol
  负责消息编码、解码、token 结构

config
  负责 roomserver 自身配置
```

第一版没有 repo 层，因为暂时不写数据库。

以后如果要保存战绩、回放、房间日志，可以新增：

```text
src/roomserver/repo
```

repo 只负责持久化，不应该处理 tick 或网络连接。

## 7. 网络设计

### 7.1 KCP 监听

roomserver 启动后监听一个 UDP 端口，例如：

```text
0.0.0.0:9001
```

客户端连接这个地址后，服务端为它创建一个 session。

### 7.2 Session 是什么

Session 是服务端眼中的“一个客户端连接”。

它保存：

- 连接对象。
- 玩家 ID。
- 房间 ID。
- 最后心跳时间。
- 发送队列。
- 是否关闭。

每个 session 运行两个主要 goroutine：

```text
read loop：不断读客户端消息
write loop：不断写服务端消息给客户端
```

read loop 读到消息后，不直接修改房间状态，而是调用 service/logic，把输入投递给房间。

write loop 从发送队列里取消息，写回客户端。

### 7.3 为什么发送队列要有限制

如果某个客户端网络很差，服务端一直给它发消息，但它收不动，就会堆积越来越多待发送数据。

如果发送队列无限大，可能导致内存持续上涨，最后拖垮整个 roomserver。

所以第一版要设置上限，例如：

```text
write_queue_size = 128
```

队列满了就关闭该 session 或丢弃低优先级消息。

第一版为了简单，队列满可以直接断开连接。

## 8. 消息协议设计

KCP 只负责传输字节，不负责告诉你“这一段字节是什么业务消息”。所以我们需要业务帧格式。

第一版建议：

```text
uint16 message_type
uint32 payload_length
bytes payload
```

含义：

- `message_type`：消息类型，比如入房、心跳、输入、快照。
- `payload_length`：后面 payload 有多少字节。
- `payload`：具体业务内容。

第一版消息：

- `MsgJoinRoom`
  - 客户端请求入房，携带 room token。

- `MsgJoinRoomAck`
  - 服务端返回入房是否成功。

- `MsgHeartbeat`
  - 客户端心跳。

- `MsgHeartbeatAck`
  - 服务端心跳响应。

- `MsgPlayerInput`
  - 客户端输入，例如移动、视角、开火。

- `MsgSnapshot`
  - 服务端状态快照。

- `MsgError`
  - 服务端错误消息。

payload 第一版可以先用 JSON。

为什么第一版用 JSON：

- 方便人眼查看和调试。
- 不用一开始处理 proto 生成和客户端解析。
- 骨架阶段性能压力不大。

为什么后续可能要换 protobuf：

- JSON 字段名占空间。
- JSON 编解码 CPU 开销更高。
- protobuf 更适合固定协议和跨语言客户端。

第一版会把编码逻辑集中在 protocol 包，后续从 JSON 换 protobuf 时，logic 层不用大改。

## 9. 入房 token 设计

### 9.1 为什么不能直接用登录 token

登录 token 证明的是：

```text
这个用户已经登录
```

room token 证明的是：

```text
这个玩家被匹配服务允许进入某个 roomserver 的某个 room
```

它们语义不同。

如果直接拿登录 token 入房，会有几个问题：

- roomserver 不知道玩家应该进哪个房间。
- 无法限制 token 只能用于某台 roomserver。
- 无法限制 token 很短时间内有效。
- 无法防止玩家拿旧 token 重复入房。

所以第一版单独定义 room token。

### 9.2 RoomTokenClaims

建议字段：

```text
player_id   玩家 ID
room_id     房间 ID
server_id   目标 roomserver ID
match_id    匹配批次或对局 ID
expires_at  过期时间
nonce       随机串
```

校验时检查：

- token 是否为空。
- 签名是否正确。
- 是否过期。
- server_id 是否等于当前 roomserver。
- room_id 是否存在或可创建。
- player_id 是否有效。

第一版可以先不做 nonce 一次性消费。后续接 Redis 后，可以用 Redis 记录 nonce 是否已使用，避免同一个 token 重放。

## 10. 房间设计

### 10.1 RoomManager

RoomManager 管所有房间：

```text
room_id -> Room
```

职责：

- 创建房间。
- 查找房间。
- 玩家加入房间。
- 玩家离开房间。
- 关闭房间。

RoomManager 可以有一把 mutex 保护 map。

注意：RoomManager 不应该直接计算玩家移动、射击、AOI。这些属于 Room 内部逻辑。

### 10.2 Room

Room 是一局游戏。

字段大概包括：

```text
room_id
players
max_players
tick_rate
snapshot_rate
input_channel
join_channel
leave_channel
stop_channel
```

Room 启动后有一个 goroutine 跑 loop：

```text
for 每个 tick:
  处理加入/退出事件
  处理玩家输入
  更新世界状态
  处理射击和物理
  计算 AOI
  发送快照
```

### 10.3 为什么一个房间一个 goroutine

因为房间内状态共享很多：

- 玩家位置。
- 血量。
- 开火状态。
- 子弹/命中事件。
- 房间 tick。

如果每个玩家、每个系统都开 goroutine 直接改状态，就需要很多锁，容易出现：

- 数据竞争。
- 死锁。
- 状态顺序不确定。
- 问题难复现。

第一版一个房间一个 goroutine，所有操作进入房间队列，逻辑更清楚。

对于 10 人小房间，这个模型足够。

## 11. 状态同步设计

### 11.1 客户端发输入

客户端不应该直接告诉服务端：

```text
我的位置是 x=100 y=0 z=200
我打中了 player_2
```

因为这很容易作弊。

客户端应该发：

```text
我当前按了 W/A/S/D
我的视角方向是 yaw/pitch
我在 client_tick=123 开火
```

服务端根据输入计算结果。

### 11.2 服务端发快照

服务端发给客户端的是权威结果：

```text
server_tick
可见玩家列表
自己的状态
命中事件
血量变化
```

第一版 snapshot 可以很简单，只要把链路打通即可。

后续再优化：

- delta snapshot：只发变化。
- 压缩坐标。
- 客户端插值。
- 客户端预测和服务端校正。
- 延迟补偿。

## 12. AOI 设计

第一版 AOI 接口：

```text
FilterVisible(self, candidates) -> visible players
```

占位逻辑：

1. 排除自己。
2. 排除死亡玩家。
3. 距离超过最大可见距离则排除。
4. 不在视野角度内则排除。
5. 遮挡检测先留接口。

后续真实 FPS AOI 可以继续增强：

- 地图分区。
- 门、墙、楼层遮挡。
- 视锥裁剪。
- 听觉范围。
- 战斗事件强制同步，例如附近枪声。
- 反作弊可见性控制。

## 13. 射击与物理设计

第一版物理接口：

```text
Raycast(request) -> hit
BatchRaycast(requests) -> hits
```

RaycastRequest 包含：

```text
origin      起点
direction   方向
max_distance 最大距离
mask        检测哪些对象，例如墙、玩家、道具
```

RaycastHit 包含：

```text
hit          是否命中
target_id    命中的对象 ID
point        命中点
normal       命中面法线
distance     命中距离
```

第一版用 SimplePhysicsWorld 占位。

后续接 PhysX 时，替换为 PhysXPhysicsWorld。

关键原则：

- room 逻辑不要直接调用 cgo。
- room 逻辑只依赖 PhysicsWorld 接口。
- 高频 raycast 后续要批量调用。
- PhysX 对象生命周期集中管理，不散落在业务逻辑里。

## 14. 配置设计

第一版 roomserver 配置建议：

```yaml
room_server_01:
  server_id: "room-01"
  listen_addr: ":9001"
  token_secret: "room-token-secret"
  max_rooms: 1000
  max_players_per_room: 10
  tick_rate: 20
  snapshot_rate: 10
  read_timeout: "10s"
  write_queue_size: 128
```

字段说明：

- `server_id`
  - 当前 roomserver 的唯一 ID。
  - room token 里也会带 server_id，防止 token 被拿到别的服务器使用。

- `listen_addr`
  - KCP 监听地址。

- `token_secret`
  - room token 签名密钥。
  - 后续 matchserver 和 roomserver 要共享或通过更安全方式管理。

- `max_rooms`
  - 单进程最大房间数。

- `max_players_per_room`
  - 每个房间最大人数，本玩法是 10。

- `tick_rate`
  - 房间每秒计算多少次。

- `snapshot_rate`
  - 每秒给客户端发多少次状态快照。

- `read_timeout`
  - 多久没有消息认为连接异常。

- `write_queue_size`
  - 每个 session 的发送队列上限。

当前项目还没有完整配置加载器，因此第一版可以先在 roomserver config 包提供默认值，main 中直接使用默认配置。配置文件示例可以先加上，后面再统一接入 yaml 加载。

## 15. 错误处理

第一版至少处理这些情况：

- KCP 监听失败：启动直接失败。
- accept session 失败：记录日志，服务继续运行。
- 消息头不完整：关闭连接。
- payload 长度超过上限：关闭连接，防止恶意大包。
- JSON 解析失败：返回错误消息或关闭连接。
- token 为空：拒绝入房。
- token 过期：拒绝入房。
- token 签名错误：拒绝入房。
- token server_id 不匹配：拒绝入房。
- 房间满员：拒绝入房。
- 玩家重复入房：按规则拒绝或踢掉旧连接，第一版建议拒绝。
- 发送队列满：关闭慢连接。
- 房间 goroutine panic：记录日志并关闭房间。

## 16. 性能考虑

第一版性能重点不是极致优化，而是避免明显错误。

建议：

- tick 默认 20Hz，不要第一版直接 60Hz。
- snapshot 默认 10Hz，不要每 tick 都发完整快照。
- 10 人房间直接遍历玩家做 AOI，足够简单可靠。
- 高频 tick 内不打 info 日志。
- 每个 session 发送队列有上限。
- payload 设置最大长度，例如 64KB 或更低。
- PhysX 后续必须批量调用。

未来可能要优化：

- protobuf 替换 JSON。
- 快照 delta 压缩。
- 输入消息合并。
- AOI 空间索引。
- 物理批量查询。
- 多 room 分 shard 调度。
- 热房间监控和迁移。

## 17. 安全与反作弊考虑

第一版只做基础边界，但设计时要预留：

- 不信任客户端位置。
- 不信任客户端命中结果。
- token 短期有效。
- token 限定 server_id 和 room_id。
- payload 长度限制。
- 输入频率限制。
- 心跳超时。
- AOI 不给客户端发不该知道的对象。

后续更完整的反作弊可以做：

- 移动速度校验。
- 开火频率校验。
- 视角变化异常检测。
- 命中合法性校验。
- 延迟补偿边界限制。
- 服务器回放审计。

## 18. 兼容性影响

第一版不会破坏现有功能：

- 不修改 logic 登录接口。
- 不修改现有 JWT 访问令牌字段。
- 不修改 MongoDB/Redis 包。
- 不强制已有服务接入 roomserver。

会新增：

- roomserver 独立代码。
- KCP 依赖。
- room proto。
- roomserver 配置示例。

## 19. 验证方式

实现后执行：

```bash
gofmt -w src/roomserver/**/*.go
```

格式化新增 Go 文件。

```bash
go mod tidy
```

拉取并整理 KCP 依赖。

```bash
make proto
```

如果新增 `pb/room/room.proto`，验证 protobuf 生成。

```bash
go test ./...
```

验证全仓编译和测试。

```bash
go run ./src/roomserver/cmd
```

验证 roomserver 能启动监听。

如果 `go test ./...` 因已有代码问题失败，需要如实说明失败点，不把它说成通过。

## 20. 分阶段路线

### 阶段 1：roomserver 骨架

目标：服务能启动、能监听 KCP、基础结构可编译。

包含：

- config 默认配置。
- server 生命周期。
- session read/write loop。
- protocol 帧编码。
- room manager。
- room tick loop。

### 阶段 2：入房链路

目标：客户端能带 token 入房。

包含：

- room token claims。
- token 签发/校验工具。
- JoinRoom 消息。
- JoinRoomAck 消息。
- 房间满员处理。

### 阶段 3：输入与快照

目标：客户端能发输入，服务端能广播简化状态。

包含：

- PlayerInput 消息。
- Snapshot 消息。
- 玩家位置占位更新。
- tick + snapshot rate 分离。

### 阶段 4：AOI 与物理接口

目标：逻辑边界可替换。

包含：

- AOI 接口。
- 简化可见性过滤。
- PhysicsWorld 接口。
- SimplePhysicsWorld 占位。

### 阶段 5：接 matchserver 和 logic

目标：跑通完整外部链路。

包含：

- matchserver 分配房间。
- matchserver 签发 room token。
- logic 调 matchserver。
- 客户端从 logic 拿 token 后连接 roomserver。

### 阶段 6：真实玩法和 PhysX

目标：进入真正 FPS 玩法验证。

包含：

- 移动规则。
- 武器系统。
- hitscan 射击。
- 伤害和死亡。
- PhysX raycast。
- 延迟补偿。
- 回放和战斗日志。

## 21. 自我审查

### 21.1 是否过度设计

第一版没有直接做完整玩法、matchserver、PhysX、数据库和配置加载器，避免范围过大。

### 21.2 是否太简单

第一版虽然不做真实玩法，但会把关键边界建出来：KCP、session、room、tick、protocol、token、AOI、physics 接口。这些是后续功能的地基，不是临时代码。

### 21.3 是否符合项目分层

符合。service 只处理网络和基础校验，logic 处理房间业务，protocol 处理编码和 token，第一版没有 repo。

### 21.4 是否存在协议风险

room proto 是新增，不影响现有 logic proto。字段会按 snake_case，并添加简短中文注释。

### 21.5 是否存在性能风险

JSON 和 KCP 单通道不是最终高性能方案，但第一版房间人数少、目标是骨架可编译，可以接受。后续通过 protocol 封装替换编码，通过双通道优化实时同步。

### 21.6 是否存在 PhysX 接入风险

有，所以第一版不接真实 PhysX，只定义接口。后续接 PhysX 时优先批量调用，避免频繁 cgo。

### 21.7 是否有更简单的做法

可以只写一个 UDP echo server，但那对后续房间、AOI、物理、状态同步帮助不大。当前方案是“最小可扩展骨架”，比 echo server 更贴近最终目标。

## 22. 修正后的最终方案

第一版按以下内容实现：

1. 新增 `src/roomserver`，按 `cmd/config/service/logic/protocol` 拆分。
2. 使用 `github.com/xtaci/kcp-go/v5` 实现 KCP 监听。
3. 实现 session read/write loop 和有限发送队列。
4. 实现轻量帧协议：消息类型 + payload 长度 + payload。
5. 第一版 payload 使用 JSON，协议层封装，后续可替换 protobuf。
6. 实现 room token claims、签发和校验工具，但不接 matchserver。
7. 实现 RoomManager 和 Room，每个房间一个 tick goroutine。
8. 实现 JoinRoom、Heartbeat、PlayerInput、Snapshot、Error 消息骨架。
9. 实现 AOI 接口和简化可见性过滤。
10. 实现 PhysicsWorld 接口和 SimplePhysicsWorld 占位。
11. 新增 `pb/room/room.proto`，按项目 proto 注释规范写清楚。
12. 新增 roomserver 配置示例。
13. 执行 `gofmt`、`go mod tidy`、`make proto`、`go test ./...` 验证。

等待你确认后，再开始修改业务代码。
