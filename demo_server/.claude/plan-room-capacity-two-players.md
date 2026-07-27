# 每房间两人容量调整方案

## 需求理解

将当前每个房间最多 10 人的配置和默认行为改为每个房间最多 2 人。匹配分配时第三个玩家应进入新房间；roomserver 实际入房时同一房间超过 2 人应拒绝。

## 影响范围

预计修改：

- `config/config.yaml`
  - `match_server_01.max_players_per_room` 改为 2
  - `match_server_01.room_servers[].max_players_per_room` 改为 2
  - `room_server_01.max_players_per_room` 改为 2

- `config/config.go`
  - matchserver 配置缺省值从 10 改为 2
  - 避免 YAML 未配置时仍回退到 10

- `src/matchserver/logic/matcher.go`
  - matcher 内部 normalize 的单房间人数缺省值从 10 改为 2
  - 保证直接构造 `MatchServerConfig` 的测试或未来调用也按 2 人房间处理

- `src/roomserver/config/config.go`
  - roomserver `DefaultConfig().MaxPlayersPerRoom` 从 10 改为 2
  - 因当前 `src/roomserver/cmd/main.go` 直接使用 `roomconfig.DefaultConfig()`，这里是 roomserver 运行时实际生效点

- `src/roomserver/logic/room_manager.go`
  - `NewRoomManager` 参数非法时的兜底人数从 10 改为 2

- `src/roomserver/logic/aoi.go` 与 `src/roomserver/README.md`
  - 更新“10 人房间”等说明，避免文档和注释与行为不一致

- 可能新增或补充测试文件
  - `src/matchserver/logic/matcher_test.go`：验证第 3 个玩家分配到新房间
  - `src/roomserver/config/config_test.go`：验证默认配置和 Normalize 默认值为 2

## 设计方案

1. 将“单房间最大人数”的运行时配置统一改为 2。
2. matchserver 的分配逻辑不改算法，只改容量参数：`room.players < s.maxPlayersPerRoom` 会自然变成每房间最多分配 2 个占位。
3. roomserver 的入房逻辑不改算法，只改容量参数：`len(r.players) >= r.maxPlayers` 会自然变成第 3 人入同一房间时返回 `room is full`。
4. 不新增协议字段，不修改 token 结构，不修改 logicserver 调用链。
5. 不引入新的配置层或依赖，保持当前项目的配置方式。

## 兼容性

- 不影响 proto、KCP 消息结构、gRPC 接口和 room token claim 字段。
- 已有客户端无需改协议，只会观察到匹配房间分配数量变化。
- 已有配置文件如果仍手动写 10，会继续覆盖默认值；本次会同步修改仓库内默认 `config/config.yaml`。

## 健壮性

- 对非法或缺省 `max_players_per_room <= 0` 的情况，matchserver、roomserver 和 RoomManager 都回退到 2。
- matchserver 和 roomserver 两侧都设置为 2，避免 matchserver 分配 3 人到同房间但 roomserver 拒绝的配置不一致问题。
- 保留现有满房错误路径：matchserver 无可用房间时返回 `roomserver full`，roomserver 单房满员时返回 `room is full`。

## 性能考虑

- 每房间人数从 10 降到 2，会降低单房间 AOI 遍历、快照组包和物理玩家数量的开销。
- matchserver 房间数量可能增加，但当前只是内存切片遍历；在既有 `max_rooms=1000` 下没有新的高频跨语言调用、锁竞争或网络开销。
- 不改变 roomserver tick、snapshot 或 PhysX 调用方式。

## 验证方式

计划执行：

- `gofmt` 格式化改动的 Go 文件
- `go test ./src/matchserver/logic ./src/roomserver/config ./src/roomserver/logic`
- 必要时执行 `go test ./...`，如果因外部服务、PhysX build tag 或环境依赖失败，会如实说明失败点

## 自我审查

检查结果：

1. 不能只改 `config/config.yaml`，因为 roomserver 当前启动入口使用 `roomconfig.DefaultConfig()`，不会读取 YAML。
2. 不能只改 roomserver，否则 matchserver 仍可能把 3 到 10 个玩家分配到同一个房间，导致客户端拿到 token 后入房失败。
3. 不能只改 matchserver，否则手动指定同房间或配置不一致时 roomserver 仍允许最多 10 人。
4. 不需要修改 proto 或 room token，因为人数上限是服务端运行时策略，不是协议数据。
5. 当前 matchserver 只增加房间占位人数，没有玩家离开后的释放链路；这是已有设计限制，本次只调整容量，不扩展房间生命周期同步，避免需求外重构。

## 修正后的最终方案

同步修改 matchserver 分配容量、roomserver 实际入房容量、配置缺省值和仓库配置文件，将所有单房间默认人数统一为 2；补充最小测试验证默认值和匹配分配行为；更新相关注释和 README 中的 10 人描述。确认后开始实现。