# roomserver 玩家按地图出生点 A/B 入场方案

## 1. 需求理解

当前 `configs/maps/map_001/collision.json` 中已经有两个出生点：`spawn_a` 和 `spawn_b`。用户要求两名玩家进入同一个房间时分别出生在 A 和 B，而不是都使用当前默认的 `(0,0,0)`。

目标行为：

- 第 1 名进入房间的玩家使用 `spawn_a`
- 第 2 名进入房间的玩家使用 `spawn_b`
- 玩家离开后释放对应出生点，新加入玩家可复用空出的出生点
- 当前只处理这个 2 人房间、单地图场景，不引入队伍、座位或多地图匹配协议

## 2. 影响范围

预计修改：

- `src/roomserver/logic/physics.go`
  - 增加 `SpawnPoint` 类型
  - 在 `PhysicsWorld` 接口中增加获取地图出生点的方法
  - `SimplePhysicsWorld` 提供默认 A/B 出生点，保证 simple 后端也能工作
- `src/roomserver/logic/player.go`
  - 给 `Player` 增加 `SpawnID` 字段，用于记录占用的出生点
- `src/roomserver/logic/room.go`
  - 玩家入房时先分配可用出生点，再初始化 `Player.X/Y/Z/Yaw/Pitch`
  - `AddPlayer` 使用分配后的出生坐标创建 PhysX 玩家 capsule
- `src/roomserver/physx/map_collision.go`
  - 将地图 JSON 中的 `spawn_points` 转换为 logic 层可用的出生点
  - 从 Unity 四元数中提取 yaw，作为玩家初始朝向
- `src/roomserver/physx/world.go`
  - `World` 保存加载到的出生点，并通过 `PhysicsWorld` 接口返回
- `src/roomserver/logic/*_test.go`
  - 增加房间内两名玩家分别占用 `spawn_a` / `spawn_b` 的测试
  - 增加玩家离开后出生点可复用的测试
- `src/roomserver/physx/*_test.go`
  - 验证 PhysX map loader 能从 JSON 中读出 spawn points
- `src/roomserver/README.md` / `src/roomserver/PHYSX_FLOW.md`
  - 更新出生点接入说明

## 3. 设计方案

### 3.1 出生点模型

在 logic 层定义通用出生点类型：

```go
type SpawnPoint struct {
    ID       string
    Position Vector3
    Yaw      float64
}
```

`ID` 对应 JSON 中的 `spawn_a`、`spawn_b`；`Position` 对应 JSON 的 `position`；`Yaw` 从 JSON 的四元数 `rotation` 中提取。

### 3.2 通过 PhysicsWorld 暴露地图出生点

现有架构中，logic 层只依赖 `PhysicsWorld` 接口，不直接依赖 `physx` 包。为保持这个边界，在接口上增加：

```go
SpawnPoints() []SpawnPoint
```

PhysX 后端在加载 `collision.json` 时保存 spawn points；Room 只通过接口读取，不知道这些数据来自 JSON 还是其他后端。

### 3.3 房间内出生点分配规则

`Room.handleJoin` 中增加分配流程：

```text
校验房间未满、玩家未重复
-> 选择未被占用的出生点
-> 写入 player.X/Y/Z/Yaw/Pitch/SpawnID
-> physics.AddPlayer(player.ID, spawn.Position)
-> 加入 r.players
-> 返回 JoinRoomAck
```

选择规则：

1. 按 `SpawnPoints()` 返回顺序遍历
2. 跳过当前房间内已被玩家占用的 `SpawnID`
3. 第一个可用出生点分配给新玩家
4. 如果没有可用出生点，拒绝入房并返回 `spawn point not available`

当前 JSON 顺序是 `spawn_a` 在前、`spawn_b` 在后，所以第 1 名玩家拿 A，第 2 名玩家拿 B。

### 3.4 玩家离开后的复用

玩家离开时已有 `delete(r.players, playerID)`。因为出生点占用通过房间内现存玩家的 `SpawnID` 计算，所以删除玩家后该出生点自然释放，不需要额外维护一个容易失配的占用表。

