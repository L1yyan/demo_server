# Roomserver 击杀后复活与 5 秒无敌方案

## 需求理解

玩家在房间内被击杀后，不再长期停留在死亡状态，而是立即复活到自己入房时分配的出生点，并获得 5 秒无敌时间。无敌期间受到开火命中时不扣血、不重复计死亡；玩家仍保持可移动、可开火，战绩中的击杀数和死亡数仍按击杀事件累计。

默认理解：本次不增加复活倒计时，击杀发生的同一服务端 tick 完成复活；如果需要“死亡后等待 N 秒再复活”，需要另行调整设计。

## 影响范围

预计修改文件：

- `src/roomserver/logic/player.go`：增加玩家无敌截止帧字段和 tick 感知的状态转换方法
- `src/roomserver/logic/room.go`：调整伤害结算、击杀后复活、无敌判定、快照/纠偏状态构造
- `src/roomserver/logic/sync.go`：让权威帧状态携带无敌信息，纠偏消息能同步复活后的状态
- `src/roomserver/protocol/message.go`：在 `PlayerState` 中补充无敌状态字段
- `pb/room/room.proto`：同步补充 `PlayerState` 的 proto 字段与简短注释
- `src/roomserver/logic/shooting_test.go`：更新击杀测试，并新增无敌期挡伤害和过期后可受伤测试
- 可能小幅调整 `src/roomserver/logic/room_spawn_test.go`：覆盖快照状态中的无敌字段

不计划修改 service/repo 层；该功能属于房间内战斗状态结算，保持在 logic 层。

## 设计方案

1. 增加基础常量

- `defaultPlayerHP = 100`：替换当前入房和复活时的硬编码血量
- `defaultRespawnInvincibleDuration = 5 * time.Second`：5 秒无敌时间
- 通过现有 `durationToTicks` 转换为房间 tick，避免新增 timer 或 goroutine

2. 扩展玩家状态

- 在 `Player` 增加 `InvincibleUntilTick int64`，表示无敌结束帧号；`<= 当前 tick` 表示当前不无敌
- 增加 `IsInvincible(serverTick int64) bool` 判断方法
- 增加 tick 感知的 `ToStateAt(serverTick int64)`，用于快照、结束状态、纠偏状态输出
- 保留现有 `ToState()`，避免已有测试或调用点大面积改动；房间发送路径改用 `ToStateAt(r.tick)`

3. 扩展协议状态

- `protocol.PlayerState` 增加：
  - `Invincible bool json:"invincible"`：当前是否处于无敌
  - `InvincibleUntilTick int64 json:"invincible_until_tick"`：无敌结束帧号，客户端可结合 `server_tick` 展示剩余时间
- `pb/room/room.proto` 的 `PlayerState` 增加字段编号 11 和 12：
  - `bool invincible = 11; // 是否无敌`
  - `int64 invincible_until_tick = 12; // 无敌结束帧号`

4. 调整伤害与复活流程

- `applyFireDamage` 开头增加无敌判定：目标处于无敌时直接忽略本次伤害
- 非致命伤害维持现有逻辑：扣血、保存权威状态
- 致命伤害改为：
  - 只在本次血量归零时累计 shooter 击杀和 target 死亡
  - 调用 `respawnPlayerAtSpawn(ctx, target)` 将目标放回自己的 `SpawnID` 出生点
  - 重置目标坐标、朝向、Pitch、HP、Alive 和 `InvincibleUntilTick`
  - 使用 `physics.SetPlayerPosition` 移动已有物理 actor；如果物理层缺失该玩家，再尝试 `AddPlayer`
  - 保存目标权威状态
  - 对预测同步客户端发送一次 `reason=respawn` 的当前状态纠偏，减少客户端仍停留在死亡前位置的时间

5. 同步状态处理

