# PhysX CCT 下蹲功能实施方案

## 1. 需求理解

在现有玩家移动链路中接入下蹲功能，继续使用 PhysX `PxCapsuleController`，不新增滑铲、冲刺、玩家互推等角色能力。

当前 `PlayerInput.squat`、逻辑层 `authoritativeInput.Squat` 和 `MovePlayerRequest.Squat` 已经存在，但 C++ bridge 尚未使用该字段。目标是：

- 按输入进入下蹲状态，缩短 CCT 胶囊体高度
- 松开输入时尝试恢复站立高度
- 下蹲和站立都保持玩家脚底坐标不变
- 头顶空间不足时保持下蹲，不穿透地图
- 下蹲期间继续使用现有 CCT `move` 处理水平移动、重力、跳跃和碰撞
- 保持现有 C ABI、房间逻辑分层和客户端输入协议兼容

## 2. 影响范围

预计修改：

- `src/roomserver/logic/player.go`
  - 增加服务端权威的当前下蹲状态
  - 将下蹲状态写入玩家快照；是否新增快照字段需结合协议兼容性实施
- `src/roomserver/logic/physics.go`
  - 扩展 `MovePlayerResult` 或物理接口，使物理层返回最终下蹲状态
  - 保持 `SimplePhysicsWorld` 能编译并维持相同语义
- `src/roomserver/logic/movement.go`
  - 将已有 `Squat` 输入传给物理请求
  - 将下蹲状态纳入无水平位移时仍需模拟的条件
- `src/roomserver/logic/room.go`
  - 在物理返回成功后同步玩家下蹲状态
  - 加入、死亡、重生时恢复站立初始状态
- `src/roomserver/physx/physx_bridge.h`
  - 如采用现有 `MovePlayer` 参数扩展，更新 C ABI 声明
- `src/roomserver/physx/physx_bridge.cc`
  - 在玩家记录中保存站立总高度、下蹲总高度和当前下蹲状态
  - 使用 `PxCapsuleController::resize` 切换胶囊高度
  - 起身前使用 scene overlap 查询确认目标高度空间没有静态地图占用
  - 保持脚底位置、contact offset 和业务坐标转换一致
- `src/roomserver/physx/world.go`
  - 转换下蹲参数和结果
- `src/roomserver/logic/*_test.go`、`src/roomserver/physx/world_test.go`
  - 增加状态传递、缩放、低矮障碍物和恢复站立测试
- `pb/room/room.proto` 与生成代码
  - 只有在客户端需要通过快照表现其他玩家下蹲姿态时，才增加 `PlayerState` 的兼容字段；不修改已有字段编号

## 3. 核心设计

### 3.1 高度模型

当前业务配置 `PlayerCapsuleHeight` 是胶囊体端到端总高度，默认值为 `1.8`，半径为 `0.35`。PhysX CCT 的 `height` 表示两个球心之间的距离，因此统一使用：

`cctHeight = totalHeight - 2 * radius`

建议新增下蹲总高度常量 `1.0`，并在创建 CCT 时校验下蹲高度大于 `2 * radius`。下蹲 CCT 高度约为 `0.3`，底部位置不变。

如果项目后续需要配置化，下蹲高度优先加入 roomserver 配置，而不是散落在 C++ 和 Go 两侧；本次先沿用现有配置风格，仅增加一个明确的默认值。

### 3.2 CCT 调整

`player_actor` 保存：

- 站立 CCT height
- 下蹲 CCT height
- 当前是否下蹲

进入下蹲：

- 调用 `controller->resize(crouchHeight)`
- `resize` 会按照 PhysX 语义保持脚底位置
- 更新底层代理 actor 的 scene-query 形状
- 按现有传送同步方式刷新 query 数据

恢复站立：

- 先检查以当前脚底为基准的站立胶囊体是否会与静态地图重叠
- 空间足够时调用 `resize(standHeight)`
- 空间不足时保持当前下蹲状态，并返回最终状态
- 不直接修改 CCT 底层代理 actor pose

起身检测建议使用 `PxScene::overlap` 配合目标站立胶囊体和 `IgnoreActorFilter`，查询静态与动态对象；当前项目已明确关闭 CCT 之间的交互，玩家代理 actor 应被排除。若 SDK 的 overlap 结果对边界接触存在误差，保留小的 contact offset 和测试容差，避免使用手写几何判断替代 PhysX 查询。

### 3.3 移动调用

继续让 `PxController::move` 负责 collide-and-slide。每个移动请求处理顺序为：

1. 根据 `Squat` 请求尝试调整胶囊高度
2. 按现有规则计算跳跃和重力
3. 调用一次 `controller->move`
4. 返回位置、阻挡、落地、垂直速度和最终下蹲状态

