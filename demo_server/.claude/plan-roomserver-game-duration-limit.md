# roomserver 对局 3 分钟限时方案

## 需求理解

两个玩家都成功进入同一个 roomserver 房间后，服务端开始 3 分钟倒计时；倒计时结束后，本局游戏结束，房间内玩家都能收到明确的结束通知。当前项目是 2 人房配置，所以开始条件按房间满员触发，等价于两名玩家都进入房间。

## 影响范围

预计修改这些文件：

- `src/roomserver/protocol/message.go`：新增对局开始、对局结束消息类型和 JSON payload 结构
- `pb/room/room.proto`：同步补充协议文档结构，保持字段注释
- `gen/room/room.pb.go`、`gen/room/room_grpc.pb.go`：执行 `make proto` 后更新生成代码
- `src/roomserver/config/config.go`：新增对局时长配置，默认 `3 * time.Minute`
- `src/roomserver/service/server.go`：把对局时长传给 `RoomManager`
- `src/roomserver/logic/room_manager.go`：创建房间时传入对局时长，并在房间结束后清理 `rooms` 和 `playerRooms`
- `src/roomserver/logic/room.go`：维护对局开始 tick、结束 tick、结束状态；到期广播结束消息并停止房间
- `src/roomserver/logic/*_test.go`、`src/roomserver/config/config_test.go`：新增/补充单元测试
- `src/roomserver/learning/02-protocol-fields.md`、必要时 `src/roomserver/README.md`：补充新增消息说明

## 设计方案

### 协议设计

新增两个服务端下行消息：

- `MsgGameStart = 11`：对局开始通知
- `MsgGameOver = 12`：对局结束通知

新增 payload：

- `GameStart`
  - `room_id`：房间 ID
  - `server_tick`：通知发出时服务端 tick
  - `start_tick`：倒计时开始 tick
  - `end_tick`：预计结束 tick
  - `duration_seconds`：对局时长，默认 180
  - `server_time`：服务端 Unix 毫秒时间

- `GameOver`
  - `room_id`：房间 ID
  - `server_tick`：结束通知发出时服务端 tick
  - `start_tick`：对局开始 tick
  - `end_tick`：对局结束 tick
  - `reason`：结束原因，当前固定为 `time_limit`
  - `server_time`：服务端 Unix 毫秒时间
  - `players`：结束时玩家权威状态快照，便于客户端停留在最终状态

`JoinRoomAck` 追加兼容字段：

- `game_duration_seconds`：默认 180
- `game_started`：当前房间是否已开始
- `game_start_tick`：已开始时的开始 tick，未开始为 0
- `game_end_tick`：已开始时的结束 tick，未开始为 0

这样第二名玩家入房时，即使开始通知因为队列或网络晚到，客户端仍能从入房响应知道倒计时信息；第一名玩家则通过 `MsgGameStart` 得到开始时间。

### 房间生命周期

`Room` 增加字段：

- `gameDuration time.Duration`：对局时长配置
- `gameDurationTicks int64`：按 tickRate 换算后的对局时长
- `gameStarted bool`：是否已经开始倒计时
- `gameEnded bool`：是否已经结束
- `gameStartTick int64`：开始 tick
- `gameEndTick int64`：结束 tick
- `onFinished func(roomID string, playerIDs []uint64)`：房间结束后的管理器回调

入房流程：

1. `handleJoin` 保留现有 nil、满员、重复、出生点、物理 actor 创建校验
2. 如果房间已经开始或已经结束，拒绝新的入房，返回 `JoinRoomAck{OK:false}`，避免中途补位进入已开局房间
3. 玩家成功写入 `players` 和 `syncStates` 后，判断 `len(players) >= maxPlayers`
4. 达到满员且未开始时，设置 `gameStarted=true`、`gameStartTick=r.tick`、`gameEndTick=r.tick+gameDurationTicks`
5. 先发送当前玩家 `JoinRoomAck`，再广播 `MsgGameStart`，保证第二名玩家的消息顺序是入房成功后再开局通知

更新流程：

1. `update` 每 tick 先推进 `r.tick` 和 `currentTick`
2. 如果对局已开始且未结束，并且 `r.tick >= gameEndTick`，广播 `MsgGameOver`
3. 广播成功后标记 `gameEnded=true`，调用 `onFinished` 清理 RoomManager 索引，随后停止房间 loop
4. 已结束房间不再处理输入、移动、快照和纠偏

