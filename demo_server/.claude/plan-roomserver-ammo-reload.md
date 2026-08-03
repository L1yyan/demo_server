# Roomserver 子弹弹夹与换弹功能方案

## 需求理解

为 roomserver 设计服务端权威的弹药系统：每名玩家主弹夹容量 30 发，后备弹药 60 发；玩家按 R 触发换弹，换弹时间 2 秒；换弹完成时按“补满主弹夹”规则从后备弹药扣除，不丢弃主弹夹剩余子弹。若后备弹药不足以补满 30 发，则后备弹药清零并全部补入主弹夹。开火消耗主弹夹子弹，主弹夹为空时不能开火。

## 影响范围

预计修改：

- `pb/room/room.proto`：输入帧增加 `reload`，玩家状态增加主弹夹、后备弹药、换弹状态字段
- `src/roomserver/protocol/message.go`：同步 JSON 协议结构字段和消息注释
- `src/roomserver/logic/player.go`：玩家状态增加弹药与换弹字段，并输出到 `PlayerState`
- `src/roomserver/logic/movement.go`：`authoritativeInput` 和输入清洗增加 Reload
- `src/roomserver/logic/sync.go`：权威历史帧记录弹药与换弹状态，纠偏状态包含弹药
- `src/roomserver/logic/room.go`：入房、复活、每 tick 更新、开火和换弹结算接入弹药规则
- `src/roomserver/logic/shooting_test.go` / `sync_test.go`：补充开火耗弹、空弹夹禁止开火、换弹补弹、换弹期间不能开火、纠偏携带弹药等测试
- 文档：`src/roomserver/README.md` 和 `src/roomserver/learning/02-protocol-fields.md`、`04-authoritative-movement-and-sync.md` 按实际字段更新

不计划新增数据库、repo 层或 service 层业务逻辑；这是房间内实时状态，保持在 logic 层即可。

## 设计方案

### 1. 协议设计

在客户端输入中新增 `reload`：

- `PlayerInput.Reload bool json:"reload"`，旧单帧输入可用
- `PlayerInputFrame.Reload bool json:"reload"`，批量输入推荐使用
- `pb/room/room.proto` 中 `PlayerInput` 使用新字段编号 8，`PlayerInputFrame` 使用新字段编号 9，避免复用已有编号

在玩家状态中新增弹药/换弹字段，用于 snapshot 和 correction：

- `magazine_ammo`：当前主弹夹子弹数
- `reserve_ammo`：后备子弹数
- `reloading`：是否正在换弹
- `reload_finish_tick`：换弹完成帧号，客户端可用于 UI 倒计时

`PlayerState` 当前已用于 `Snapshot`、`GameOver` 和 `StateCorrection`，把弹药字段放入这里能让客户端从快照和纠偏中都拿到服务端权威状态。协议只追加字段，不改已有字段编号。

### 2. 玩家状态模型

在 `Player` 中新增：

- `MagazineAmmo int`：主弹夹当前子弹
- `ReserveAmmo int`：后备子弹
- `Reloading bool`：是否正在换弹
- `ReloadFinishTick int64`：换弹完成帧号

新增常量：

- `defaultMagazineCapacity = 30`
- `defaultReserveAmmo = 60`
- `defaultReloadDuration = 2 * time.Second`

入房初始化：主弹夹 30、后备 60、非换弹。复活建议重置弹药为 30/60，并清理换弹状态；原因是当前复活已经重置 HP、位置和无敌状态，弹药也作为玩家战斗状态一起重置，行为清晰。如果你希望复活保留当前剩余弹药，实施前可以改为保留策略。

### 3. 输入与权威 tick 流程

`reload` 和 `fire` 都作为单帧动作处理，沿用缺帧输入时强制置 false，避免弱网缺帧导致重复开火或重复换弹。

每个服务端 tick 的顺序建议为：

1. `inputForTick` 取当前权威输入
2. `simulatePlayerTick` 更新视角和移动
3. 若已到 `ReloadFinishTick`，先完成换弹结算
4. 若当前精确输入里 `reload=true`，尝试开始换弹
5. 若当前精确输入里 `fire=true`，尝试开火

同一帧如果同时 `reload=true` 和 `fire=true`，服务端优先处理换弹请求，进入换弹后本帧不开火。这样能避免客户端同帧既开火又换弹造成歧义，也符合“按 R 换弹”是显式动作的规则。

### 4. 换弹规则

开始换弹需要满足：

- 玩家存活
- 当前没有处于换弹中
- 主弹夹未满
- 后备弹药大于 0

开始换弹时只设置：

- `Reloading = true`
- `ReloadFinishTick = r.tick + durationToTicks(defaultReloadDuration, r.tickRate)`

真正补弹发生在完成帧：

