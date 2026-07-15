# logicserver 登录与 JWT 鉴权实现方案

## 需求理解

本次要把 `logicserver` 的登录链路补起来：客户端使用邮箱和密码登录；注册暂时不实现；登录成功后用已有 `pkg/jwt` 生成短期 `access_token` 和长期 `refresh_token` 返回给客户端；服务端需要把 token 状态存起来，并在后续请求中校验 token 是否由服务端签发且仍在服务端存储中有效。

## 影响范围

预计新增或修改：

- `pb/logic/logic.proto`：补齐注释，新增 token 校验 RPC 和请求结构，保持登录返回 `AuthResp`
- `gen/logic/*`：由 proto 生成 Go 与 gRPC 代码
- `config/config.go`：增加顶层 YAML 配置结构与加载函数，支持读取 logicserver 的 Redis、MongoDB、JWT 和监听地址配置
- `config/config.yaml`：给 `logic_server_01` 增加 `listen_addr`
- `src/logicserver/cmd/main.go`：加载配置、初始化日志/JWT/Redis/MongoDB、启动 gRPC server
- `src/logicserver/repo/*`：新增用户查询 repo 和 token 持久化 repo
- `src/logicserver/logic/*`：新增登录、token 校验、刷新短 token 的业务逻辑
- `src/logicserver/service/service.go`：实现 gRPC `LogicService` 的 `Login`、`VerifyToken`，注册和验证码先返回未实现
- `go.mod` / `go.sum`：如生成或编译需要，整理已有依赖

## 设计方案

### 1. 协议设计

在 `LogicService` 中保留已有：

- `Login(LoginReq) returns (AuthResp)`：邮箱密码登录
- `Register(RegisterReq) returns (AuthResp)`：暂时返回未实现
- `SendVerifyCode(SendVerifyCodeReq) returns (SendVerifyCodeResp)`：暂时返回未实现

新增：

- `VerifyToken(VerifyTokenReq) returns (AuthResp)`：服务端校验客户端提交的 `access_token` 和 `refresh_token`

`VerifyTokenReq` 字段：

- `access_token`：短 token
- `refresh_token`：长 token，用于短 token 过期时换新短 token

`AuthResp` 继续复用：

- `status=true` 表示认证或校验成功
- `access_token` 返回当前可用短 token；如果短 token 已过期但长 token 有效，则返回新短 token
- `refresh_token` 返回长期 token；刷新短 token 时保持原 refresh token

### 2. 数据模型

MongoDB 使用 `users` 集合查询登录用户，建议结构：

```go
type User struct {
    ID           primitive.ObjectID `bson:"_id,omitempty"`
    UserID       uint64             `bson:"user_id"`
    Email        string             `bson:"email"`
    PasswordHash string             `bson:"password_hash"`
}
```

密码只校验 `password_hash`，使用 `bcrypt.CompareHashAndPassword`。由于注册暂时不写，测试账号需要提前写入 MongoDB，密码字段需要是 bcrypt hash，不建议服务端兼容明文密码。

Redis 保存 token 会话，不直接用原始 token 做 key，先对 token 做 SHA-256：

- `logic:auth:access:<access_hash>`：保存用户 ID、邮箱、refresh hash，TTL 等于短 token 过期时间
- `logic:auth:refresh:<refresh_hash>`：保存用户 ID、邮箱，TTL 等于长 token 过期时间
- 可选 `logic:auth:user:<user_id>`：保存当前 token hash，用于后续限制单账号单会话或踢下线；本次先保存但不强制单端互踢

这样服务端校验时既校验 JWT 签名和过期时间，也校验 Redis 中是否存在该 token，实现服务端可撤销和可控的登录态。

### 3. 分层设计

严格按 `service -> logic -> repo`：

- `repo.UserRepo`：只负责 MongoDB 查询，例如 `FindByEmail(ctx, email)`
- `repo.TokenRepo`：只负责 Redis token 保存、查询、刷新短 token 状态
- `logic.AuthLogic`：负责参数校验、密码校验、JWT 生成、token 存储、token 校验和短 token 刷新流程
- `service.LogicService`：只做 gRPC 参数转换、调用 logic、组装响应，不直接操作 MongoDB/Redis/JWT 细节

### 4. 登录流程

1. service 接收 `LoginReq`
2. logic 校验邮箱和密码非空，邮箱格式基本合法
3. repo 通过邮箱查 MongoDB 用户
4. logic 用 bcrypt 校验密码 hash
5. logic 调用 `pkg/jwt.GenerateToken(userID, email)` 生成短 token 和长 token
6. logic 调用 token repo 写入 Redis，并设置对应 TTL
7. service 返回 `AuthResp{status:true, access_token, refresh_token}`

