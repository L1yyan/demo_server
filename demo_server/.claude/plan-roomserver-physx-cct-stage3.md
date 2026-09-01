# PhysX CCT 阶段 3 实施方案

## 1. 需求理解

将 `src/roomserver/physx/physx_bridge.cc` 的 `px_world_move_player` 从手写 capsule sweep + `PxRigidDynamic` kinematic target 迁移为 `PxCapsuleController::move`，保持现有 Go `PhysicsWorld`、C ABI、跳跃/重力参数和返回结构不变。

本阶段只替换玩家的碰撞推进算法，不新增协议、动态刚体交互、批量移动、蹲伏或其他角色能力。

## 2. 影响范围

### 预计修改

- `src/roomserver/physx/physx_bridge.cc`
  - 删除 `MovePlayer` 中针对玩家的手写 capsule sweep、kinematic pose 写入和每次移动的 `scene.simulate/fetchResults`
  - 保留现有跳跃与重力计算
  - 通过 `PxController::move` 推进 CCT
  - 将 `PxControllerCollisionFlags` 映射为现有 `Blocked`、`Grounded` 和 `VerticalVelocity`
  - 继续用 `getFootPosition` 返回业务脚底坐标
  - 为 CCT-vs-CCT 明确过滤策略，避免依赖未调用 `computeInteractions` 的默认行为

- `src/roomserver/physx/world.go`
  - 仅更新 `MovePlayer` 的注释，准确描述 CCT 实现；不改变 ABI 转换

- `src/roomserver/physx/world_test.go`
  - 保留现有移动、跳跃、静态 box/mesh 阻挡测试
  - 必要时补充落地状态和纯重力推进断言

### 不修改

- `src/roomserver/logic/physics.go`
- `src/roomserver/logic/movement.go`
- `src/roomserver/logic/room.go`
- proto、客户端协议、地图碰撞格式和 simple 后端

## 3. 核心设计

### 3.1 位移计算

继续使用当前 bridge 的状态计算：

1. 校验玩家、方向、距离、时间和垂直速度
2. 将输入方向归一化并乘以水平距离
3. 按现有规则计算下一帧垂直速度：
   - `jump && grounded`：跳跃初速度加上本帧重力
   - `grounded`：垂直速度清零
   - 空中：累加重力
4. 将水平位移和垂直位移合并为一个 `PxVec3 displacement`
5. 调用 `controller->move(displacement, minDist, deltaTime, filters)`

`minDist` 使用足够小的数值阈值，避免固定皮肤宽度导致小幅重力位移被吞掉；接触偏移继续由 CCT descriptor 管理。

### 3.2 查询过滤

继续使用当前 `IgnoreActorFilter` 忽略玩家自身的底层代理 actor，并将它传入 `PxControllerFilters`，保留静态和动态场景查询范围。

当前接口没有独立的“物理帧开始”调用，而 `computeInteractions` 按 PhysX 要求应每帧只调用一次。为避免在每个玩家 `MovePlayer` 调用中重复计算并产生 tick 顺序依赖，本阶段显式关闭 CCT-vs-CCT 交互过滤。该项目当前没有将玩家互相推动或互相阻挡作为对外游戏功能；地图静态碰撞仍由 CCT 完整处理。后续若确实需要玩家互撞，应新增明确的物理帧边界接口，再统一调用一次 `computeInteractions`，不在本阶段隐式引入。

### 3.3 碰撞结果映射

- `eCOLLISION_SIDES` 或 `eCOLLISION_UP`：`Blocked = true`
- `eCOLLISION_DOWN`：`Grounded = true`
- 向上移动时出现 `eCOLLISION_UP`：清零垂直速度
- 向下移动时出现 `eCOLLISION_DOWN`：清零垂直速度
- 仅有地面接触不视为 `Blocked`，避免站在地面上被误报为水平移动阻挡
- 返回位置来自 `getFootPosition()`，继续保持 Go 业务脚底坐标

### 3.4 Scene 模拟

CCT `move` 自己执行控制器的 sweep 和位移，不再在每个玩家调用中设置代理 actor 的 kinematic target，也不再调用 `scene.simulate/fetchResults`。本阶段没有动态刚体模拟需求，因此不新增统一 scene 模拟循环。

## 4. 错误处理和边界

- world、输出指针、玩家 ID 和 controller 为空时返回现有错误
- 非有限方向、距离、时间或垂直速度继续返回错误
- `deltaTime <= 0` 的 Go 包装默认值行为保持不变
- 零水平位移仍调用 CCT move，以支持纯重力和落地检测
- CCT move 返回后统一从 controller 读取位置，不读取代理 actor 的 pose
- 保持 controller 只能由对应 room loop 串行访问
- 不在 `simulate` 期间释放对象；本阶段移除该调用后，释放仍由现有 world 生命周期负责

## 5. 兼容性

- Go 接口、C ABI、网络协议和配置字段不变
- 跳跃初速度、重力、tick 时间和 `MovePlayerResult` 字段不变
- 墙体阻挡从手写 sweep 改为 CCT collide-and-slide，斜向碰撞位置可能变化，但应保持不穿透并能沿墙滑动
- 仅地面接触不再设置 `Blocked`，这是对返回字段语义的明确化，不影响现有水平阻挡调用
- 玩家之间不新增 CCT 交互；当前阶段显式过滤，避免未定义的默认交互行为

## 6. 性能考虑

- 每次玩家移动仍为一次 C ABI 调用
- 删除旧的每玩家 `scene.simulate/fetchResults`，避免重复推进整个 PhysX scene
- 不调用 `computeInteractions`，避免没有帧边界时的重复计算和玩家遍历顺序依赖
- 不新增 goroutine、锁或批量接口

## 7. 验证方式

1. `git diff --check -- src/roomserver/physx/physx_bridge.cc src/roomserver/physx/world.go src/roomserver/physx/world_test.go`
2. `cmake --build build/windows/physx_bridge --config checked --target physx_bridge --parallel`
3. `PATH="$PWD/bin/windows:$PATH" go test -count=1 -tags physx ./src/roomserver/physx`
4. `go test ./src/roomserver/logic/...`
5. 重点确认：水平移动、墙体阻挡、静态 mesh、跳跃上升、空中重力、落地、位置读写、射线和删除玩家测试

## 8. 自我审查与修正后的方案

- 不改变 Go 和协议层，符合阶段 3 的最小迁移边界
- 不保留旧 actor pose 更新，避免 CCT 与代理 actor 状态分叉
- 不把 `eCOLLISION_DOWN` 错误映射为 `Blocked`
- 不使用 `k_sweep_skin_width` 作为 CCT 的 `minDist`，避免小位移被吞掉
- 不在每个玩家移动时调用 `computeInteractions`，避免把一次物理帧错误地计算多次
- 明确记录 CCT-vs-CCT 的当前策略和未来所需帧边界，而不是依赖 PhysX 默认值
- 使用现有测试验证外部行为；若真实地图上发现出生点或接触偏移差异，只在 bridge 内修正，不扩大到业务层

最终实施路径：只在 bridge 中完成 CCT move 和碰撞标志映射，必要时更新 Go 注释与最小行为测试，然后按 Windows native build、PhysX 测试、logic 测试顺序验证。
