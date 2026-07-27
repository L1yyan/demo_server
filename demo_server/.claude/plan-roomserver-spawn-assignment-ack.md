# roomserver 入房出生点编号与客户端通知方案

## 需求理解

玩家进入 roomserver 局内房间时，服务端按照实际入房顺序分配出生点编号：第一个成功进入房间的玩家为 `spawn_a`，第二个为 `spawn_b`。该编号必须与地图出生点位置对应，并在入房成功后告知客户端。

## 已有代码现状

当前工作区已有一部分相关改动：

- `logic.PhysicsWorld` 已包含 `SpawnPoints()`，简化物理后端返回默认 `spawn_a` / `spawn_b`
- PhysX 地图碰撞文件已包含 `spawn_points`
- `logic.Player` 已新增 `SpawnID`
- `Room.handleJoin` 已按未占用出生点分配位置、朝向和 `SpawnID`
- `room_spawn_test.go` 已覆盖两名玩家分配到 `spawn_a` / `spawn_b` 以及离开后复用出生点

因此本次应在现有实现上补齐客户端可见协议字段，并补强测试，不重复重构入房流程。

## 影响范围

预计修改文件：

- `src/roomserver/protocol/message.go`
  - 在 `JoinRoomAck` 中新增 `spawn_id`、出生坐标和初始朝向字段，直接告知当前玩家自己的出生点编号与初始状态
  - 在 `PlayerState` 中新增 `spawn_id`，让后续快照也能携带玩家编号，便于客户端区分对局位置/阵营语义
- `src/roomserver/logic/player.go`
  - `ToState()` 填充 `SpawnID`
- `src/roomserver/logic/room.go`
  - 入房成功 ACK 携带 `spawn_id`、`x/y/z`、`yaw/pitch`
  - 如当前代码存在英文错误信息，保持风格不扩大改动
- `src/roomserver/logic/room_spawn_test.go`
  - 校验 ACK 中包含 `spawn_id` 和出生位置/朝向
  - 校验快照 `PlayerState` 中包含 `spawn_id`

可能按需更新文档：

- `src/roomserver/README.md` 或 `src/roomserver/PHYSX_FLOW.md`
  - 如果现有文档已有入房 ACK 或出生点描述，则补充协议字段说明

## 设计方案

### 协议结构

在 JSON payload 协议中扩展字段，保留原字段不改名：

```go
type JoinRoomAck struct {
    OK      bool    `json:"ok"`
    RoomID  string  `json:"room_id"`
    Content string  `json:"content"`
    Tick    int64   `json:"tick"`
    SpawnID string  `json:"spawn_id"`
    X       float64 `json:"x"`
    Y       float64 `json:"y"`
    Z       float64 `json:"z"`
    Yaw     float64 `json:"yaw"`
    Pitch   float64 `json:"pitch"`
}
```

在 `PlayerState` 中新增：

```go
SpawnID string `json:"spawn_id"`
```

这样客户端入房成功后可以立即从 ACK 得到自己的编号和初始 Transform；后续从 Snapshot 也能继续看到自己和可见玩家的编号。

### 入房流程

保持现有调用链：

```text
service.handleJoinRoom
  -> RoomManager.JoinRoom
  -> Room.Join 投递事件
  -> Room.handleJoin
  -> nextSpawnPoint
  -> physics.AddPlayer
  -> 发送 JoinRoomAck
```

`Room.handleJoin` 在成功加入时构造 ACK：

- `spawn_id = player.SpawnID`
- `x/y/z = player.X/Y/Z`
- `yaw/pitch = player.Yaw/Pitch`

失败 ACK 不填这些字段，JSON 中会出现零值；如果要避免失败消息携带零值，可后续把字段改为 `omitempty`，但本次优先保持协议简单。

### 分配规则

继续使用当前 `nextSpawnPoint()` 规则：

- 从 `physics.SpawnPoints()` 返回顺序扫描
- 跳过空 ID
- 跳过已被房内玩家占用的 `SpawnID`
- 返回第一个未占用出生点

对于当前两人房，地图/简化后端顺序即 `spawn_a`、`spawn_b`，因此满足“第一个进入为 `spawn_a`，第二个为 `spawn_b`”。玩家离开后释放 `SpawnID` 占用，新玩家可复用空出的出生点。

## 兼容性影响

- 不修改消息类型编号，不删除或重命名现有 JSON 字段
- `JoinRoomAck` 和 `PlayerState` 只新增 JSON 字段，对能忽略未知字段的客户端是向后兼容的
- 如果客户端当前使用严格 schema，需要同步更新客户端结构体，否则会因为新增字段触发校验失败；这是唯一兼容性风险
- 入房失败语义不变，仍通过 `ok=false` 与 `content` 表达失败原因

## 健壮性

- 房间满、重复入房、没有可用出生点、物理 AddPlayer 失败时继续返回失败 ACK
- 只有物理玩家创建成功后才写入 `r.players`，避免服务端记录了玩家但物理世界没有 actor
- 出生点列表为空或 ID 为空时拒绝入房，避免分配无意义编号
- 如果后续支持更多出生点，当前顺序扫描策略可以自然扩展，不需要改入房主流程

## 性能考虑

- 入房不是每帧高频路径，`nextSpawnPoint()` 每次按房内玩家数构建小 map，当前两人房开销可忽略
- 快照新增一个字符串字段会增加少量 JSON payload；当前两人房和 AOI 快照规模很小，影响可忽略
- 不增加跨语言 PhysX 调用；出生点已经在物理世界创建时加载到 Go 内存

## 验证方式

计划执行：

```bash
go test ./src/roomserver/logic ./src/roomserver/protocol
```

如改动触及 PhysX 相关包或编译依赖允许，再执行：

```bash
go test ./src/roomserver/...
```

如果 PhysX/cgo 环境缺依赖导致全量测试无法运行，会如实说明失败原因，并至少保证 logic/protocol 层测试通过。

## 自我审查

检查结果：

1. 已复用现有 `SpawnPoint`、`SpawnID`、`PhysicsWorld.SpawnPoints()` 和 `Room.handleJoin` 结构，不新增无关抽象
2. 不改 service/logic 分层边界；service 仍只解析 token 并调用 logic，分配出生点仍在 logic 层
3. 协议只增字段，不改变旧字段含义或消息类型编号，兼容性风险可控
4. 当前 `RoomManager.JoinRoom` 会在入房事件真正处理前记录 `playerRooms`，如果后续 room 内失败，manager 映射会短暂保留；这不是本需求新增问题，本次不扩大重构，但可以在后续将 JoinRoom 做成可返回异步结果
5. `nextSpawnPoint()` 依赖 `SpawnPoints()` 顺序，因此地图配置必须把 `spawn_a` 放在 `spawn_b` 前；当前配置和简化后端都满足
6. 不引入 per tick 额外计算或 cgo 调用

## 修正后的最终方案

本次只补齐客户端通知和协议状态同步：在 `JoinRoomAck` 中返回 `spawn_id` 与初始坐标/朝向，在 `PlayerState` 中返回 `spawn_id`，并补充单元测试验证第一个玩家收到 `spawn_a`、第二个收到 `spawn_b`。保留当前已存在的出生点分配实现，不做额外架构调整。

等待用户确认后开始修改业务代码。
