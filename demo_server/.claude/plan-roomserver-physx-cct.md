# PhysX CCT 分阶段接入方案

## 1. 需求理解

在不新增游戏特性、不修改网络协议的前提下，将当前 PhysX 后端中玩家的实现从 `PxRigidDynamic + 手写 scene.sweep` 替换为 `PxControllerManager + PxCapsuleController`。已有功能必须保持：玩家加入/离开、静态 box/mesh 碰撞、平地移动、跳跃和重力、落地状态、位置读写、重生、玩家射线命中、忽略自身射线、多房间隔离和资源释放。

本次迁移的目标是让 Go 逻辑层继续使用现有 `PhysicsWorld` 和 `MovePlayer` 语义，CCT 细节集中在 PhysX bridge 内部。构建配置已经为 `PhysXCharacterKinematic_static_64` 做好准备，但运行时代码尚未迁移。

## 2. 当前实现基线

- [src/roomserver/logic/physics.go](src/roomserver/logic/physics.go) 定义 `PhysicsWorld`、`MovePlayerRequest` 和 `MovePlayerResult`
- [src/roomserver/logic/movement.go](src/roomserver/logic/movement.go) 根据服务端 tick、yaw、速度和玩家状态生成移动请求
- [src/roomserver/logic/room.go](src/roomserver/logic/room.go) 在单个房间 goroutine 中处理玩家加入、tick 移动、开火、重生和关闭 world
- [src/roomserver/logic/room_manager.go](src/roomserver/logic/room_manager.go) 通过 factory 为每个房间创建独立 `PhysicsWorld`
- [src/roomserver/physx/world.go](src/roomserver/physx/world.go) 将 Go 类型转换为 C ABI，并保留错误处理和地图加载
- [src/roomserver/physx/physx_bridge.cc](src/roomserver/physx/physx_bridge.cc) 当前使用 `PxRigidDynamic` 胶囊体；`MovePlayer` 手动计算重力、sweep、碰撞和 scene simulate
- [src/roomserver/physx/world_test.go](src/roomserver/physx/world_test.go) 已覆盖移动、跳跃、位置读写、删除、静态 box/mesh 阻挡、raycast、多 world 和非法请求

迁移时必须先固定以下兼容契约：

- 逻辑层 `Player.X/Y/Z` 和地图出生点的 `Y` 按当前约定表示玩家业务脚底位置；不得直接把 CCT 的中心位置写回 Go
- 本 SDK 的 CCT `getFootPosition()` 会把 `contactOffset` 纳入脚底定义，因此 bridge 必须统一补偿，避免角色高度和旧实现发生隐含偏移
- 当前 `PlayerCapsuleHeight` 表示端到端总高度，而 `PxCapsuleControllerDesc.height` 表示两端球心距离；以半径 0.35、总高 1.8 为例，CCT height 应为 1.1
- 当前刚体实现的 `Raycast` 参数 `Mask` 还没有真正写入 PhysX filter data；本次不扩大查询系统范围，但 CCT 迁移不能误称 mask 已兼容
- CCT 的底层 proxy actor 与 descriptor 的 `userData` 不应被假定自动一致；射线命中 ID 需要显式维护映射或给 proxy actor 设置稳定 ID

## 3. 影响范围

### 必须修改

- `src/roomserver/physx/physx_bridge.cc`
  - 增加房间级 `PxControllerManager`
  - 将玩家映射从 `PxRigidDynamic*` 改为 `PxCapsuleController*` 及必要的玩家元数据
  - 改写创建、删除、移动、获取位置、设置位置和资源释放
  - 保留静态碰撞和 scene raycast

- `src/roomserver/physx/world_test.go`
  - 将旧实现假设改成 CCT 外部行为断言
  - 补充 CCT 关键行为的最小测试：碰撞标志映射、地面落地、墙阻挡/滑动、台阶或坡度参数至少一项
  - 增加 foot position/contact offset 和总高度换算的 golden 断言，防止业务脚底坐标发生隐含偏移
  - 增加零位移、天花板和 CCT-vs-CCT 行为断言，明确 `Blocked` 与 `Grounded` 的边界

### 可能小幅修改

- `src/roomserver/physx/physx_bridge.h`
  - 仅当现有 ABI 无法表达所需 CCT 结果时调整；优先保持函数签名不变

- `src/roomserver/physx/world.go`
  - 仅处理 CCT 返回值或位置语义需要的转换；优先保持现有 Go 封装不变

- `src/roomserver/logic/physics.go`
  - 第一版不改接口；若必须暴露更明确的 collision flags，新增字段时保持旧调用兼容

