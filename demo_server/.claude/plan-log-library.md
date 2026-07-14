# pkg/log glog 日志库设计方案

## 需求理解

在 `/home/liyan1/code/demo_server/pkg/log` 下设计一套基于 `github.com/golang/glog` 的项目级日志库。日志库面向游戏服务器，重点关注高性能写入、服务维度隔离、错误日志分离、统一调用方式和后续可维护性。

日志输出要求：

- 日志根目录为 `/home/liyan1/code/demo_server/bin/logs`。
- 按服务名分目录，例如 logic 服务输出到 `/home/liyan1/code/demo_server/bin/logs/logic_server`。
- 使用 glog 按级别生成日志文件，普通日志主要查看 `INFO` 文件，错误日志主要查看 `ERROR` 文件。
- 文件名采用 glog 默认规范，例如：`logic_server.<host>.<user>.log.INFO.20260209-180454.45961`。
- 文件名最后一段 `45961` 是进程 PID。

## 影响范围

预计新增文件：

- `pkg/log/config.go`：日志配置、默认配置、配置校验。
- `pkg/log/field.go`：日志字段定义和字段格式化。
- `pkg/log/context.go`：`trace_id`、`request_id` 的 context 注入与提取。
- `pkg/log/logger.go`：日志初始化、全局方法、模块 logger。
- `pkg/log/logger_test.go`：基础单元测试。

预计修改文件：

- `go.mod`：新增 `github.com/golang/glog` 依赖。
- `go.sum`：由 `go mod tidy` 生成或更新。

暂不修改业务层代码，避免扩大影响面。

## 核心设计

### 包名

目录使用 `pkg/log`，包名使用 `logx`，避免和 Go 标准库 `log` 混淆。

业务代码导入方式：

```go
import logx "demo_server/pkg/log"
```

### Config 设计

```go
type Config struct {
    ServiceName     string // 服务名，例如 logic_server
    RootDir         string // 日志根目录，例如 ./bin/logs
    LogToStderr     bool   // 是否只输出到 stderr
    AlsoToStderr    bool   // 是否同时输出到 stderr
    StderrThreshold string // 输出到 stderr 的最低级别
    Verbosity       int    // glog V 日志等级
    VModule         string // glog vmodule 配置
    LogBufSecs      int    // 日志缓冲秒数
}
```

默认配置：

```go
Config{
    ServiceName:     "server",
    RootDir:         "./bin/logs",
    LogToStderr:     false,
    AlsoToStderr:    true,
    StderrThreshold: "error",
    Verbosity:       0,
    VModule:         "",
    LogBufSecs:      0,
}
```

logic 服务使用时设置：

```go
cfg := logx.DefaultConfig()
cfg.ServiceName = "logic_server"
```

最终日志目录：

```text
bin/logs/logic_server/
```

### glog 文件规则

`glog` 会根据 `-log_dir` 和程序名自动创建日志文件。文件名大致为：

```text
<program>.<host>.<user>.log.<LEVEL>.<timestamp>.<pid>
```

示例：

```text
logic_server.hostname.username.log.INFO.20260209-180454.45961
logic_server.hostname.username.log.ERROR.20260209-180454.45961
```

说明：

- `INFO`、`WARNING`、`ERROR`、`FATAL` 是日志级别。
- `20260209-180454` 是时间戳。
- `45961` 是进程 PID。
- glog 文件名不强行自定义，避免破坏 glog 原生能力。

### 初始化流程

```go
func Init(cfg Config) error
func Flush()
```

`Init` 负责：

1. 校验 `ServiceName`、`RootDir`、`StderrThreshold` 等配置。
2. 创建日志目录：`RootDir/ServiceName`。
3. 设置 glog flags：
   - `log_dir`
   - `logtostderr`
   - `alsologtostderr`
   - `stderrthreshold`
   - `v`
   - `vmodule`
   - `logbufsecs`
4. 标记日志系统已初始化。

`Flush` 负责：

```go
glog.Flush()
```

main 函数中推荐：

```go
// main 主函数
func main() {
    // 初始化日志系统
    cfg := logx.DefaultConfig()
    cfg.ServiceName = "logic_server"
    if err := logx.Init(cfg); err != nil {
        panic(err)
    }
    defer logx.Flush()
}
```

### 对外日志 API

全局方法：

```go
func Info(ctx context.Context, msg string, fields ...Field)
func Warn(ctx context.Context, msg string, fields ...Field)
func Error(ctx context.Context, msg string, fields ...Field)
func Fatal(ctx context.Context, msg string, fields ...Field)

func Debug(ctx context.Context, level int, msg string, fields ...Field)
func V(level int) bool
```

使用示例：

```go
logx.Info(ctx, "login success",
    logx.String("user_id", userID),
    logx.String("server", "logic"),
)

if logx.V(2) {
    logx.Debug(ctx, 2, "sync snapshot",
        logx.String("room_id", roomID),
        logx.Int("body_count", bodyCount),
    )
}
```

### 模块 Logger

支持带固定字段的 logger：

