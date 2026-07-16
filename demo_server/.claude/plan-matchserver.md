# match_server 第一版实现方案（修正版）

## 需求理解

新增一个尽量简单的匹配服务器 `match_server`。它负责给玩家分配 `roomserver` 和 `room_id`，并签发短期 room token。客户端拿到 token 后直连 roomserver，roomserver 校验 token 后允许入房。

关键修正：room token 不是 roomserver 的私有能力，matchserver 不应调用 `src/roomserver/protocol`。需要先把 room token 结构和签发/解析逻辑抽到公共工具包，例如 `pkg/roomtoken`，然后 roomserver 和 matchserver 都依赖这个工具包。

另外，我已按你前一条反馈核对当前 main 和测试：

- `go test ./...` 当前通过
- `go run ./src/logicserver/cmd` 能启动到 `logicserver started addr=:8080`

我这里没有复现 main 编译报错。如果你本地仍报错，需要把具体错误贴出来，我会按报错继续修。

## 影响范围

预计新增：

- `pkg/roomtoken/token.go`：公共 room token 工具包，放 `RoomTokenClaims`、`Generate`、`Parse`
- `pb/match/match.proto`：matchserver gRPC 协议
- `gen/match/*`：由 proto 生成 Go 与 gRPC 代码
- `src/matchserver/cmd/main.go`：matchserver 启动入口
- `src/matchserver/config/config.go`：matchserver 默认配置与规整
- `src/matchserver/logic/matcher.go`：房间和服务器分配逻辑、room token 签发调用
- `src/matchserver/service/service.go`：gRPC service，只做参数转换和响应组装

预计修改：

- `src/roomserver/protocol/token.go`：删除或改成兼容转发；推荐删除业务实现，把 roomserver 改为直接使用 `pkg/roomtoken`
- `src/roomserver/service/server.go`：把 `protocol.ParseRoomToken` 改为 `roomtoken.Parse`
- `src/roomserver/README.md`：如果需要，更新文档中 token 包路径示例
- `config/config.go`：增加 `MatchServerConfig` 和 `RoomServerNodeConfig`
- `config/config.yaml`：增加 `match_server_01` 配置
- `Makefile`：不用改，现有 `make proto` 会扫描 `pb/**/*.proto`

## 公共 room token 工具包设计

新增 `pkg/roomtoken`，作为跨服务共享包：

```go
package roomtoken

type Claims struct {
    PlayerID uint64 `json:"player_id"`
    RoomID   string `json:"room_id"`
    ServerID string `json:"server_id"`
    MatchID  string `json:"match_id"`
    Nonce    string `json:"nonce"`
    jwt.StandardClaims
}

func Generate(secret string, claims Claims, expire time.Duration) (string, error)
func Parse(secret string, token string) (*Claims, error)
```

字段保持当前 roomserver 已定义的语义：

- `PlayerID`：玩家 ID
- `RoomID`：分配房间 ID
- `ServerID`：目标 roomserver ID
- `MatchID`：本次匹配 ID
- `Nonce`：随机串，后续可以用于一次性 token
- `ExpiresAt`：过期时间
- `Issuer`：建议从现有 `roomserver` 改成更准确的 `matchserver` 或 `demo_server_match`；为降低兼容风险，第一版可以继续用原值，因为 roomserver 当前不校验 issuer

命名建议用 `roomtoken.Claims`，不再用 `RoomTokenClaims`，避免包名重复。

## 协议设计

新增 `pb/match/match.proto`：

```proto
// MatchService 匹配服务
service MatchService {
  // AllocateRoom 分配房间并签发入房令牌
  rpc AllocateRoom(AllocateRoomReq) returns (AllocateRoomResp);
}

// AllocateRoomReq 分配房间请求
message AllocateRoomReq {
  uint64 player_id = 1; // 玩家ID
  string mode = 2; // 匹配模式
}

// AllocateRoomResp 分配房间响应
message AllocateRoomResp {
  bool status = 1; // 状态
  string content = 2; // 返回信息
  string room_id = 3; // 房间ID
  string server_id = 4; // roomserver ID
  string server_addr = 5; // roomserver连接地址
  string match_id = 6; // 匹配ID
  string room_token = 7; // 入房令牌
  int64 expire_at = 8; // 过期时间戳，毫秒
}
```

第一版先让 logicserver 或测试客户端调用 matchserver。matchserver 接收已认证后的 `player_id`，不解析登录 JWT，避免职责扩散。

## 配置设计

`config/config.yaml` 新增：

```yaml
match_server_01:
  listen_addr: ":8090"
  token_secret: "room-token-secret"
  token_expire: "1m"
  max_players_per_room: 10
  room_servers:
    - server_id: "room-01"
      server_addr: "127.0.0.1:9001"
      max_rooms: 1000
      max_players_per_room: 10
```

`token_secret` 必须和 roomserver 的 `TokenSecret` 保持一致，否则 roomserver 会拒绝 token。