- `need := 30 - MagazineAmmo`
- `take := min(need, ReserveAmmo)`
- `MagazineAmmo += take`
- `ReserveAmmo -= take`
- 清理 `Reloading` 和 `ReloadFinishTick`

如果主弹夹仍有子弹，比如 12/30、后备 60，完成后是 30/42，不丢弃原来的 12 发。如果 12/30、后备 10，完成后是 22/0。

### 5. 开火规则

`handlePlayerFire` 前增加弹药门禁：

- 换弹中不能开火
- 主弹夹 <= 0 不能开火
- 可以开火时先扣 1 发，再进行 raycast 和伤害结算

“先扣弹再命中判定”能保证空枪、未命中和命中都符合常见射击规则：只要服务端认可开火，就消耗子弹。

### 6. 纠偏与快照

`frameStateFromPlayer`、`toPlayerState`、`Player.ToStateAt` 都加入弹药字段，确保：

- 快照能同步当前弹药和换弹状态
- 预测纠偏能把客户端弹药/换弹 UI 拉回服务端权威状态
- GameOver 结束状态也包含最终弹药状态

当前 `PredictedPlayerState` 只校验位置和视角，不把弹药放入预测误差判定，避免客户端每次射击或换弹 UI 不一致都触发位置纠偏。弹药以快照和已有 correction 的 `PlayerState` 下发为权威来源。

## 错误处理与边界

- 非法浮点输入仍按现有 `sanitizePlayerInput` 拒绝；`reload` 是 bool，不需要额外范围校验
- 重复 input tick 仍按现有逻辑丢弃，避免重复消耗弹药或重复开始换弹
- 缺帧沿用输入时 `Fire/Jump/Reload` 都置 false
- 后备为 0、主弹夹满、已在换弹中时重复按 R 不改变状态
- 换弹期间收到开火输入不消耗子弹、不造成伤害
- 玩家死亡/复活时清理换弹状态，避免死亡前启动的换弹在复活后异常完成
- 玩家离开房间无需额外清理，状态随 `Player` 移除

## 兼容性

- JSON 协议新增字段对旧客户端兼容：旧客户端不发送 `reload` 时默认 false；旧客户端忽略 snapshot 里的新增弹药字段也不会解析失败
- Proto 只追加字段编号，不修改或复用已有编号
- 旧 `MsgPlayerInput` 也会支持 reload，避免单帧输入链路能力不一致
- 这次不新增消息类型，降低客户端接入成本

## 性能考虑

- 弹药和换弹状态是每玩家几个基础字段，内存开销极低
- 每 tick 只增加常数级判断，不涉及额外 goroutine、锁或网络往返
- 不增加 PhysX/cgo 调用；只有在真正开火且弹药允许时才继续执行现有 raycast
- `PlayerState` 增加几个字段会略增 snapshot payload，但当前人数上限很小，影响可控

## 验证方式

计划执行：

- `gofmt` 格式化修改过的 Go 文件
- `make proto` 生成/校验 proto 代码，如果本地 protoc 插件可用
- `go test ./src/roomserver/logic ./src/roomserver/protocol ./src/roomserver/service ./src/roomserver/config`

重点单测：

- 初始弹药为 30/60
- 每次权威开火扣 1 发，命中仍按现有伤害结算
- 主弹夹为空时开火不扣血
- 12/60 换弹完成后变为 30/42
- 12/10 换弹完成后变为 22/0
- 主弹夹满或后备为空时按 R 不进入换弹
- 换弹期间开火不消耗弹药且不造成伤害
- 换弹完成帧后可继续开火
- snapshot/correction 中包含弹药与换弹状态

## 自我审查

初版风险点：

1. 如果只在 snapshot 同步弹药，预测模式下客户端可能在 correction 后仍保留错误弹药 UI
2. 如果换弹开始时就扣后备弹药，死亡/复活或状态变化时边界更复杂
3. 如果沿用输入不清理 reload，弱网缺帧会重复触发换弹
4. 如果同帧 fire 和 reload 不定义优先级，客户端和服务端可能表现不一致
5. 如果把弹药做成配置项，会扩大配置加载、默认值和文档改动范围；当前需求给的是固定数值

修正后的最终方案：

- 弹药字段进入 `PlayerState`，同时覆盖 snapshot、game over 和 correction
- 换弹完成时再扣后备弹药并补主弹夹
- 沿用输入时把 `Fire/Jump/Reload` 都置 false
- 同帧 `reload` 优先于 `fire`
- 第一版使用固定常量 30、60、2 秒，不引入配置项；后续如果要多武器或不同枪械，再抽成武器配置

## 等待确认

请确认是否按以上方案实现。需要你确认的唯一策略点：复活后弹药是否重置为 30/60。我的建议是重置，因为当前复活已经重置生命、位置和无敌状态，弹药作为战斗状态一起重置更直观。