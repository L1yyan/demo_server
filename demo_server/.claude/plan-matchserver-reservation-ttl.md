# matchserver 房间占位过期与重复匹配修复方案

## 需求理解

当前客户端点击 Start Match 后一直显示“等待另一名玩家”，但服务端日志里两个玩家分别在不同房间，例如 `room-01-4` 和 `room-01-5`，每个房间 `room_player_count=1`。根因不是客户端等待逻辑，而是 matchserver 在分配房间时只做内存占位 `room.players++`，没有在 room token 过期、入房失败、重复点击或 roomserver 拒绝后释放占位，导致后续玩家被分配到新房间。

本次目标是修复 matchserver 的占位生命周期：同一玩家短时间重复匹配不重复占坑，过期未使用的占位会自动释放，避免两个真实玩家被分散到不同房间。

## 影响范围

预计修改服务端手写 Go 代码和单元测试，不修改客户端、不修改 Unity 资源、不修改 proto。

- `src/matchserver/logic/matcher.go`
  - 将 `roomState.players int` 改为按玩家和模式记录的 reservation。
  - 每次分配前清理已过期 reservation。
  - 同一 `player_id + mode` 在 room token 未过期时重复请求返回同一个分配结果，不重复占用房间人数。
  - 新分配成功后保存 reservation，过期时间与 `token_expire` 对齐。
- `src/matchserver/logic/matcher_test.go`
  - 增加重复匹配不会占满房间的测试。
  - 增加 reservation 过期后释放房间名额的测试。
  - 保留现有 2 人房间和满房行为测试。

不改动：

- `pb/match/match.proto` 和生成代码。
- logicserver `MatchRoom` 调用链。
- roomserver KCP/JoinRoom 协议。
- 客户端等待 HUD 或匹配按钮逻辑。

## 设计方案

1. reservation 数据模型
   - 在 matchserver logic 内部增加非导出的 `reservationKey` 和 `reservationState`。
   - `reservationKey` 使用 `player_id + mode`，避免同一玩家同一模式重复点击创建多个占位。
   - `reservationState` 保存 `server_id`、`server_addr`、`room_id`、`match_id`、`room_token`、`expire_at` 和所属 `roomState`。
   - `roomState` 用 `map[reservationKey]*reservationState` 统计当前有效占位数。
   - `Matcher` 增加全局 `reservations map[reservationKey]*reservationState`，用于快速查找同一玩家的活跃分配。

2. 分配流程
   - `AllocateRoom()` 仍作为 service 层唯一入口，service 不承载业务逻辑。
   - 进入 matcher 锁后先调用 `purgeExpiredReservations(now)`，清理所有 `expire_at <= now` 的 reservation，并同步从 roomState 中删除。
   - 如果当前 `player_id + mode` 已有未过期 reservation，直接返回之前的 `room_id/server_id/server_addr/match_id/room_token/expire_at`。
   - 如果没有活跃 reservation，再按原逻辑查找未满房间；未满判断由 `len(room.reservations) < maxPlayersPerRoom` 决定。
   - 创建新 reservation 前生成 `match_id`、`nonce` 和 room token。签发失败时不保存 reservation，避免失败也占坑。
   - 保存 reservation 后返回分配结果。

3. 和 roomserver 的关系
   - 本次不新增 roomserver -> matchserver 的确认接口，因为那会新增跨服务协议和生命周期回调，改动面更大。
   - 当前修复先解决最直接的问题：失败入房或重复匹配造成的占位会在 token TTL 后释放，同一玩家短时间重试不会继续消耗房间容量。
   - 后续如果需要更严格的房间真实人数同步，再单独增加 roomserver 入房确认、离房/结束回报或 Redis TTL 占位。

## 兼容性

- 不修改 gRPC proto，不需要重新生成 `gen/match`。
- 不修改 room token claims 字段，客户端和 roomserver 的解析逻辑不变。
- `AllocateRoomResp` 字段不变；同一玩家重复请求时可能返回同一个 `match_id` 和 token，这是本次修复的预期行为。
- 已有配置 `token_expire` 继续作为 reservation TTL。仓库当前配置为 1 分钟，短期失败占位最多保留 1 分钟。
- 运行中的 matchserver 内存状态不会自动迁移；部署后需要重启 matchserver 才会清掉旧虚占位。

## 健壮性

- `player_id == 0`、无 roomserver、满房、token secret 为空等错误路径保持现有语义。
- token 签发失败不会保存 reservation，不会产生新的虚占位。
- 每次分配都会清理过期 reservation，不新增后台 goroutine，避免生命周期和退出管理复杂化。
- 所有 reservation 状态仍由 matcher 原有 `sync.Mutex` 保护，避免并发分配超员。
- 同一玩家重复匹配不重复占坑，缓解 UI 重试、网络超时重发或误操作导致的房间占位膨胀。
- 如果 roomserver 因地图碰撞文件缺失仍无法入房，客户端仍会失败；但 matchserver 不会永久保留这次失败造成的占位。

## 性能考虑

- 每次分配会遍历全局 reservation 做过期清理。当前 `max_rooms=1000`、每房间 2 人，上限约 2000 个 reservation，开销很低。
- 不引入 Redis、数据库或跨服务请求，避免增加匹配路径网络开销。
- HMAC room token 签发仍在本地完成；重复匹配直接复用已签 token，反而减少短时间内的签发次数。
- matcher 仍使用一把锁，和现有实现一致；当前项目阶段足够，后续高并发再考虑分片或外部存储。

## 验证方式

计划执行：

- `gofmt -w src/matchserver/logic/matcher.go src/matchserver/logic/matcher_test.go`
- `go test ./src/matchserver/logic`
- 必要时再执行 `go test ./src/matchserver/...` 或 `go test ./...`。如果全仓测试因 PhysX/cgo、外部服务或环境依赖失败，会如实说明失败点。
- 联调验证建议：
  - 重启 matchserver 和 roomserver。
  - 确认 roomserver 地图碰撞路径使用当前 `config/maps/mfps_arena/collision.json`，不再使用旧的 `configs/maps/map_001/collision.json`。
  - 两个不同账号分别点击 Start Match，应拿到同一个 `room_id`，第二人 JoinRoom 后收到 GameStart，客户端不再停在“等待另一名玩家”。

## 自我审查

1. 不能只改客户端等待文案或 HUD，因为服务端日志已显示两个玩家实际在不同房间。
2. 不能只重启 matchserver，因为这只能临时清掉内存虚占位，入房失败或重复匹配后问题会复现。
3. 不直接新增 roomserver 回调接口，是因为这会修改跨服务协议和部署拓扑；当前最小修复用 TTL 和去重就能解决虚占位长期污染。
4. 需要把重复匹配返回同一结果作为显式行为，否则同一玩家连续点击仍可能消耗两个房间名额。
5. 过期清理放在分配路径而不是后台 goroutine，避免无界生命周期和额外退出逻辑。
6. 如果 roomserver 的地图碰撞配置仍错误，入房本身仍会失败；本次修复只能保证失败不会永久污染 matchserver 房间占位。
7. 测试必须覆盖重复匹配和 TTL 释放，否则只保留原有容量测试无法证明问题已修复。

## 修正后的最终方案

采用“matchserver 内存 reservation + token TTL 过期清理 + 同玩家重复请求复用结果”的最小服务端修复：不改协议、不改客户端、不新增跨服务回调，先让 matchserver 的房间占位不会被失败入房或重复匹配永久污染。实现完成后运行 matchserver logic 单元测试，并提醒部署时重启 matchserver，同时确认 roomserver 地图碰撞配置已经是当前路径。

等待确认后开始修改。