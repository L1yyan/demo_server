# pkg/glog 日志库开发设计

## 需求理解

`pkg/glog` 是项目级日志库，面向游戏服务器高性能写日志场景。业务代码通过统一 API 写日志，不直接依赖底层日志库。

当前底层选择 `go.uber.org/zap`，原因是需要严格区分 error 日志和非 error 日志，而 `zap` 可以通过不同 core 精确控制日志级别分流。

## 目标

1. 日志根目录默认为 `./bin/logs`，相对路径按项目根目录解析。
2. 按服务名分目录，例如 `logic_server` 输出到 `bin/logs/logic_server`。
3. 非 error 日志和 error 日志完全分开。
4. 文件名格式为：`log_<service_name>.log.<LEVEL>.<timestamp>.<pid>`。
5. 支持结构化字段，避免字符串拼接。
6. 支持 `trace_id` 和 `request_id` 从 context 自动输出。
7. 支持高性能写入，适合游戏服务器。
8. 保持接口简洁，便于后续扩展日志切割、采样和异步写入。

## 目录结构

```text
pkg/glog/
  DESIGN.md      // 开发设计文档
  config.go      // 日志配置
  context.go     // context 链路字段
  field.go       // 字段 helper
  logger.go      // 日志初始化和输出 API
  logger_test.go // 单元测试
```

包名使用 `glog`，业务代码导入：

```go
import "demo_server/pkg/glog"
```

## 日志目录和文件

以 `logic_server` 为例：

```text
bin/logs/logic_server/
  info/
    log_logic_server.log.INFO.20260209-180454.45961
  error/
    log_logic_server.log.ERROR.20260209-180454.45961
```

说明：

1. `INFO` 文件只写入 `debug`、`info`、`warn`。
2. `ERROR` 文件只写入 `error`、`fatal`。
3. `20260209-180454` 是进程启动时的时间戳。
4. `45961` 是进程 PID。
5. 文件名由本项目日志库生成，不依赖 glog 默认命名。

## Config 设计

```go
type Config struct {
    ServiceName string // 服务名，例如 logic_server
    RootDir     string // 日志根目录，例如 ./bin/logs
    Level       string // debug/info/warn/error
    Console     bool   // 是否同时输出到控制台
    AddCaller   bool   // 是否输出调用文件和行号
}
```

默认配置：

```go
Config{
    ServiceName: "server",
    RootDir:     "./bin/logs",
    Level:       "info",
    Console:     true,
    AddCaller:   true,
}
```

`RootDir` 为相对路径时，会从当前工作目录向上查找 `go.mod`，并按项目根目录解析。因此在 `src/logicserver/cmd` 下执行 `go run .` 时，日志仍会写入项目根目录下的 `bin/logs`。

## 对外 API

```go
func DefaultConfig() Config
func Init(cfg Config) error
func Sync() error

func Debug(ctx context.Context, msg string, fields ...Field)
func Info(ctx context.Context, msg string, fields ...Field)
func Warn(ctx context.Context, msg string, fields ...Field)
func Error(ctx context.Context, msg string, fields ...Field)
func Fatal(ctx context.Context, msg string, fields ...Field)

func With(fields ...Field) *Logger
```

模块 logger：

```go
logger := glog.With(glog.String("module", "auth"))
logger.Info(ctx, "login success", glog.String("user_id", userID))
```

## Field 设计

底层使用 `zap.Field`，对业务层提供项目自己的字段 helper。

```go
type Field = zap.Field

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

`Err(nil)` 会返回可跳过字段，避免输出无意义错误。

## Context 设计

```go
func WithTraceID(ctx context.Context, traceID string) context.Context
func TraceID(ctx context.Context) string
func WithRequestID(ctx context.Context, requestID string) context.Context
func RequestID(ctx context.Context) string
```

日志输出时自动追加：

```text
trace_id
request_id
```

空值不输出。

## 日志分流

使用 `zapcore.NewTee` 创建两个 core：

1. info core：写入 `debug`、`info`、`warn`。
2. error core：写入 `error`、`fatal`。

级别规则：

```go
infoCore:  level >= minLevel && level < error
errorCore: level >= max(minLevel, error)
```

因此 error 日志不会进入 info 文件，非 error 日志也不会进入 error 文件。

## 输出格式

第一版使用 JSON Lines，方便后续接入日志系统。

示例：

```json
{"level":"info","ts":"2026-02-09T18:04:54.123+08:00","caller":"logic/auth.go:32","msg":"login success","service":"logic_server","user_id":"10001","request_id":"req-1"}
```

## 分层使用建议

1. service 层记录请求入口、响应结果、耗时和外部错误。
2. logic 层记录关键业务判断和异常状态。
3. repo 层记录数据库错误、慢查询和数据异常。
4. 游戏 tick、物理模拟、状态同步默认不打 info 日志，只在 debug、采样或异常时记录。

## 健壮性设计

1. `Init` 校验 `ServiceName`、`RootDir`、`Level` 和相对路径解析。
2. 自动创建 `info` 和 `error` 目录。
3. 目录创建失败时返回错误。
4. 多次 `Init` 返回错误，避免运行时切换全局 logger。
5. 未初始化时使用默认开发 logger，避免业务代码 panic。
6. `Sync` 可重复调用。
7. context 为 nil 时使用 `context.Background()` 处理。

## 性能设计

1. 使用 zap 结构化字段，避免 `fmt.Sprintf` 拼接。
2. 高频路径通过日志级别过滤，不在 tick 中默认输出 info。
3. 不使用 JSON 反射序列化大对象。
4. 第一版不加异步队列，避免退出 flush、丢日志和队列满策略复杂化。
5. 后续可增加采样、缓冲写入或日志切割。

## 验证方式

实现完成后执行：

```bash
gofmt -w pkg/glog/*.go
go test ./pkg/glog
```

如果项目其他未完成文件影响 `go test ./...`，需要单独说明。