失败分支统一返回 `status=false` 和稳定错误信息：

- 参数为空或邮箱格式错误：`invalid login params`
- 用户不存在或密码错误：`invalid email or password`
- token 生成或存储失败：`login failed`

### 5. token 校验流程

1. service 接收 `VerifyTokenReq`
2. logic 先用 `pkg/jwt` 校验 access token
3. 如果 access token 未过期：
   - 校验 Redis 中存在对应 access token hash
   - 存在则返回成功
4. 如果 access token 已过期：
   - 校验 refresh token 的 JWT 签名和过期时间
   - 校验 Redis 中存在对应 refresh token hash
   - 生成新的 access token，并把新 access token 写入 Redis
   - 返回新 access token 和原 refresh token
5. 如果签名非法、refresh token 不存在、Redis 中没有记录，则返回认证失败

## 兼容性影响

- `Login` 现有字段不改，返回仍使用 `AuthResp`，兼容已有登录响应结构
- 新增 `VerifyToken` RPC 属于向后兼容的 proto 扩展，不修改已有字段编号
- 注册和验证码 RPC 保留，但实现为未实现，不会误导为可用功能
- 需要新增生成代码 `gen/logic/*`，否则 gRPC service 无法编译
- MongoDB 需要已有 `users` 集合和 bcrypt 密码 hash；如果当前库里已有明文密码数据，需要额外迁移，不建议在登录逻辑里兼容明文

## 健壮性

- 所有外部输入都会 `strings.TrimSpace` 后校验
- 用户不存在和密码错误返回同一种业务错误，避免泄露账号是否存在
- MongoDB、Redis、JWT 初始化失败会在启动阶段暴露，避免运行期空指针
- Redis 写入 token 失败时登录整体失败，不返回无法被服务端校验的 token
- access token 过期时只有 refresh token 同时通过 JWT 和 Redis 校验才会刷新
- 注册暂不实现会返回明确结果，不静默成功

## 性能考虑

- 登录路径包含一次 MongoDB 查询、一次 bcrypt 校验、两次 JWT 签名和 Redis 写入，属于低频请求，可接受
- token 校验路径包含 JWT 本地解析和一次 Redis 查询；JWT 解析为本地 CPU，Redis 是主要网络开销
- Redis key 使用 token hash，避免长 JWT 直接作为 key 带来的 key 体积问题
- 不引入无界 goroutine；gRPC server 生命周期由主进程 signal context 控制
- bcrypt cost 由已有 hash 决定，避免登录接口被高频暴力调用的问题后续应配合限流，本次先不扩展限流模块

## 验证方式

实现后计划执行：

- `gofmt -w` 格式化新增和修改的 Go 文件
- `protoc` 或 `make proto` 生成 logic proto Go 代码
- `go test ./...` 验证全仓编译和已有测试
- 如本机 MongoDB/Redis 未运行，则不做真实登录联调，只说明编译验证结果和联调前置条件

## 自我审查

1. 项目结构：当前 logicserver 为空壳，已有 `pkg/jwt`、`pkg/mongodb`、`pkg/redis` 可复用，方案按项目要求拆 repo、logic、service 三层
2. 过度设计：没有引入新框架，没有写注册、限流、单端互踢和完整用户管理，只实现登录和鉴权所需最小闭环
3. 协议风险：新增 RPC 和 message，不改已有字段编号；proto 会补简短注释
4. 错误处理：需要避免用户不存在和密码错误返回不同信息；Redis 存储失败不能继续返回 token
5. 空值边界：邮箱、密码、token 都需要空值校验；JWT 未初始化、secret 为空已有工具层错误，需要 service 转成业务响应
6. 性能风险：bcrypt 和 Redis 是主要开销；本次是登录/鉴权低频路径，暂不做批处理
7. 扩展性：token repo 保存 user key，为后续 logout、踢下线、单账号单会话预留，但本次不强制实现这些业务

## 修正后的最终方案

最终按最小可用闭环实现：

1. 补齐 `logic.proto` 注释，并新增 `VerifyToken` RPC
2. 新增 `repo.UserRepo` 查询 MongoDB `users` 集合，密码只支持 bcrypt hash
3. 新增 `repo.TokenRepo` 使用 Redis 保存 access/refresh token hash 和 TTL
4. 新增 `logic.AuthLogic` 完成邮箱密码登录、JWT 生成、Redis 存储、token 校验和短 token 刷新
5. 在 `service.LogicService` 中实现登录和 token 校验，注册/验证码返回未实现
6. 改造 `logicserver/cmd/main.go` 启动真实 gRPC server，并初始化配置、日志、JWT、MongoDB、Redis
7. 生成 `gen/logic` 代码并运行格式化和编译测试

等待用户确认后再修改业务代码。
