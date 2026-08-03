# roomserver 玩家击杀死亡数量查询接口方案

## 需求理解

在 roomserver 新增一个客户端可调用接口，用于获取玩家的击杀数量和死亡数量。当前开火扣血逻辑已经能在目标血量归零时标记死亡，但没有保存击杀/死亡统计，也没有查询消息。

## 影响范围

预计修改以下文件：

1. `src/roomserver/protocol/message.go`
   - 新增查询消息类型，例如 `MsgPlayerStatsQuery = 13`、`MsgPlayerStatsResp = 14`
   - 新增 `PlayerStatsQuery`、`PlayerStats`、`PlayerStatsResp` JSON 结构
2. `pb/room/room.proto`
   - 同步补充协议文档 message 和 rpc 定义，字段添加简短中文注释
3. `src/roomserver/logic/player.go`
   - 在 `Player` 中增加 `KillCount`、`DeathCount` 字段
4. `src/roomserver/logic/room.go`
   - 击杀结算时给射击者 `KillCount++`，目标 `DeathCount++`
   - 新增房间内统计查询事件，保证读取发生在 room 单线程事件循环内
   - `GameOver` 结束状态可复用扩展后的 `PlayerState` 携带统计
5. `src/roomserver/logic/room_manager.go`
   - 新增 `QueryPlayerStats(playerID)`，定位玩家所在房间并委托房间查询
6. `src/roomserver/service/server.go`
   - 在消息分发中处理客户端查询请求，校验已入房后调用 logic 层，发送响应或错误
7. `src/roomserver/logic/shooting_test.go` / `room_*_test.go`
   - 补充击杀后计数递增、查询自己/指定玩家统计的逻辑测试
8. `src/roomserver/service/*_test.go`
   - 如现有测试结构方便，补充服务层查询消息的参数校验测试
9. `src/roomserver/learning/02-protocol-fields.md` 或 `README.md`
   - 更新消息类型与字段说明，避免协议文档和代码脱节

## 设计方案

### 协议结构

客户端发送 `MsgPlayerStatsQuery`：

```json
{
  "player_id": 0
}
```

含义：

1. `player_id=0` 或不传时，查询当前 session 绑定玩家自己的统计。
2. `player_id>0` 时，查询同房间内指定玩家的统计；如果目标不在当前房间，返回错误，避免跨房间探查。

服务端返回 `MsgPlayerStatsResp`：

```json
{
  "ok": true,
  "content": "ok",
  "room_id": "room-xxx",
  "server_tick": 120,
  "stats": {
    "player_id": 1001,
    "kill_count": 2,
    "death_count": 1
  }
}
```

### 统计模型

1. `Player` 增加：
   - `KillCount int`：击杀数量
   - `DeathCount int`：死亡数量
2. 玩家加入房间时初始化为 0。
3. `applyFireDamage` 中，当目标从存活变为死亡时：
   - 如果 `shooter.ID != target.ID`，射击者 `KillCount++`
   - 目标 `DeathCount++`
4. 因为死亡后目标 `Alive=false` 且移除物理对象，重复 raycast 不会重复计死亡。

### 查询流程

1. `Server.HandleMessage` 新增 `MsgPlayerStatsQuery` 分支。
2. `handlePlayerStatsQuery`：
   - 检查 session 已绑定 `PlayerID`
   - 解码请求；空 payload 可视为查询自己，非法 JSON 返回 `bad_request`
   - 默认目标为 session 自己；若传入 `player_id`，使用该目标
   - 调用 `RoomManager.QueryPlayerStats(requesterID, targetID)`
   - 成功后发送 `MsgPlayerStatsResp`
3. `RoomManager.QueryPlayerStats`：
   - 根据 requesterID 找到 requester 所在房间
   - 调用 `Room.QueryPlayerStats(requesterID, targetID)`
4. `Room.QueryPlayerStats`：
   - 通过 room event 投递查询请求，附带一次性响应 channel
   - 房间 goroutine 串行读取 `players` map，避免并发读写
   - 返回目标统计或错误