### 明确不修改

- `src/roomserver/logic/room_manager.go`
- `src/roomserver/logic/room.go` 的房间生命周期、输入队列和业务规则
- `src/roomserver/logic/movement.go` 的输入协议和移动速度语义
- proto、客户端协议、地图碰撞 JSON 格式
- simple physics fallback
- 射击伤害、AOI、同步和奖励逻辑

## 4. 分阶段设计

### 阶段 0：构建和 SDK 前置检查

目标：确保 CCT 运行时代码有可用的头文件和库。

1. 确认 `third_party/physx-sdk/include/characterkinematic` 存在
2. 确认 checked 配置下存在 `PhysXCharacterKinematic_static_64.lib`
3. 通过 bridge CMake 配置和链接检查，确保 `PxCreateControllerManager` 的实现可用
4. 不在此阶段改变业务代码

已有构建配置变更：

- [scripts/setup_physx_windows.ps1](scripts/setup_physx_windows.ps1) 已加入 `PhysXCharacterKinematic` 构建 target
- [src/roomserver/physx/CMakeLists.txt](src/roomserver/physx/CMakeLists.txt) 已加入 `PhysXCharacterKinematic_static_64`

如果 SDK 中还没有该库，先重新运行 setup；不能通过修改运行时代码绕过缺失库。

### 阶段 1：C++ 内部建立 controller manager

目标：在不创建玩家 CCT 的情况下，完成 world 级 manager 生命周期。

1. 在 `px_world` 增加 `PxControllerManager* controller_manager`
2. 在 `px_world_create` 中创建 scene 和材质后创建 manager
3. 在创建失败路径中按反向顺序释放 manager、scene、dispatcher、材质和 runtime 引用
4. 在 `px_world_release` 中先释放 manager，再释放地图 actor、材质、scene 和 dispatcher；不要再单独释放 controller 的底层 proxy actor，manager 会负责其 controller 资源
5. 注意 `PxCreateControllerManager` 会增加 PhysX foundation 引用计数，必须先调用 manager 的 `release()`，再释放 scene/dispatcher，最后减少 world 对 runtime 的引用并清理 foundation
6. 保证一个 scene 只有一个 manager；每个房间 world 仍然独立
7. 继续要求所有 CCT 调用由对应 room loop 串行执行；manager 的 locking 选项不替代应用层所有权
8. 如果未来需要释放静态 actor 后继续使用 manager，按 SDK 要求维护 deletion listener/cache；本次静态地图只在 world 销毁时释放

验证：bridge 可以创建和关闭空 world，多房间创建/关闭不会崩溃或泄漏。

### 阶段 2：将玩家对象替换为 capsule controller

目标：只改变玩家物理对象，暂时保持 C ABI 和 Go 调用方式。

1. 将玩家记录改为 `PxCapsuleController*`，保留半径、高度和玩家 ID 元数据
2. 使用 `PxCapsuleControllerDesc` 创建 controller
3. 配置固定 Y 轴向上、当前玩家半径、当前业务总高度换算后的 CCT `height`
4. 保留当前业务的脚底坐标约定：创建和读写优先使用 `setFootPosition/getFootPosition`
5. 配置与现有行为相近的 `contactOffset`、`stepOffset`、`slopeLimit` 和 `nonWalkableMode`；参数应集中为常量或配置，不散落在移动函数
6. 开启默认 overlap recovery，避免重生或位置纠正时静态重叠永久卡住；仍然把非法出生点视为错误
7. 为 controller 和其底层 proxy actor 建立显式的 player ID 映射，保证现有 scene raycast 能继续返回 `TargetID`；不要假设 descriptor 的 `userData` 会自动复制到 proxy actor
8. 删除时调用 controller `release()`，不再直接释放其底层 actor
9. 保留 overlap recovery 的默认启用状态，并在静态地图加载完成后再创建 controller
10. 明确 CCT-vs-CCT 行为：当前功能不要求新增玩家互撞规则，第一版通过过滤回调保持与旧实现一致的阻挡语义，或明确关闭 CCT-vs-CCT 并在验收中记录差异；不能依赖默认值而不验证

关键尺寸约定：当前 `PlayerCapsuleHeight` 是端到端总高度；本 SDK 的 `PxCapsuleControllerDesc.height` 是两端球心之间的圆柱段高度，因此传入值应为 `totalHeight - 2 * radius`。需要在代码中校验结果大于零。CCT 的 `footPosition` 包含 `contactOffset`，因此还要通过 golden test 固定业务脚底与 CCT foot 的转换。

