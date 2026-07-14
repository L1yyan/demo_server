# JWT 工具化改造方案

## 需求理解

当前 `pkg/jwt/jwt.go` 把 JWT 生成、解析、刷新和 HTTP/Gin 中间件响应逻辑写在一起。用户希望 `pkg/jwt` 只作为工具包，不直接处理 HTTP 接口访问和 HTTP 返回，具体 HTTP 返回放到 service 层处理，同时保留原本的功能：访问令牌生成、刷新令牌生成、令牌解析、过期判断、刷新访问令牌。

## 影响范围

预计修改：

- `pkg/jwt/jwt.go`：移除 Gin/HTTP 中间件和响应写入逻辑，保留并整理为纯 JWT 工具能力

可能不修改：

- `config/config.go`：已有 `JwtConfig` 可继续作为初始化入参
- `src/logicserver/service/*`：当前 service 里没有实际 HTTP 逻辑；本次先不新增 HTTP 接口，避免扩大需求范围
- `go.mod`：当前没有 gin 依赖，改造后也不需要新增 gin 依赖

## 设计方案

1. 保留包内单例能力
   - 保留 `InitJwtConf(conf *config.JwtConfig)`，继续负责把项目配置转换为 JWT 工具配置
   - 保留 `NewJWTMiddleware()`，但它只返回 JWT 工具实例。为了兼容旧调用名，暂不删除该函数；后续可以另加更准确的 `NewJWTTool()` 或 `Instance()`

2. 移除 HTTP/Gin 耦合
   - 删除或改造 `Authenticate() gin.HandlerFunc`
   - 不在 `pkg/jwt` 内读取 HTTP header
   - 不在 `pkg/jwt` 内调用 `AbortWithStatusJSON`、设置响应头、写 HTTP 状态码
   - 不在 `pkg/jwt` 内依赖 `gin.H`、`gin.Context`

3. 提供纯工具方法承接原本中间件能力
   - 保留 `GenerateToken(userID uint64, email string) (string, string, error)`
   - 保留 `RefreshToken(refreshTokenStr string) (string, error)`
   - 保留 `ParseToken(tokenStr string) (*JWTClaims, bool, error)`，第二个返回值继续表示是否过期
   - 新增一个协议无关的方法，例如：
     - `VerifyAccessToken(accessToken string) (*JWTClaims, bool, error)`：只校验访问令牌，返回 claims、是否过期、错误
     - `RefreshAccessToken(refreshToken string) (string, *JWTClaims, error)`：根据刷新令牌生成新访问令牌，并返回新令牌对应 claims
   - service 层未来拿到 HTTP header 后，只调用这些方法，再自行决定 HTTP 状态码、响应体和响应头

4. 错误处理
   - 增加配置未初始化检查，避免 `instance.config` 为 nil 时 panic
   - 对空 token、空 secret 做显式错误返回
   - 保留原有错误语义：过期返回 `isExpired=true`，签名非法和解析失败返回明确错误

5. 命名与注释
   - 补齐导出类型和导出函数中文注释
   - 内部关键流程添加简短中文注释
   - 包名当前是 `Jwt`，不符合 Go 常规小写包名。为降低影响，本次不强制改包名；但建议后续统一为 `jwt`，这会影响导入方。

## 兼容性

- `GenerateToken`、`RefreshToken`、`ParseToken`、`InitJwtConf`、`NewJWTMiddleware` 会保留，降低已有调用方改动成本
- `Authenticate()` 如果删除，会影响未来已有 HTTP 中间件调用方。但当前仓库没有任何调用，且该方法现在无法编译，因为缺少 `gin` 和 `log` 依赖/import
- 本次不会修改 proto、数据库结构或已有对外接口

## 健壮性

- 初始化前调用会返回明确错误，而不是空指针崩溃
- 空访问令牌、空刷新令牌返回明确错误
- 刷新令牌过期、签名错误、解析失败都会由 `ParseToken` 统一处理
- `SkipPaths`/`SkipPathsMap` 是 HTTP 路由跳过逻辑，工具层不再使用；可以保留在配置结构里以兼容配置，但不参与工具方法

## 性能考虑

- JWT 生成和解析为本地 CPU 计算，无网络和数据库开销
- `SkipPathsMap` 初始化仍是一次性 map 构建，无高频额外开销
- 不引入锁竞争；读取配置仍使用单例指针。若未来需要运行时热更新，可再引入 `atomic.Value` 或读写锁

## 验证方式

- `gofmt -w pkg/jwt/jwt.go`
- `go test ./pkg/jwt` 验证 JWT 包可编译
- `go test ./...` 做全仓检查；但当前仓库还存在其他无关编译问题，例如 `pkg/redis/redis.go` 的 `conf` 未定义风险、空文件问题需要单独处理。如果全仓失败，会如实说明失败点

## 自我审查

1. 是否遗漏项目结构：已确认当前 service 层没有 HTTP 实现，仓库中也没有调用 `Authenticate()` 的地方
2. 是否过度设计：不新增 service HTTP 中间件，不新增 gin 依赖，只把 JWT 包改成工具层，范围可控
3. 是否协议兼容风险：不改 proto，不改已有 token 字段语义
4. 是否错误处理不足：需要补上配置未初始化、空 token、空 secret 的错误返回
5. 是否性能风险：本地签名和解析开销很小，无新增高频 IO
6. 是否扩展困难：工具方法返回 claims/token/error，service 层可自由映射 HTTP、gRPC 或其他协议响应
7. 是否有更简单做法：最简单是直接删除 `Authenticate()` 并补 import，但这会丢失原来“过期后用刷新令牌换新访问令牌”的流程封装。因此新增协议无关的刷新辅助方法更贴合“保留原本功能”

## 修正后的最终方案

最终按最小范围改造 `pkg/jwt/jwt.go`：

1. 移除 Gin/HTTP 响应依赖和 `Authenticate()` 中间件方法
2. 保留原有 token 生成、解析、刷新方法签名
3. 新增协议无关的访问令牌校验和刷新辅助方法，供 service 层处理 HTTP header/response 时调用
4. 增加初始化和空值校验，避免 panic
5. 运行 `gofmt` 和 JWT 包编译验证，并汇报全仓验证是否被其他既有问题阻塞

等待用户确认后再修改代码。