## 分层设计

matchserver 第一版仍按职责拆分，但不引入无意义数据库层：

- `service`：实现 gRPC 接口，只做请求校验、调用 logic、响应组装
- `logic`：维护 roomserver 和房间内存状态，负责分配房间、生成 matchID/roomID/nonce、调用 `pkg/roomtoken` 签发 token
- `config`：配置默认值和规整
- 暂不新增 repo：第一版没有数据库或外部持久化存储，repo 层不是必要层。后续如果用 Redis 做房间占位或服务发现，再补 `repo`

## 核心分配算法

第一版使用内存状态：

1. matchserver 启动时读取 roomserver 列表
2. `AllocateRoom(player_id, mode)` 校验 `player_id > 0`
3. 在 roomserver 列表中按顺序选择未满服务器
4. 优先找未满房间
5. 找不到未满房间时创建新房间，room ID 例如 `room-01-1`
6. 房间人数 `+1`
7. 生成 `match_id` 和 `nonce`
8. 调用 `roomtoken.Generate` 签发 room token
9. 返回 `room_id/server_id/server_addr/match_id/room_token/expire_at`

第一版不做客户端连接确认。如果客户端拿 token 后不连接，matchserver 内存人数会偏高；后续可以通过 Redis TTL 占位、roomserver 回调确认、或 matchserver 定时回收解决。

## 错误处理和边界

- `player_id == 0`：返回 `status=false, content="invalid player"`
- 没有可用 roomserver：返回 `status=false, content="no available roomserver"`
- 所有房间满且达到 `max_rooms`：返回 `status=false, content="roomserver full"`
- token secret 为空或签发失败：记录错误日志，返回 `status=false, content="allocate room failed"`
- service 层沿用当前项目风格，业务失败返回 `status/content`，不直接抛 gRPC error
- 分配状态用 `sync.Mutex` 保护，避免并发分配导致房间超员

## 兼容性影响

- room token 字段语义不变，只是从 roomserver 私有包迁移到公共包
- roomserver 校验逻辑会改为依赖 `pkg/roomtoken`，行为保持一致
- 新增 `pb/match`，不影响已有 `pb/logic` 和 `pb/room`
- 新增配置项，不影响已有 logicserver/roomserver 配置
- 如果删除 `src/roomserver/protocol/token.go`，外部旧代码引用 `protocol.GenerateRoomToken` 会失效；当前仓库业务代码只有 roomserver 自己解析 token，建议直接迁移。若想更保守，可以先保留兼容 wrapper，但标注后续删除

## 性能考虑

- 分配逻辑是内存 map/slice 操作，第一版开销很低
- 一把锁保护全局分配状态，第一版够用；高并发后可按 mode 或 server 分片
- room token 签发是本地 HMAC，开销可接受
- 不引入 Redis/MongoDB，避免第一版 matchserver 复杂化

## 验证方式

实现后执行：

- `make proto` 生成 `gen/match` 代码
- `gofmt -w` 格式化新增和修改 Go 文件
- `go test ./...` 全仓编译测试
- `go run ./src/matchserver/cmd` 验证 matchserver 能启动
- 增加或临时运行一个 token 解析检查：用 matchserver 签出的 token 调 `roomtoken.Parse`，确认 claims 中 `player_id/room_id/server_id/match_id` 正确

## 自我审查

1. 项目边界：已修正，不再让 matchserver 依赖 `src/roomserver/protocol`，公共 token 能力放到 `pkg/roomtoken`
2. 过度设计：第一版不接数据库、Redis、复杂匹配队列，只做内存房间分配
3. 协议风险：新增独立 match proto，不改已有字段；token 字段语义保持不变
4. 错误处理：覆盖无玩家、无服务器、满房、签发失败等分支
5. 并发风险：内存状态加锁，避免并发超员
6. 性能风险：一把锁第一版可接受，后续再分片
7. 扩展性：公共 roomtoken 包让 matchserver、roomserver、后续测试工具都能共享 token 协议，不再产生服务间反向依赖

## 修正后的最终方案

最终按以下顺序实现：

1. 抽出 `pkg/roomtoken`，迁移现有 room token claims、生成和解析逻辑
2. 更新 roomserver 使用 `pkg/roomtoken.Parse` 校验入房 token
3. 新增 `pb/match/match.proto` 并生成 `gen/match`
4. 新增 matchserver 配置，默认监听 `:8090`，room token 默认 1 分钟有效
5. 新增 `src/matchserver/logic.Matcher`，用内存状态分配未满房间
6. 分配成功后调用 `roomtoken.Generate` 签发 token
7. 新增 `src/matchserver/service.MatchService` 和 `cmd/main.go`
8. 运行 `make proto`、`gofmt`、`go test ./...`、`go run ./src/matchserver/cmd` 验证

等待你确认后再开始改代码。