```go
type Logger struct {
    fields []Field
}

func With(fields ...Field) *Logger

func (l *Logger) Info(ctx context.Context, msg string, fields ...Field)
func (l *Logger) Warn(ctx context.Context, msg string, fields ...Field)
func (l *Logger) Error(ctx context.Context, msg string, fields ...Field)
func (l *Logger) Debug(ctx context.Context, level int, msg string, fields ...Field)
```

使用示例：

```go
authLog := logx.With(logx.String("module", "auth"))
authLog.Info(ctx, "login success", logx.String("user_id", userID))
```

输出文本类似：

```text
login success module=auth user_id=10001
```

### Field 设计

`glog` 本身不是结构化日志库，所以 `Field` 负责统一格式化成 `key=value` 文本。

```go
type Field struct {
    Key   string
    Value any
}
```

提供 helper：

```go
func String(key, value string) Field
func Int(key string, value int) Field
func Int64(key string, value int64) Field
func Uint64(key string, value uint64) Field
func Bool(key string, value bool) Field
func Float64(key string, value float64) Field
func Duration(key string, value time.Duration) Field
func Any(key string, value any) Field
func Err(err error) Field
```

格式规则：

- 普通字段：`key=value`
- 字符串包含空格时加引号：`content="invalid password"`
- 错误字段统一为：`error=xxx`
- 空 key 字段忽略。
- `Err(nil)` 忽略，避免输出无意义的 `error=<nil>`。

### Context 字段

支持自动追加链路字段：

```go
func WithTraceID(ctx context.Context, traceID string) context.Context
func TraceID(ctx context.Context) string
func WithRequestID(ctx context.Context, requestID string) context.Context
func RequestID(ctx context.Context) string
```

日志输出时自动追加：

```text
trace_id=xxx request_id=xxx
```

如果为空则不输出。

## 分层适配

`pkg/log` 属于项目公共基础设施，不属于 repo、logic、service 任一业务层。

使用约束：

- service 层记录请求入口、响应结果、耗时和外部错误。
- logic 层记录关键业务判断和异常状态。
- repo 层记录数据库错误、慢查询和数据异常。
- 游戏 tick、物理模拟、状态同步默认不打 info 日志，只在 verbose debug、采样或异常时记录。

依赖方向不受影响，业务层只依赖 `pkg/log`，`pkg/log` 不依赖业务层。

## 健壮性设计

1. `Init` 对空 `ServiceName`、空 `RootDir`、非法 `StderrThreshold` 返回错误。
2. 日志目录不存在时自动创建。
3. 日志目录创建失败时返回错误，不静默降级。
4. 多次调用 `Init` 返回错误，避免运行中重复修改 glog 全局 flags。
5. 未初始化时仍允许输出到 glog 默认输出，避免业务代码崩溃，但推荐启动阶段显式初始化。
6. `Flush` 可重复调用。
7. context 为 nil 时使用 `context.Background()` 处理。

## 性能设计

1. 高频路径使用 `V(level)` 先判断，再构造字段。
2. 不在日志 API 内做复杂 JSON 编码，只拼接简单 `key=value` 文本。
3. 不提前 `fmt.Sprintf`，优先用字段 helper。
4. 不默认记录每帧 tick、每个玩家移动等高频 info 日志。
5. 依赖 glog 自身缓冲机制，不额外加异步队列。
6. 第一版不做采样日志，后续如果高频重复日志较多，再增加 `EveryN` 或 `Sample` 能力。

## 兼容性

- 新增 `pkg/log`，不影响现有 proto、Makefile 和生成代码。
- 新增第三方依赖 `github.com/golang/glog`。
- glog 使用全局 flags，项目启动流程需要明确：先初始化配置，再调用 `logx.Init`，最后在退出前 `logx.Flush`。

## 验证方式

实现后执行：

```bash
gofmt -w pkg/log/*.go
go test ./pkg/log
```

如果新增依赖：

```bash
go mod tidy
```

可以增加一个临时小程序或单元测试验证日志目录创建，但不在测试中强依赖 glog 实际落盘文件名，因为文件名包含 host、user、时间戳和 PID。

## 自我审查

### 发现的问题

1. glog 不是结构化日志库，强行做完整结构化能力会过度设计。
2. glog 文件名不适合完全自定义，强行改成 `log_logic.log.INFO...` 会破坏 glog 原生机制。
3. glog 依赖全局 flags，多次初始化或运行时动态切换服务目录不合适。
4. 高频 debug 如果直接调用并构造字段，仍会产生开销。
5. ERROR 日志是否完全不出现在 INFO 文件中由 glog 行为决定，不能承诺严格互斥。

### 修正后的最终方案

1. 接受 glog 默认文件命名规则，只严格控制日志目录：`bin/logs/<service_name>`。
2. 用 `pkg/log` 作为项目门面，业务代码不直接依赖 `glog`。
3. 第一版只做简单 `key=value` 字段格式化，不做 JSON 和复杂结构化输出。
4. 提供 `V(level)`，高频路径先判断再记录 debug 日志。
5. `Init` 只允许启动阶段调用一次，避免 glog 全局 flags 被运行时反复修改。
6. 错误日志主要查看 `ERROR` 文件，普通日志主要查看 `INFO` 文件，但不承诺级别文件完全互斥。

请确认是否按此方案开始实现。