验证：现有 Add/Remove/Get/Set 和删除后 raycast 测试通过；位置误差按合理接触偏移容差判断，不要求与旧刚体实现逐位一致。

### 阶段 3：改写 MovePlayer 为 CCT collide-and-slide

目标：保留现有跳跃/重力和返回结构，只将碰撞推进交给 CCT。

1. 继续接受现有 `MovePlayerRequest`，不修改 Go 协议
2. 复用当前 bridge 中的重力和跳跃状态计算，保持跳跃速度、重力和 delta time 语义不变
3. 根据当前水平 `Direction` 和 `Distance` 生成水平位移；与垂直位移合并为一个 CCT displacement
4. 调用 `PxController::move(displacement, minDist, elapsedTime, filters)`
5. 通过 `PxControllerCollisionFlags` 映射结果：
   - `eCOLLISION_SIDES` -> `Blocked`
   - `eCOLLISION_DOWN` -> `Grounded`
   - `eCOLLISION_UP` -> 清零向上垂直速度
6. 当 `eCOLLISION_DOWN` 出现且垂直速度向下时清零垂直速度
7. 从 `getFootPosition()` 返回经过 contact offset 补偿后的业务脚底坐标
8. 保持无水平输入时仍能执行纯重力移动，保持现有空中状态推进
9. 不在每个玩家的 CCT move 中重复推进整个 scene；CCT move 负责查询和控制器位移
10. 第一版继续在房间 goroutine 中串行调用，避免引入并发访问
11. 如果保留玩家之间的 CCT 碰撞，在每帧移动前按 SDK 要求调用一次 `computeInteractions`；否则显式配置 CCT 过滤回调，避免默认行为造成房间 tick 顺序依赖
12. `Blocked` 不直接等同于 CCT 有任意碰撞：至少区分 `eCOLLISION_SIDES/eCOLLISION_UP` 与仅仅站在地面的 `eCOLLISION_DOWN`，并通过测试固定旧接口语义

需要明确一个行为差异：CCT 自带 collide-and-slide，墙角和斜向撞墙结果可能与旧手写 sweep 不同。验收以“不穿墙、能沿墙滑动、正确落地/顶头”为准，而不是旧坐标完全相同。

### 阶段 4：保持位置纠正、重生和射线功能

目标：确保现有业务流程不因 controller 语义变化回归。

1. `SetPlayerPosition` 使用 CCT 的脚底位置接口完成传送，不把它当普通移动处理
2. 保持现有重生流程：先同步物理位置成功，再恢复 `Alive`、HP、无敌和 grounded 状态
3. 如果 controller 的 foot position 含 contact offset，统一在 bridge 内转换，Go 层仍只看到原有业务脚底坐标
4. raycast 继续走 `PxScene::raycast`，静态地图和玩家均保持现有查询范围
5. 继续支持 `IgnorePlayerID`；忽略自身时过滤 controller 的底层 actor
6. 保持 `BatchRaycast` 现有 ABI 和返回顺序

验证：开火命中玩家、忽略自身、多房间互不命中、死亡重生后可继续移动。

### 阶段 5：行为测试和真实地图验收

目标：只验证已有功能的 CCT 行为，不扩展游戏功能。

测试顺序：

1. 空 world 创建/关闭
2. 平地 AddPlayer 后位置稳定
3. 水平移动距离正确
4. 无输入空中继续重力下落
5. 跳跃后上升且不标记 grounded
6. 跳跃后落地并清零垂直速度
7. 墙体阻挡且返回 Blocked
8. 斜向撞墙后仍能沿切线移动
9. 静态 mesh 阻挡
10. 位置读写和重生
11. 玩家删除后 raycast 不命中
12. raycast 忽略自身
13. 多 world 隔离
14. 非法输入显式报错
15. 使用真实 `mfps_arena` 地图运行 roomserver，检查出生点、地面和主要墙体

测试命令按环境分层执行：

- `cmake --build build/windows/physx_bridge --config checked --target physx_bridge --parallel`
- `go test -tags physx ./src/roomserver/physx`
- `go test ./src/roomserver/logic/...`
- 必要时执行 Windows 构建脚本验证 DLL 和 roomserver 链接

若当前环境无法使用对应 MSVC/PhysX DLL，必须报告具体失败命令和缺失依赖。

## 5. 兼容性

