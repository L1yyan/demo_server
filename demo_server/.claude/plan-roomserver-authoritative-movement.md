# roomserver 服务端权威移动实现方案

## 需求理解

当前 `Room.handleInput` 在收到 `MsgPlayerInput` 事件时直接执行：

```go
player.X += input.MoveX * 0.2
player.Z += input.MoveZ * 0.2
```

这会导致玩家移动距离取决于客户端发包频率。客户端发包越快，服务端位置变化越快，不符合服务端权威移动的基本要求。

本次目标是把移动推进改为由 roomserver 固定 tick 驱动：客户端只提交输入意图，服务端按自己的 tick_rate、速度限制和输入校验计算最终位置，并通过 snapshot 广播权威状态。

## 影响范围

预计修改：

- `src/roomserver/logic/room.go`
  - 输入事件不再直接修改玩家坐标
  - `update` 每 tick 统一推进玩家移动
  - 增加输入缓存、输入校验、固定 tick 位移计算

- `src/roomserver/logic/player.go`
  - 可能增加玩家最后输入、最后输入帧等状态字段，或保持在 `Room` 内部 map 中

- 可选新增 `src/roomserver/logic/movement.go`
  - 抽出移动参数、输入规范化、角度处理、方向计算等纯逻辑，避免 `room.go` 继续变大

暂不修改：

- `protocol.PlayerInput` 字段结构
- `service` 网络层
- `RoomManager` 对外接口
- matchserver / logicserver
- MongoDB / Redis

## 设计方案

### 1. 输入只作为意图，不直接改位置

`handleInput` 改为：

1. 找到玩家并确认存活。
2. 校验输入是否合法。
3. 归一化移动向量。
4. 归一化 yaw，限制 pitch。
5. 把处理后的输入保存为该玩家的 latest input。

客户端仍然发送 `MoveX`、`MoveZ`、`Yaw`、`Pitch`、`Fire`，但最终坐标只由服务端 tick 更新。

### 2. 服务端固定 tick 推进移动

`Room.update` 每 tick 做：

1. `r.tick++`
2. 按 `dt = 1 / tickRate` 计算本 tick 时间步长。
3. 遍历房间内存活玩家。
4. 根据玩家最后一次有效输入计算位移。
5. 按固定速度推进位置。
6. 再判断是否到 snapshot 广播间隔。

建议默认速度先写成 logic 常量，例如：

```go
const defaultPlayerMoveSpeed = 4.0 // 每秒移动单位
```

如果 tick_rate = 20，则单 tick 最大位移：

```text
4.0 / 20 = 0.2
```

这样保留当前手感的大致数值，但移动速度不再受客户端发包频率影响。

### 3. 输入校验和归一化

对 `MoveX/MoveZ`：

- 非有限数值直接丢弃输入：NaN、Inf
- 限制到 `[-1, 1]`
- 如果向量长度大于 1，归一化，避免斜向移动变快

对 `Yaw/Pitch`：

- 非有限数值丢弃输入
- `Yaw` 归一化到 `[-180, 180]`
- `Pitch` 限制到 `[-89, 89]`

### 4. 坐标边界

当前没有真实地图和碰撞体。为了先建立服务端权威移动边界，增加简单世界边界：

```go
const defaultWorldLimit = 100.0
```

位置限制在：

```text
X: [-100, 100]
Z: [-100, 100]
```

这不是最终地图碰撞，但能先防止客户端靠异常输入把位置推到无限远。后续接 PhysX 或地图碰撞时，再把边界替换为物理查询。

### 5. 碰撞处理边界

当前 `PhysicsWorld` 只有 `Raycast/BatchRaycast`，没有角色移动或 shape cast 接口，也没有地图碰撞数据。因此这次不伪造“真实碰撞”。

本次只做：

- 服务端固定 tick 移动
- 输入合法性校验
- 速度限制
- 简单世界边界

后续真实碰撞建议单独扩展 `PhysicsWorld`，增加类似：

```go
MoveCapsule(playerID, from, desiredTo Vector3) (Vector3, error)
```

再接 PhysX 或地图碰撞实现。

### 6. Fire 行为

`Fire` 暂时保持现状，不引入武器系统。本次只确保视角会先经过服务端校验，后续射击方向可以基于服务端认可的 `Yaw/Pitch` 计算。

## 兼容性影响

协议不变，客户端仍然发送原来的 `PlayerInput` JSON。

行为变化：

- 客户端高频发送输入不会再获得更快移动速度。
- 非法输入会被服务端丢弃或归一化。
- 斜向移动速度会被限制到和单方向移动一致。
- 玩家不能移动出简单世界边界。

这属于服务端权威移动的预期变化。

## 健壮性

需要处理：

- NaN / Inf 输入
- 超大 MoveX / MoveZ
- 斜向移动速度过快
- pitch 超出正常范围
- tick_rate 异常时的 dt 保护
- 玩家死亡或不存在时忽略输入
- 无输入玩家保持当前位置和最后朝向

输入异常不需要每次都打日志，避免恶意客户端刷日志；可以静默丢弃或后续增加限频 warn。

## 性能考虑

房间人数默认 10，每 tick 遍历玩家成本很低。

本次实现不会引入 goroutine、锁或跨语言调用。移动计算都是简单数学运算，高频路径避免日志和额外分配。

输入缓存放在 `Room` 内部，由房间 goroutine 单线程访问，不需要加锁。

## 验证方式

实现后执行：

```bash
gofmt -w src/roomserver/logic/room.go src/roomserver/logic/player.go src/roomserver/logic/movement.go
```

按实际修改文件调整格式化命令。

然后执行：

```bash
go test ./...
```

再执行：

```bash
bash scripts/build_all.sh
```

确认所有服务能编译。

如果需要联调，再重启服务并用客户端测试：

- 持续输入时 snapshot 位置按固定速度变化
- 提高客户端输入发送频率，移动速度不应变快
- `MoveX=999` 不应造成瞬移
- 斜向移动不应比直线更快

## 自我审查

### 是否遗漏已有结构

已有结构是 `service -> logic -> protocol`。本次只改 logic 层移动计算，不把业务规则放进 service，符合项目分层。

### 是否过度设计

不新增复杂物理接口、不接 PhysX、不改协议，避免把“权威移动”和“完整碰撞系统”混在一次改动里。

### 是否存在协议风险

不修改 `PlayerInput`，没有兼容性风险。

### 是否错误处理不足

会显式处理非法浮点数、超范围输入、异常 tick_rate、死亡玩家等边界。

### 是否性能风险

每 tick 遍历玩家是当前 10 人房间下最简单可靠的做法，没有明显性能风险。

### 是否未来扩展困难

移动输入规范化和 tick 推进会拆成清晰函数，后续接碰撞或 PhysX 时只需要替换“计算目标位置 -> 修正目标位置”的部分。

## 修正后的最终方案

1. 在 `Room` 中增加每个玩家的最新有效输入缓存。
2. `handleInput` 只做玩家存在性检查、输入校验、输入归一化和缓存更新。
3. `update` 每 tick 调用移动推进逻辑，再处理 snapshot 广播。
4. 移动速度按 `defaultPlayerMoveSpeed / tickRate` 计算，不再依赖输入消息频率。
5. `MoveX/MoveZ` 做范围限制和向量归一化，避免瞬移和斜向加速。
6. `Yaw/Pitch` 做服务端归一化和夹取。
7. 加入简单世界边界，暂不伪造真实地图碰撞。
8. 执行 gofmt、go test、build 脚本验证。

等待确认后开始修改业务代码。