下蹲切换即使没有水平位移也必须触发物理请求，因此 `shouldMovePlayer` 要把请求姿态与当前姿态差异纳入判断。起身失败时不能把逻辑状态改成站立。

### 3.4 逻辑状态和协议

逻辑层把客户端下蹲输入作为请求，不直接修改物理位置或高度。只有 PhysX 成功完成切换后，`Player` 才更新最终下蹲状态。

加入房间、重生和移除时沿用当前生命周期，不新增 RoomManager 重构。重生后的初始状态为站立；物理传送成功后再恢复逻辑状态。

为了让其他客户端能够正确表现玩家姿态，建议在 `PlayerState` 增加一个新的 `crouched` 字段，使用未占用的新编号并重新生成代码。该字段是向后兼容的：旧客户端忽略它，新客户端可据此切换模型姿态。如果本次只要求服务端碰撞行为，则可以暂不增加快照字段，但仍需保留服务端权威状态，避免输入释放后无法处理起身。

## 4. 错误处理和边界

- 拒绝非法或过小的下蹲高度，避免 CCT descriptor 或 resize 进入无效状态
- 进入下蹲失败时返回错误，不改变当前状态
- 起身空间不足不是物理错误，返回当前下蹲状态并继续移动
- 玩家不存在、world 已关闭、非法移动参数继续使用现有错误路径
- 下蹲状态切换不重置垂直速度、位置、Yaw 或 Pitch
- 跳跃请求与下蹲请求同时出现时，沿用当前 `Grounded` 规则；建议下蹲时不接受起跳，避免姿态切换与跳跃语义冲突，并在逻辑测试中固定该行为
- 低矮空间中松开下蹲后，玩家保持下蹲；空间恢复后下一帧再次尝试站立
- 传送和重生不允许遗留旧的下蹲状态

## 5. 兼容性

- `PlayerInput.squat` 已存在，不新增输入字段
- 现有 C ABI 若通过已有移动函数传递下蹲参数，需要改变函数参数列表；Go bridge 和所有调用点同步修改，Windows DLL 需重新构建
- 更稳妥的方案是扩展 C ABI 移动接口并同步 Go 封装，C++ 与 Go 在同一版本构建，不保留旧 DLL 混用
- 若新增 `PlayerState.crouched`，只使用新字段编号，不修改或复用已有编号
- Simple physics 后端应实现同一请求/结果语义，至少保证现有测试和非 PhysX 构建不受影响

## 6. 性能考虑

- 每个玩家每个 tick 最多执行一次姿态切换检查和一次 `PxController::move`
- 只有收到下蹲状态变化或当前处于下蹲且请求起身时才执行 overlap 检查，不在每帧无条件查询
- 不新增 scene simulate/fetchResults 到正常移动路径
- 不调用批量 CCT API，不改 RoomManager 调度模型
- scene query 刷新仅在 CCT 高度变化后执行，避免额外开销

## 7. 验证方式

1. `gofmt` 格式化 Go 文件
2. 重新构建 Windows checked bridge：
   `cmake --build build/windows/physx_bridge --config checked --target physx_bridge --parallel`
3. 运行 PhysX 测试：
   `PATH="$PWD/bin/windows/checked:$PWD/bin/windows:$PATH" go test -count=1 -tags physx ./src/roomserver/physx`
4. 运行 room logic 测试：
   `go test -count=1 ./src/roomserver/logic/...`
5. 验证以下行为：
   - 站立状态进入下蹲后 CCT 高度下降，脚底 Y 坐标不变
   - 松开下蹲后在空旷处恢复站立，脚底坐标不变
   - 低矮顶棚下无法恢复站立，玩家仍保持下蹲
   - 移动、跳跃、落地和墙体碰撞不因姿态切换回归
   - 下蹲玩家的射线代理形状与新高度同步
   - 重生后恢复站立并可继续移动
   - Simple 后端、非 PhysX 编译和现有协议测试通过

## 8. 自我审查与修正后的最终方案

初步方案中存在三个风险：直接调用 `resize` 起身可能穿透顶棚；只在 C++ 保存姿态会让 Go 状态与物理状态不同步；无水平位移时不执行移动请求会导致松开下蹲无法恢复。修正后的方案加入了起身前 overlap 检查、物理结果返回最终姿态，以及把姿态变化纳入 tick 模拟条件。

另一个需要控制的范围是协议：输入字段已经存在，不需要新增输入协议。快照字段只有在客户端确实需要渲染下蹲姿态时才增加；如果先只验证服务端碰撞，则可暂不改 `PlayerState`，避免扩大本次协议改动。

最终建议先实现服务端权威 CCT 姿态切换和完整碰撞测试，默认不新增 `PlayerState` 字段，除非现有客户端表现需求明确要求同步其他玩家下蹲姿态。实现前等待用户确认。
