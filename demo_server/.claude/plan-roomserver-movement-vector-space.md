# roomserver 修正 W 前进偏 45 度方案

## 需求理解

当前按 W 前进时，客户端视角仍然朝正前方，但服务端权威位置让模型往左偏约 45 度移动，导致动画/位移不再是直线。结合当前 `movement.go` 实现，最可能原因是客户端发送的 `move_x/move_z` 已经是客户端按视角或世界方向计算后的水平移动向量，而服务端又在 `movementDirection(input.Yaw, input.MoveX, input.MoveZ)` 中按 `yaw` 再旋转了一次，形成二次旋转。

本次目标是让按 W 的权威移动方向和客户端视角/动画表现重新一致。

## 影响范围

预计修改：

- `src/roomserver/logic/movement.go`
  - `buildMovePlayerRequest` 不再用 `yaw` 二次旋转 `MoveX/MoveZ`
  - 将 `MoveX/MoveZ` 作为客户端提交的水平移动方向使用，仍经过服务端夹取和归一化
  - `Yaw/Pitch` 继续只用于视角、AOI、raycast 和 snapshot
- `src/roomserver/logic/movement_test.go` 或现有 logic 测试
  - 新增移动方向测试，明确 `MoveZ=1` 不会因为 `Yaw=45` 被旋转成斜向
  - 保留斜向输入归一化测试，避免恢复出斜向加速
- `src/roomserver/protocol/message.go`
  - 更新 `PlayerInput.MoveX/MoveZ` 注释，说明这是客户端提交的水平移动方向分量，不再描述为服务端本地左右/前后输入
- `pb/room/room.proto`
  - 同步更新 `PlayerInput` 字段注释，保持客户端协议文档一致
- `src/roomserver/README.md` / `PHYSX_FLOW.md`
  - 如存在“服务端按 yaw 转换移动方向”的描述，改成“服务端校验并使用客户端提交的水平移动向量”

不修改：

- 消息字段编号和 JSON 字段名
- `PlayerInput` 结构字段数量
- `JoinRoomAck`、`Snapshot` 结构
- service 层、RoomManager、PhysX 接口

## 设计方案

### 1. 调整移动语义

当前逻辑：

```go
move := movementDirection(input.Yaw, input.MoveX, input.MoveZ)
```

修正为：

```go
move := Vector3{X: input.MoveX, Z: input.MoveZ}
```

`sanitizePlayerInput` 仍然负责：

- 拒绝 NaN / Inf
- 把 `MoveX/MoveZ` 限制到 `[-1, 1]`
- 长度大于 1 时归一化，防止斜向加速
- 归一化 `Yaw`
- 限制 `Pitch`

这样服务端继续权威控制速度和合法性，但不改变客户端提交的水平移动方向。

### 2. 保留视角用途

`Yaw/Pitch` 不参与平面移动方向换算，但继续用于：

- `applyViewRotation` 更新玩家视角
- `viewDirection` 计算开火 raycast 方向
- AOI 可见性判断
- snapshot/ack 同步给客户端

这可以避免修复移动方向时影响射击和视野逻辑。

### 3. 协议注释同步

字段名保持不变：

- `move_x`
- `move_z`

但注释从“左右/前后移动输入”调整为“水平移动方向 X/Z 分量”。这是语义文档修正，不改变 wire format。

如果后续想切换成服务端按裸 WASD 和 yaw 计算方向，应新增或明确客户端发送 raw input，而不是复用现在已经承载方向向量的字段。

## 兼容性

- 不改变 JSON 字段名和 proto 字段编号，客户端不需要重新适配字段结构
- 行为上会回到“客户端提交方向，服务端校验和限速”的语义
- 如果某些客户端确实发送的是裸 WASD 本地输入，那么这次改动会让它不再随视角转向；但从当前现象看，现有客户端更像已经发送了视角后的方向向量

## 健壮性

- 非法浮点数仍丢弃
- 超范围输入仍夹取
- 斜向移动仍归一化
- tick_rate 非法仍拒绝生成移动请求
- 玩家不存在或死亡仍忽略输入
- 物理移动失败仍保留现有 warn，不更新玩家坐标

## 性能考虑

- 高频 tick 路径会减少一次 `sin/cos` 计算，比当前更轻
- 不增加锁、goroutine、cgo 调用或额外分配
- 移动方向直接使用输入缓存，保持 room goroutine 单线程访问

## 验证方式

执行：

```bash
gofmt -w src/roomserver/logic/movement.go src/roomserver/logic/movement_test.go src/roomserver/protocol/message.go
go test ./src/roomserver/logic ./src/roomserver/protocol
go test ./src/roomserver/...
```

如果同步修改 proto 注释，再执行：

```bash
make proto
```

最后重新编译并重启服务供客户端验证：

```bash
./scripts/build_all.sh
```

联调重点：

- 客户端按 W 时，模型权威位置沿客户端当前直线方向前进
- `Yaw=45` 但 `move_x=0, move_z=1` 不再被服务端额外旋转成 45 度斜向
- 斜向输入长度仍不超过 1，移动速度不变快
- 开火 raycast 仍沿 `Yaw/Pitch` 指向

## 自我审查

1. 没有把移动逻辑放到 service 层，仍保持 service -> logic 分层边界
2. 没有改协议字段编号，避免破坏客户端解析
3. 没有引入配置项或新依赖，改动范围小
4. 修复点直接对应当前现象：避免对客户端方向向量做二次 yaw 旋转
5. 风险是如果客户端实际发送裸 WASD，本方案会改变预期；但当前“视角直、模型偏 45 度”的表现更符合二次旋转问题
6. 如果后续要同时支持两种输入语义，应新增明确字段或配置，不建议让同一字段在不同客户端里含义不一致

## 修正后的最终方案

本次按“客户端提交水平移动方向向量，服务端只做校验、归一化、限速和物理修正”实现：删除移动方向里的 yaw 旋转，保留 yaw/pitch 的视角和 raycast 用途，补测试锁定按 W 不被额外旋转，并同步协议注释和文档。

等待确认后开始修改业务代码。