- 网络协议不变
- Go `PhysicsWorld` 第一版不变
- C ABI 函数签名第一版不变
- 地图静态碰撞输入不变
- simple 后端不受影响
- 运行时玩家底层对象从刚体改为 CCT，碰撞细节会变化，但对外只承诺现有功能的语义结果
- CCT 的默认玩家与玩家碰撞行为需要以现有功能为准验证；若当前项目没有明确要求玩家互相阻挡，第一版不新增业务处理

## 6. 健壮性

- 所有 CCT descriptor 字段在创建前校验：半径、换算高度、位置、材质、contact offset、step offset
- manager/controller 创建失败时返回清晰错误，并完整释放已创建对象
- 不允许重复 player ID；删除不存在玩家保持当前幂等语义
- world 关闭后所有公开操作继续返回已有 closed error
- controller 的 teleport 不进行碰撞检查，因此出生点和重生点必须由地图数据保证合法；开启 overlap recovery 只作为已有位置纠正的防护，不替代输入校验
- 物理错误不能静默覆盖 Player 状态；现有 room 层日志和状态更新逻辑继续生效
- CCT 与 scene 的访问限定在 room loop，保持当前串行模型
- PhysX scene 的 `simulate/fetchResults` 必须严格一一配对，且不能在 simulate 期间释放 actor、controller manager 或 scene
- `RoomManager.Stop` 当前只发送停止信号、不等待 room loop 完成；后续 CCT 阶段必须增加停止完成同步，确保 loop 退出并完成 `PhysicsWorld.Close` 后，才允许重建或释放关联 runtime 资源
- `PxCreateControllerManager` 会增加 foundation 引用计数；manager 必须在 scene、dispatcher 和 runtime/foundation 之前释放

## 7. 性能考虑

本次只替换已有玩家移动实现，不新增能力。CCT 的 `move` 避免当前手写 sweep 后再更新 kinematic actor 的重复流程，也不应在每个玩家移动时调用 scene simulate。第一版保留每个玩家一次 C ABI 调用的结构，避免为批量接口扩大改动范围；批量移动不属于本次需求。

## 8. 自我审查

1. **是否需要改 RoomManager？** 不需要。它只创建房间级 world，CCT manager 属于 world 内部资源。
2. **是否需要改协议？** 不需要。现有输入已经包含移动、跳跃和视角，CCT 只改变物理实现。
3. **是否会误把 CCT 当刚体？** 方案明确使用 controller 的 release、move、foot position 和 collision flags，不直接操作底层 actor。
4. **是否遗漏射线？** 已保留 scene raycast，并计划绑定 player ID 到可查询 actor。
5. **是否遗漏重力？** CCT 不自动提供重力，方案明确复用现有 bridge 的跳跃/重力计算。
6. **是否遗漏 scene simulate？** 方案明确移除其作为每次 CCT 移动必要步骤；当前没有动态物体功能，不新增统一动态模拟流程。
7. **是否有高度坐标风险？** 已明确区分业务总高度、CCT 圆柱段高度和脚底坐标，并要求用本地 SDK 头文件定义校验。
8. **是否过度设计？** 不新增动态物体、触发器、批量移动、玩家互推或新的接口；第一版只改 bridge 和必要测试。
9. **是否需要修改 raycast filter？** 需要在 CCT 实现阶段验证底层 actor 的 userData 和查询过滤，但不提前重构整个过滤系统。
10. **是否遗漏 foundation 引用计数？** 已补充 manager 创建/释放的引用计数约束，manager 必须先于 scene、dispatcher 和 runtime/foundation 释放。
11. **是否存在停止释放竞态？** 已补充 scene simulate/fetch 配对和 room loop 完成同步要求；不能只发送 Stop 后立即释放 world。

## 9. 修正后的最终方案

采用“保持 Go/协议/C ABI，分阶段替换 bridge 内部对象”的路径：先确认 CCT 库可链接，再建立每个 world 一个 `PxControllerManager`，随后将玩家替换为 `PxCapsuleController`，最后把现有重力/跳跃位移交给 `PxController::move`，并用 collision flags 生成现有返回值。每一阶段都先运行对应的 native/Go 测试，再进入下一阶段。

本次第一步实施范围建议限定为 **阶段 1 + 阶段 2**：完成 manager 和玩家 controller 生命周期、位置读写，暂时不改 `MovePlayer`；这样能先验证 CCT 创建、尺寸和坐标约定，再单独迁移移动算法。

第一步实施前提：先重新执行 PhysX SDK 准备脚本生成 `PhysXCharacterKinematic_static_64.lib`。当前 prepared SDK 中尚未有该库，不能直接声称 CCT bridge 已经可以链接。

等待用户确认后开始实施。