管理器清理：

- `RoomManager` 创建房间时注入结束回调
- 回调在锁内删除 `rooms[roomID]`
- 同时删除该房间当前玩家的 `playerRooms` 映射
- 不主动关闭 session，避免 `MsgGameOver` 刚入队就被关闭连接打断；客户端收到结束消息后自行离开或断开，服务端仍有 read timeout 兜底

## 兼容性

- 新增消息类型 11、12，不修改已有消息编号
- `JoinRoomAck` 只追加 JSON 字段，旧客户端忽略未知字段即可继续入房
- `pb/room/room.proto` 只追加字段，不复用旧字段编号
- 中途加入已开始房间会从允许补位变为拒绝；当前 matchserver 按 2 人满房分配，正常路径不受影响
- 当前不做胜负和积分结算，只做 `time_limit` 结束通知，后续伤害/击杀系统接入后可在 `GameOver` 上扩展 winner/result 字段

## 健壮性

- 对局时长配置小于等于 0 时回退默认 3 分钟
- tickRate 非法时沿用现有默认 20，并基于归一化 tickRate 计算结束 tick
- `MsgGameStart` 和 `MsgGameOver` 构造失败会记录错误，不让房间 loop panic
- 结束消息使用普通控制队列 `Session.Send`，不走快照覆盖队列，避免被快照丢弃
- 房间结束后忽略输入，并通过 RoomManager 清理映射，避免后续请求继续写入已停止房间
- 玩家在开局前离开不会启动倒计时；开局后玩家离开不重置倒计时，本局仍按时间结束

## 性能考虑

- 计时使用房间已有 tick，不新增每房间独立 timer goroutine
- 每 tick 只做一次布尔和整数比较，开销极低
- `GameStart` 和 `GameOver` 只广播一次，消息量与房间人数线性相关，当前默认 2 人
- 房间结束后停止 loop 并关闭 physics world，释放物理资源
- RoomManager 清理只在结束时执行一次加锁，不影响高频输入路径

## 验证方式

计划执行：

- `gofmt` 格式化修改过的 Go 文件
- `make proto` 生成 proto 代码
- `go test ./src/roomserver/...` 验证 roomserver 单元测试
- 如 proto 工具链不可用，再执行 `go test ./src/roomserver/logic ./src/roomserver/config ./src/roomserver/protocol` 并明确说明 proto 生成未执行原因

新增测试重点：

- 默认对局时长为 3 分钟，非法配置回退默认值
- 第一名玩家入房不启动倒计时
- 第二名玩家入房后启动倒计时，并向两个玩家广播 `MsgGameStart`
- 达到结束 tick 后只广播一次 `MsgGameOver`
- 对局开始后拒绝新玩家加入
- 房间结束后 RoomManager 清理房间和玩家索引

## 自我审查

检查结果：

1. 当前 roomserver 没有玩法结算协议，如果只在服务端内部 stop 房间，客户端无法知道本局为何结束，因此必须新增下行结束消息
2. 如果到期后立即 `Session.Close`，存在结束消息刚入队尚未写出就断连的风险，因此不能由房间结束流程主动关连接
3. 如果房间停止但 RoomManager 不删除 `rooms`，后续同 roomID 入房会投递到无人消费的事件队列，属于隐蔽 bug，因此必须在结束回调里清理索引
4. 直接写死 180 秒会降低测试和后续配置能力，因此使用配置默认值，同时保持默认行为就是 3 分钟
5. 当前 matchserver 不接收 roomserver 的结束回报，匹配器内的房间占位不会释放；这属于已有简化匹配模型的限制。本次只处理 roomserver 单局结束，不扩展跨服务房间状态上报，避免扩大范围

## 修正后的最终方案

采用“roomserver 本地权威计时 + 新增开始/结束通知 + RoomManager 清理”的实现：

- 在第二名玩家成功入房后，以当前房间 tick 作为 `game_start_tick`，用配置默认 `3m` 换算为 `game_end_tick`
- 给两名玩家广播 `MsgGameStart`，到期广播一次 `MsgGameOver{reason:"time_limit"}`
- 房间结束后停止 room loop、释放 physics world，并从 RoomManager 删除房间和玩家映射
- 不做胜负、积分、matchserver 状态回收，本次保持为最小可用的时间限制和结束通知

等待确认后再开始修改代码。