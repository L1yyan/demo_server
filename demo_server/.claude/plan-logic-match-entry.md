# logicserver 转发匹配请求实现方案

## 需求理解

用户点击匹配时，请求入口应该从 logicserver 发起，而不是客户端直接调用 matchserver。完整链路应为：

```text
客户端
  -> logicserver MatchRoom，携带登录 access_token / refresh_token 和匹配模式
  -> logicserver 校验登录态
  -> logicserver 调 matchserver AllocateRoom
  -> matchserver 返回 roomserver 地址、房间 ID、match ID、room token
  -> logicserver 把这些信息返回给客户端
  -> 客户端拿 server_addr + room_token 直连 roomserver
```

这样 matchserver 不直接暴露给客户端，客户端只依赖 logicserver 和 roomserver。

## 影响范围

预计修改：

- `pb/logic/logic.proto`：新增 `MatchRoom` RPC、请求和响应 message
- `gen/logic/*`：执行 `make proto` 后本地生成，按当前要求不提交 `gen`
- `config/config.go`：给 `LogicServerConfig` 增加 `MatchServerAddr`
- `config/config.yaml`：给 `logic_server_01` 增加 `match_server_addr: "127.0.0.1:8090"`
- `src/logicserver/cmd/main.go`：启动时连接 matchserver，注入 logicserver service
- `src/logicserver/logic/auth.go`：让 token 校验结果能返回 `user_id/email`，供匹配使用
- `src/logicserver/logic/match.go`：新增匹配业务逻辑，负责校验 token 后调用 matchserver
- `src/logicserver/service/service.go`：新增 `MatchRoom` gRPC 方法，只做参数转换和响应组装

不修改：

- `pb/match/match.proto`：当前 `AllocateRoomResp` 已包含 `server_addr`、`room_token` 等信息，够用
- `src/matchserver/*`：matchserver 当前职责已经正确
- `src/roomserver/*`：roomserver 仍只校验 room token

## 协议设计

在 `LogicService` 新增：

```proto
// MatchRoom 请求匹配房间
rpc MatchRoom(MatchRoomReq) returns (MatchRoomResp);
```

新增请求：

```proto
// MatchRoomReq 匹配请求
message MatchRoomReq {
  string access_token = 1; // 短token
  string refresh_token = 2; // 长期token
  string mode = 3; // 匹配模式
}
```

新增响应：

```proto
// MatchRoomResp 匹配响应
message MatchRoomResp {
  bool status = 1; // 状态
  string content = 2; // 返回信息
  string access_token = 3; // 当前可用短token
  string refresh_token = 4; // 长期token
  string room_id = 5; // 房间ID
  string server_id = 6; // roomserver ID
  string server_addr = 7; // roomserver连接地址
  string match_id = 8; // 匹配ID
  string room_token = 9; // 入房令牌
  int64 expire_at = 10; // 过期时间戳，毫秒
}
```

为什么响应里保留 `access_token/refresh_token`：

- 如果客户端发来的 access token 已过期但 refresh token 有效，logicserver 会刷新 access token
- 客户端收到新的 `access_token` 后可以更新本地登录态

## 核心设计

### 1. AuthLogic 返回用户身份

当前 `AuthLogic.VerifyToken` 只返回 token 字符串，不返回用户 ID。匹配需要用登录用户作为 `player_id`，所以需要把结果结构扩展为：

```go
type AuthResult struct {
    UserID       uint64
    Email        string
    AccessToken  string
    RefreshToken string
}
```

可以直接扩展现有 `LoginResult`，避免大范围重命名；但更清晰的做法是新增 `AuthResult` 并逐步替代。为减少改动，建议第一版扩展 `LoginResult`：

```go
type LoginResult struct {
    UserID       uint64
    Email        string
    AccessToken  string
    RefreshToken string
}
```

登录成功时填入 userID/email；token 校验成功时从 JWT claims 或 Redis session 填入 userID/email。

### 2. LogicMatch 业务层

新增 `src/logicserver/logic/match.go`：

```go
type MatchLogic struct {
    auth   *AuthLogic
    client matchpb.MatchServiceClient
}
```

流程：

