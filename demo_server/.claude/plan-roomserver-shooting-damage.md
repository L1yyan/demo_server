# roomserver 开火射击扣血实现方案

## 需求理解

在 roomserver 中补齐开火射击功能：客户端输入里的 `fire=true` 已经存在，服务端需要在权威 tick 中进行命中判定，命中一次扣除目标 20 点血量。血量最低为 0，玩家血量归零后标记为不存活。

## 影响范围

预计修改以下文件：

1. `src/roomserver/logic/room.go`
   - 在玩家 tick 模拟中调用射击处理逻辑
   - 增加命中扣血、死亡状态、物理对象移除和日志
2. `src/roomserver/logic/physics.go`
   - 扩展内部 `RaycastRequest`，支持忽略发起射线的玩家
   - 让 simple 物理后端可命中玩家，用于本地测试和 simple 后端运行
3. `src/roomserver/physx/world.go`
   - 将忽略玩家 ID 传给 PhysX C bridge
4. `src/roomserver/physx/physx_bridge.h`
   - 扩展 raycast/batch raycast C 接口参数
5. `src/roomserver/physx/physx_bridge.cc`
   - raycast 查询时过滤射击者自身 actor
6. `src/roomserver/logic/*_test.go`
   - 新增或扩展射击扣血、死亡、自己不命中自己的单元测试
7. `src/roomserver/physx/world_test.go`
   - 如环境支持 physx build tag，补充或调整忽略自身 raycast 的测试

暂不修改 `pb/room/room.proto` 和 `src/roomserver/protocol/message.go` 的对外字段，因为 `PlayerInput.fire` 和 `PlayerState.hp` 已经存在，当前需求不需要新增协议字段。

## 设计方案

### 核心规则

1. 客户端通过已有 `PlayerInput.fire` / `PlayerInputFrame.fire` 表示当前输入帧开火。
2. 服务端只在 `hasExactInput && input.Fire` 时执行射击，沿用输入帧不会重复开火，避免弱网缺帧时连射被放大。
3. 射线从玩家视角方向发出：方向继续使用已有 `viewDirection(yaw, pitch)`，起点使用玩家位置加一个固定视角高度，避免从脚底发射。
4. 射线最大距离先使用常量，例如 `defaultFireMaxDistance = 100`，命中伤害使用常量 `defaultFireDamage = 20`。
5. 射线查询忽略射击者自身，命中 `TargetID` 对应的存活玩家后扣血。
6. 扣血后 `HP = max(0, HP-20)`；如果归零，则 `Alive=false`，并从物理世界移除目标，避免死者继续被 raycast 命中或参与移动。
7. 血量变化通过现有 `Snapshot.players[].hp` 同步给客户端；死者已被现有 AOI 逻辑过滤，最终对局结束状态仍会包含其 `hp=0`。

### 调用流程

1. `handleInputBatch` 接收并校验输入，保留已有 `fire` 字段。
2. `updatePlayers` 在对应 tick 取出权威输入。
3. `simulatePlayerTick` 先应用视角和移动，再处理 `fire`。
4. 新增 `handlePlayerFire(ctx, shooter, input)`：
   - 构造 `RaycastRequest{Origin, Direction, MaxDistance, IgnorePlayerID: shooter.ID}`
   - 调用 `physics.Raycast`
   - 忽略未命中、命中静态物体、命中自己、命中不存在玩家、命中已死亡玩家
   - 对有效目标调用 `applyFireDamage(ctx, shooter, target)`
5. `applyFireDamage` 负责扣血、死亡状态和物理移除，保持房间逻辑仍在 room 单线程事件循环内完成，不额外加锁。

### simple 物理后端

`SimplePhysicsWorld.Raycast` 当前只校验参数并返回未命中。为保证 simple 后端可用且单元测试能覆盖射击逻辑，将补充玩家命中检测：

1. 对 `players` map 中除 `IgnorePlayerID` 外的玩家做射线检测。
2. 用玩家胶囊体的简化包围体计算命中距离，优先返回最近命中的玩家。
3. 保持静态地图仍不参与 simple raycast，真实地图遮挡由 PhysX 后端负责。

