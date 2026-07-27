# roomserver 视角驱动移动实现方案

## 需求理解

当前服务端权威移动已经改为固定 tick 推进，但 `MoveX/MoveZ` 仍按世界坐标直接作用到 `X/Z`：

```go
player.X += input.MoveX * delta
player.Z += input.MoveZ * delta
```

这意味着玩家朝向变化不会影响前后左右移动方向。现在要做的是 FPS 常见的视角驱动移动：客户端仍只上传输入意图和视角，服务端根据服务端认可的 `Yaw` 计算前后左右方向，再推进权威位置。

## 影响范围

预计只修改：

- `src/roomserver/logic/movement.go`
  - 修改 `applyAuthoritativeMovement`，让 `MoveX/MoveZ` 先转换为基于 yaw 的世界方向
  - 可新增一个辅助函数计算水平移动方向

不修改：

- `protocol.PlayerInput` 协议字段
- `room.go` 的调用链
- service 层和网络层
- snapshot 结构
- MongoDB / Redis / 其他服务

## 设计方案

### 1. 输入语义保持不变

继续沿用现有字段：

- `MoveX`：左右移动输入，`-1` 表示左，`1` 表示右
- `MoveZ`：前后移动输入，`1` 表示向视角前方移动，`-1` 表示向视角后方移动
- `Yaw`：水平视角，决定前后左右对应的世界方向
- `Pitch`：垂直视角，继续只用于视角和射击方向，不参与地面移动

这样不需要客户端改协议，只需要确保客户端按这个语义发送输入。

### 2. 基于 yaw 计算水平前向和右向

现有坐标体系里：

- `yaw = 0` 时，前方是 `+Z`
- `yaw = 90` 时，前方是 `+X`

所以：

```go
forwardX = sin(yaw)
forwardZ = cos(yaw)
rightX = cos(yaw)
rightZ = -sin(yaw)
```

世界位移方向：

```go
worldX = forwardX*MoveZ + rightX*MoveX
worldZ = forwardZ*MoveZ + rightZ*MoveX
```

因为 `MoveX/MoveZ` 已经在 `sanitizePlayerInput` 中归一化过，所以斜向移动不会更快。

### 3. Pitch 不影响移动

FPS 地面移动通常不因为抬头/低头改变水平移动速度。本次 `Pitch` 不参与移动，只继续用于 snapshot 和开火 `viewDirection`。

### 4. 保持服务端权威 tick

移动仍然在 `Room.update -> updatePlayers -> applyAuthoritativeMovement` 中按服务端 tick 执行，不改回输入事件即时移动。

## 兼容性影响

协议不变。行为会改变：

- `MoveZ=1` 会变成“向当前 yaw 前方移动”，不再固定向世界 `+Z`。
- `MoveX=1` 会变成“向当前 yaw 右方移动”，不再固定向世界 `+X`。

这是视角驱动移动的预期变化。

## 健壮性

沿用已有输入校验：

- `MoveX/MoveZ/Yaw/Pitch` 非有限值丢弃
- `MoveX/MoveZ` 限制并归一化
- `Yaw` 归一化到 `[-180, 180]`
- `Pitch` 限制到 `[-89, 89]`
- 位置仍限制在简单世界边界内

## 性能考虑

每个玩家每 tick 增加一次 `sin/cos` 计算。当前默认 10 人、20Hz，成本可以忽略。

后续如果人数上升，可以把 yaw 对应的 forward/right 缓存在玩家状态或输入状态里，但现在没有必要增加复杂度。

## 验证方式

实现后执行：

```bash
gofmt -w src/roomserver/logic/movement.go
go test ./...
bash scripts/build_all.sh
```

联调时重点看：

- `yaw=0, move_z=1`：Z 增加
- `yaw=90, move_z=1`：X 增加
- `yaw=0, move_x=1`：X 增加
- `yaw=90, move_x=1`：Z 减少

## 自我审查

1. 没有改协议，避免客户端生成代码或消息结构同步风险。
2. 没有把视角逻辑放进 service 层，仍在 logic 层处理。
3. 没有让 pitch 影响地面移动，避免抬头导致飞行或减速。
4. 没有引入配置项，当前只改方向换算，保持范围小。
5. 与 `viewDirection` 的 yaw 坐标系保持一致，避免移动和射击方向不一致。

## 修正后的最终方案

只修改 `movement.go`：

1. 在 `applyAuthoritativeMovement` 中先通过 yaw 计算水平世界移动方向。
2. 使用世界方向乘以 `defaultPlayerMoveSpeed / tickRate` 推进 `X/Z`。
3. 保留现有角度、速度、边界和 snapshot 行为。
4. 运行 gofmt、go test、build 脚本验证。

等待确认后开始修改。