1. 校验 `access_token` 非空
2. 调 `auth.VerifyToken` 校验登录态，必要时刷新 access token
3. 使用 `authResult.UserID` 作为 `AllocateRoomReq.PlayerId`
4. 调用 matchserver `AllocateRoom`
5. 如果 matchserver 返回失败，向 service 返回失败原因
6. 成功时返回 roomserver 地址、room token、match ID，以及当前可用登录 token

### 3. service 层职责

`LogicService.MatchRoom` 只做：

- nil req 校验
- 调 `matchLogic.MatchRoom`
- 组装 `MatchRoomResp`
- 记录内部错误日志

service 不直接调用 matchserver，也不直接解析 JWT。

### 4. main 初始化

`logicserver/cmd/main.go` 启动时：

1. 读取 `logic_server_01.match_server_addr`
2. `grpc.Dial` 或新版 `grpc.NewClient` 建立 matchserver client
3. 创建 `MatchLogic`
4. `service.NewLogicService(authLogic, matchLogic)` 注入
5. 进程退出时关闭 gRPC client 连接

## 兼容性影响

- 新增 `MatchRoom` RPC，不改已有 `Login`、`VerifyToken` 字段编号，兼容已有客户端
- `AuthResp` 不变
- 新增 `MatchRoomResp`，不复用 `AuthResp`，避免把房间字段塞进认证响应
- logicserver 配置新增 `match_server_addr`，缺省时可以使用 `127.0.0.1:8090`
- 需要本地重新 `make proto`，但按你的要求 `gen/` 不纳入 git 跟踪

## 健壮性

- access token 缺失：返回 `status=false, content="unauthorized"`
- 登录态无效或 Redis 中不存在 token：返回 `unauthorized`
- access token 过期但 refresh token 有效：刷新 access token 后继续请求 matchserver
- matchserver 不可用或 RPC 失败：返回 `match server unavailable`，并记录错误日志
- matchserver 返回业务失败：透传 `content`，例如 `roomserver full`
- matchserver 返回成功但缺少 `room_token/server_addr`：返回 `match failed`

## 性能考虑

- 点击匹配属于低频请求，一次 JWT/Redis 校验加一次 gRPC 调用可以接受
- logicserver 到 matchserver 使用长连接 gRPC client，避免每次匹配重新建连
- 当前没有批量匹配队列，第一版是立即分配；后续如果要等待多人凑局，再在 matchserver 内部升级匹配池

## 验证方式

实现后执行：

```bash
make proto
gofmt -w ...
go test ./...
go run ./src/matchserver/cmd
go run ./src/logicserver/cmd
```

如果本地有 Redis/MongoDB 和测试用户，再做完整联调：

1. 调 logicserver `Login` 获取登录 token
2. 调 logicserver `MatchRoom`
3. 确认响应包含 `server_addr` 和 `room_token`
4. 用 `room_token` 直连 roomserver 入房

## 自我审查

1. 是否遗漏项目结构：当前 logicserver 已有 auth logic/service，matchserver 已有 AllocateRoom，方案只补中间转发链路
2. 是否过度设计：不做匹配队列、不做取消匹配、不做多人组队，只做用户点击匹配后的立即分配
3. 协议风险：新增独立 `MatchRoomResp`，不污染 `AuthResp`；字段编号从 1 顺序新增
4. 错误处理：覆盖 token 无效、刷新失败、matchserver 不可用、matchserver 业务失败和响应异常
5. 性能风险：gRPC client 长连接复用，低频请求无明显瓶颈
6. 扩展性：后续可以让 `MatchRoomReq` 增加地图、模式、队伍 ID；不影响 roomserver 入房 token 结构
7. 分层边界：service 不直接操作 JWT/Redis/match RPC 细节，核心流程放在 logic 层

## 修正后的最终方案

最终按以下顺序实现：

1. 修改 `pb/logic/logic.proto`，新增 `MatchRoom` RPC 和请求/响应
2. `make proto` 生成本地 `gen/logic` 代码
3. 扩展 `AuthLogic` token 校验结果，返回 `user_id/email`
4. 新增 `logic.MatchLogic`，负责校验登录态并调用 matchserver
5. 修改 `service.LogicService`，新增 `MatchRoom` 方法
6. 修改 logicserver 配置和启动入口，建立 matchserver gRPC client
7. 运行格式化、测试和启动验证

等待确认后再修改业务代码。