### 快照兼容扩展

同时建议把 `kill_count`、`death_count` 加到现有 `PlayerState`：

1. Snapshot 和 GameOver 已经会下发 `PlayerState`，带上统计可以让客户端 UI 被动刷新。
2. JSON 新增字段对旧客户端兼容；proto 在 `PlayerState` 末尾新增字段编号 9、10，不改旧字段编号。
3. 查询接口仍保留，用于客户端打开结算/计分面板时主动拉取，避免只依赖最近一次快照。

## 兼容性

1. 新增消息类型 13/14，不修改既有消息编号。
2. `PlayerState` 只在末尾追加 `kill_count`、`death_count`，不改旧字段含义。
3. 旧客户端忽略 JSON 新字段即可继续运行。
4. 新查询接口要求玩家已入房；未入房请求返回 `not_joined`。
5. `pb/room/room.proto` 只作为当前协议文档和后续生成基础，同步补齐注释和字段编号，避免未来 protobuf 化时语义不一致。

## 健壮性

1. 查询方必须已入房，防止未认证连接查询房间数据。
2. 指定 `player_id` 只能查询同房间玩家；不存在或不在同房间返回 `stats_failed`。
3. 房间查询通过事件队列串行执行，避免直接跨 goroutine 读取 `Room.players`。
4. 查询事件使用带缓冲响应 channel，并设置短超时，避免房间已停止或队列阻塞导致 service goroutine 永久等待。
5. 事件队列满时返回 `room event queue full`，由 service 转成错误响应。
6. 击杀统计只在目标从存活到死亡的一次状态转换中递增，避免重复计数。

## 性能考虑

1. 击杀/死亡计数是玩家结构体上的整数递增，开销极低。
2. 查询是低频控制消息，不参与每帧广播路径。
3. 查询通过 room 事件队列会占用一个事件槽；正常 UI 查询频率很低，不会影响 tick。若未来需要高频排行榜，可以考虑在 room 内维护定期快照缓存。
4. `PlayerState` 增加两个数字字段会略增快照 payload，但当前双人房间影响很小。

## 验证方式

1. `gofmt` 格式化修改过的 Go 文件。
2. `go test ./src/roomserver/logic` 验证击杀统计和房间查询逻辑。
3. `go test ./src/roomserver/service` 验证服务层消息处理不回归。
4. `go test ./src/roomserver/...` 验证 roomserver 相关包。
5. 如本机安装 `protoc` 和 Go 插件，则运行 `make proto` 验证 proto 可生成；如果环境缺失，会如实说明未执行或失败原因。

## 自我审查

1. 项目结构：统计属于房间内业务状态，放在 logic 层；service 只做消息解析、基础校验和响应组装，符合分层要求。
2. 过度设计：不引入数据库持久化、排行榜系统、助攻、连杀、复活或结算规则，只做当前房间内统计和查询。
3. 协议风险：新增消息编号和字段编号都追加在末尾，不复用旧编号；字段命名使用 snake_case。
4. 边界条件：需要处理未入房、目标不存在、房间停止、事件队列满、查询超时、死亡重复命中等情况。
5. 性能风险：直接在查询时走事件队列比跨 goroutine 加锁更稳；低频查询可接受。快照多两个字段对双人房影响很小。
6. 扩展性：`PlayerStats` 独立结构后续可追加伤害、助攻等字段；`PlayerState` 只放客户端当前常用展示字段。
7. 简化修正：不新增单独的 repo 层，因为统计是房间内临时状态，不涉及数据库持久化。

## 修正后的最终方案

按上述方案实施：在 roomserver 内新增房间内临时 K/D 统计，击杀时更新计数；新增 `MsgPlayerStatsQuery` / `MsgPlayerStatsResp` 查询接口；查询由 service 层解析请求并委托 RoomManager，Room 通过事件队列串行读取；同时在 `PlayerState` 末尾追加 `kill_count`、`death_count`，让快照和 GameOver 也能携带统计。不加入持久化排行榜、复活或胜负结算规则。

等待确认后开始修改代码。