### PhysX 后端

1. `RaycastRequest` 增加 `IgnorePlayerID uint64`。
2. `physx.World.Raycast` 将该字段传入 `px_world_raycast`。
3. C++ bridge 根据 `ignored_player_id` 找到 actor，使用 `PxQueryFlag::ePREFILTER` 和已有的过滤 callback 忽略该 actor。
4. `BatchRaycast` 同步增加 ignored player ID 数组，避免接口语义不一致。

## 兼容性

1. 不新增对外 KCP 消息类型，不修改已有 JSON 字段名。
2. 不修改 proto 字段编号，也不新增字段；客户端只要已经发送 `fire` 并消费快照 `hp` 即可使用。
3. 内部 `RaycastRequest` 新增字段为 Go 结构体扩展，未设置时默认 0，兼容已有调用。
4. PhysX C bridge 的 Go/C++ 调用会同步修改，属于同仓库内部 ABI，不影响客户端协议。
5. 当前方案不改变限时对局结束规则；击杀后是否立即结束对局不在本次需求内，避免把“扣血”扩成结算规则改动。

## 健壮性

1. 输入校验继续复用 `sanitizePlayerInput`，非法 yaw/pitch/move 输入不会进入权威模拟。
2. 射线方向、起点和距离由服务端生成并通过 `validRaycastRequest` 校验。
3. 命中静态物体时 `TargetID=0`，不会扣血。
4. 命中不存在玩家、已死亡玩家或自己时不扣血。
5. 血量使用下限裁剪，避免负数。
6. 目标死亡后移除物理对象；移除失败只记录 warn，不阻塞房间循环。
7. 死亡玩家后续输入会被已有 `!player.Alive` 分支拒绝。

## 性能考虑

1. 每次开火只做一次 raycast；没有开火时不增加额外物理查询。
2. 房间当前默认 2 人，simple 后端遍历玩家 map 成本很低；未来多人房间如需高频射击，可以改成批量 raycast 或加入冷却配置。
3. PhysX 仍走已有单次 raycast 调用，不引入额外 goroutine 或锁竞争。
4. 不在快照外新增即时广播，避免增加发送队列压力。

## 验证方式

1. 运行 `go test ./src/roomserver/logic`，验证房间逻辑、同步和 simple 射击测试。
2. 运行 `go test ./src/roomserver/service`，验证输入处理和会话行为未回归。
3. 运行 `go test ./src/roomserver/physx -tags physx`，如果本机 PhysX SDK 和库可用，则验证 PhysX raycast 忽略自身逻辑。
4. 运行 `go test ./src/roomserver/...` 做 roomserver 非 physx 默认测试覆盖。

## 自我审查

1. 项目结构：射击扣血属于 room 内业务逻辑，放在 `logic` 层；物理查询能力仍封装在 `PhysicsWorld` 接口，符合 service -> logic -> physics 的现有边界。
2. 过度设计：不新增武器系统、弹药、射速、击杀结算或新协议消息，避免超出“命中一次扣20血量”的范围。
3. 协议风险：已有 `fire` 和 `hp` 字段足够表达本次需求，不修改 proto 字段编号，兼容性风险低。
4. 边界条件：需要特别处理自命中、静态物体命中、死亡后重复命中、血量负数和 raycast 错误日志。
5. 性能风险：高频开火会带来 raycast 成本，但当前按输入帧触发且双人房间成本可控；暂不加入冷却配置，避免需求外规则。
6. 扩展性：先以常量实现伤害和射程，后续如果需要多武器或配置化，可以把这些常量迁入配置或玩家武器状态。

## 修正后的最终方案

按上述方案实施：只补齐服务端权威射击命中扣血，复用已有输入和快照协议；内部扩展 raycast 忽略自身；simple 后端补齐玩家 raycast 以支持测试；PhysX 后端同步过滤射击者 actor；命中扣 20，血量归零标记死亡并移除物理对象；不在本次改动中加入击杀结束对局或即时命中消息。

等待确认后开始修改代码。