- `playerFrameState` 增加 `InvincibleUntilTick`
- `frameStateFromPlayer(tick, player)` 记录该帧无敌截止帧
- `toPlayerState()` 根据帧号计算 `Invincible`
- 复活时清理该玩家当前 tick 之后的旧预测状态，避免客户端死亡前上报的预测位置在复活后持续触发无意义纠偏

6. 无敌过期

- 不启动后台定时器
- 每个房间 tick 中用当前 tick 判断是否仍无敌；必要时可在 `updatePlayers` 中把已过期的 `InvincibleUntilTick` 清零，保持快照字段干净

## 兼容性影响

- 服务端 JSON 快照和纠偏中的 `PlayerState` 会新增两个字段；这是向后兼容扩展，旧客户端可以忽略未知字段
- proto 仅追加新字段编号，不修改或复用已有字段编号
- 战绩接口不变，`kill_count` / `death_count` 语义不变
- 行为层面有明显变化：击杀后目标会立即以满血复活，因此客户端不应再依赖“HP=0 且长时间不可见”作为死亡持续状态

## 健壮性处理

- 如果目标已经不存在、已非存活状态或处于无敌期，本次伤害直接忽略，避免重复死亡计数
- 复活时优先使用玩家原本的 `SpawnID`，不通过 `nextSpawnPoint` 重新分配，避免抢占别人的出生点
- 如果出生点异常缺失或物理层复位失败，会记录日志并保留目标死亡状态，避免逻辑层显示已复活但物理层没有 actor 的不一致
- 复活后的权威状态会立即保存，并向预测模式玩家下发纠偏
- 清理旧预测状态，降低复活后连续纠偏的风险

## 性能考虑

- 无敌时间用 tick 数比较实现，没有额外 goroutine、timer 或锁竞争
- 每次命中只增加一次整数比较，开销可忽略
- 物理层只在死亡复活时调用一次 `SetPlayerPosition`，不是每帧额外 cgo 调用
- 快照新增两个字段，双人房 payload 增量很小

## 验证方式

实现后执行：

- `gofmt` 格式化修改的 Go 文件
- `go test ./src/roomserver/logic` 验证房间、射击、同步相关逻辑
- `go test ./src/roomserver/protocol ./src/roomserver/service ./src/roomserver/config` 验证协议结构和服务层未破坏
- 如本地已安装 `protoc` 及插件，再执行 `make proto` 或等价 proto 生成/语法校验；若工具缺失，会如实说明未执行原因

## 自我审查

1. 是否遗漏项目结构：功能属于 roomserver logic 层，不需要下沉 repo，也不应放到 service 层，符合现有分层
2. 是否过度设计：不新增配置项、不新增消息类型、不新增复活队列，只用 tick 字段表达 5 秒无敌，范围可控
3. 协议风险：新增字段需要客户端适配展示，但字段编号追加且含义清晰，不影响旧字段
4. 边界风险：即时复活会让客户端看不到持续死亡状态；这是当前需求的直接解释，已在兼容性中明确
5. 物理一致性风险：必须用 `SetPlayerPosition` 同步物理 actor；失败时不能假装复活成功
6. 预测同步风险：复活是位置突变，必须发当前状态纠偏并清理旧预测状态，避免弱网纠偏循环
7. 性能风险：无敌判断和复活重定位都不是高频重操作，符合当前房间规模

## 修正后的最终方案

采用“立即复活 + tick 截止帧无敌”的实现。击杀时完成 KDA 计数，然后把目标放回自己的出生点，满血、存活、设置 `InvincibleUntilTick = 当前 tick + 5 秒对应 tick 数`。伤害结算遇到无敌目标直接跳过扣血。快照和纠偏新增 `invincible` / `invincible_until_tick`，客户端可据此展示无敌状态。物理层只做复活时的一次位置同步，失败时保留死亡状态并记录日志。测试覆盖击杀后复活、无敌期不受伤、5 秒后重新受伤、战绩仍正确。

等待用户确认后再开始修改业务代码。