### 3.5 初始朝向

`spawn_a` 的 rotation 是 `[0,0,0,1]`，解析后 yaw 为 `0`。

`spawn_b` 的 rotation 是 `[0,1,0,0]`，解析后 yaw 为 `180` 或 `-180`。

这样两名玩家不只是位置不同，也能按 Unity 出生点朝向初始化 `Player.Yaw`。

## 4. 兼容性影响

- 客户端协议不变，不需要新增 JoinRoom 字段
- room token 不变，不需要加入座位或 spawn ID
- Snapshot 协议不变，客户端仍通过已有 `x/y/z/yaw/pitch` 获得初始位置和朝向
- 服务端行为会变化：入房成功后玩家初始坐标不再是 `(0,0,0)`，而是地图出生点
- 如果地图没有足够出生点，第二名玩家会入房失败；当前 `map_001` 已有 `spawn_a` 和 `spawn_b`

## 5. 健壮性

- `SpawnPoints()` 返回拷贝，避免外部修改 PhysX world 内部数据
- 出生点 ID 为空、position/rotation 非法时继续由地图加载校验拦截
- 分配出生点时按当前房间存活的玩家占用情况计算，玩家离开后自动释放
- 物理 actor 创建失败时仍保持现有逻辑：玩家不会加入 `r.players`
- 如果没有可用出生点，明确返回入房失败，不静默退回 `(0,0,0)`，避免两个玩家重叠或出生到错误位置

## 6. 性能考虑

- 房间最多 2 人，出生点数量也很少，入房时遍历玩家和出生点开销可以忽略
- 出生点只在 world 创建时从 JSON 转换一次，不在每帧解析
- `SpawnPoints()` 返回拷贝只发生在入房阶段，不在 tick 高频路径
- 不增加额外 goroutine、锁竞争或网络开销

## 7. 验证方式

实现后执行：

1. `gofmt` 格式化修改过的 Go 文件
2. `go test ./src/roomserver/...`
   - 验证 simple 后端和 room 逻辑测试
3. `go test -tags physx ./src/roomserver/...`
   - 验证 PhysX 后端加载地图出生点和已有地图碰撞测试
4. `go build -tags physx ./src/roomserver/cmd`
   - 验证 roomserver 构建
5. `scripts/build_all.sh`
   - 验证全部服务构建

## 8. 自我审查

检查后发现并修正的点：

1. 不能在 service 层给玩家写死 A/B 坐标，否则地图数据改了服务端代码不会同步；最终方案从 `collision.json` 的 `spawn_points` 读取
2. 不能让 logic 层 import `physx` 包，否则破坏当前 `logic -> interface -> physx` 的边界；最终方案通过 `PhysicsWorld.SpawnPoints()` 暴露通用数据
3. 只按 `len(r.players)` 分配会在 A 玩家离开后把新玩家错误分配到 B；最终方案按现存玩家 `SpawnID` 计算占用
4. 不能没有出生点时默默使用 `(0,0,0)`，否则问题不明显且可能导致重叠；最终方案入房失败并返回明确错误
5. 只设置位置不设置朝向会浪费 Unity 出生点 rotation；最终方案提取 yaw 初始化玩家朝向
6. 维护独立 occupied map 容易与玩家离房状态失配；最终方案不持久化占用表，只从 `r.players` 实时计算

## 9. 最终执行方案

确认后我会按以下顺序实施：

1. 在 logic 层增加 `SpawnPoint` 类型和 `PhysicsWorld.SpawnPoints()` 接口
2. 为 simple 后端提供默认 `spawn_a` / `spawn_b`
3. 在 PhysX map loader 中把 JSON spawn points 转成 logic spawn points，并保存到 `World`
4. 修改 `Room.handleJoin`，按未占用出生点初始化玩家位置和朝向
5. 给 `Player` 增加 `SpawnID` 记录当前占用的出生点
6. 增加 room 级出生点分配测试和 PhysX map spawn 读取测试
7. 更新文档说明出生点已接入
8. 执行测试和构建验证

等待用户确认后再修改业务代码。
