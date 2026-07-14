package glog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	globalMu          sync.RWMutex             // 全局 logger 读写锁
	globalLogger      = newDevelopmentLogger() // 全局 logger 实例
	globalInitialized bool                     // 全局 logger 是否已初始化
)

// Logger 项目日志对象
type Logger struct {
	logger *zap.Logger // zap logger 实例
	fields []Field     // 固定日志字段
}

type levelEnablerFunc func(zapcore.Level) bool

// Enabled 判断日志级别是否启用
func (f levelEnablerFunc) Enabled(level zapcore.Level) bool {
	return f(level)
}

// Init 初始化全局日志
func Init(cfg Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}

	cfg, err := cfg.normalizeRootDir()
	if err != nil {
		return err
	}

	globalMu.Lock()
	defer globalMu.Unlock()
	if globalInitialized {
		return errors.New("logger already initialized")
	}

	// 创建服务日志目录，保证 info 和 error 可以完全分开落盘
	if err := os.MkdirAll(cfg.infoLogDir(), 0o755); err != nil {
		return fmt.Errorf("create info log dir: %w", err)
	}
	if err := os.MkdirAll(cfg.errorLogDir(), 0o755); err != nil {
		return fmt.Errorf("create error log dir: %w", err)
	}

	logger, err := buildLogger(cfg)
	if err != nil {
		return err
	}

	globalLogger = logger
	globalInitialized = true
	return nil
}

// Sync 刷新日志缓冲
func Sync() error {
	globalMu.RLock()
	logger := globalLogger
	globalMu.RUnlock()
	return logger.Sync()
}

// With 创建带固定字段的日志对象
func With(fields ...Field) *Logger {
	globalMu.RLock()
	logger := globalLogger
	globalMu.RUnlock()
	return logger.With(fields...)
}

// Debug 输出 debug 日志
func Debug(ctx context.Context, msg string, fields ...Field) {
	globalMu.RLock()
	logger := globalLogger
	globalMu.RUnlock()
	logger.Debug(ctx, msg, fields...)
}

// Info 输出 info 日志
func Info(ctx context.Context, msg string, fields ...Field) {
	globalMu.RLock()
	logger := globalLogger
	globalMu.RUnlock()
	logger.Info(ctx, msg, fields...)
}

// Warn 输出 warn 日志
func Warn(ctx context.Context, msg string, fields ...Field) {
	globalMu.RLock()
	logger := globalLogger
	globalMu.RUnlock()
	logger.Warn(ctx, msg, fields...)
}

// Error 输出 error 日志
func Error(ctx context.Context, msg string, fields ...Field) {
	globalMu.RLock()
	logger := globalLogger
	globalMu.RUnlock()
	logger.Error(ctx, msg, fields...)
}

// Fatal 输出 fatal 日志并退出进程
func Fatal(ctx context.Context, msg string, fields ...Field) {
	globalMu.RLock()
	logger := globalLogger
	globalMu.RUnlock()
	logger.Fatal(ctx, msg, fields...)
}

// With 创建带固定字段的日志对象
func (l *Logger) With(fields ...Field) *Logger {
	baseFields := make([]Field, 0, len(l.fields)+len(fields))
	baseFields = append(baseFields, l.fields...)
	baseFields = append(baseFields, fields...)
	return &Logger{logger: l.logger, fields: baseFields}
}

// Debug 输出 debug 日志
func (l *Logger) Debug(ctx context.Context, msg string, fields ...Field) {
	l.logger.Debug(msg, l.mergeFields(ctx, fields...)...)
}

// Info 输出 info 日志
func (l *Logger) Info(ctx context.Context, msg string, fields ...Field) {
	l.logger.Info(msg, l.mergeFields(ctx, fields...)...)
}

// Warn 输出 warn 日志
func (l *Logger) Warn(ctx context.Context, msg string, fields ...Field) {
	l.logger.Warn(msg, l.mergeFields(ctx, fields...)...)
}

// Error 输出 error 日志
func (l *Logger) Error(ctx context.Context, msg string, fields ...Field) {
	l.logger.Error(msg, l.mergeFields(ctx, fields...)...)
}

// Fatal 输出 fatal 日志并退出进程
func (l *Logger) Fatal(ctx context.Context, msg string, fields ...Field) {
	l.logger.Fatal(msg, l.mergeFields(ctx, fields...)...)
}

// Sync 刷新当前日志对象缓冲
func (l *Logger) Sync() error {
	if l == nil || l.logger == nil {
		return nil
	}
	return l.logger.Sync()
}

// mergeFields 合并固定字段、context 字段和本次日志字段
func (l *Logger) mergeFields(ctx context.Context, fields ...Field) []Field {
	ctxFields := fieldsFromContext(ctx)
	merged := make([]Field, 0, len(l.fields)+len(ctxFields)+len(fields))
	merged = append(merged, l.fields...)
	merged = append(merged, ctxFields...)
	merged = append(merged, fields...)
	return merged
}

// buildLogger 构建 zap logger
func buildLogger(cfg Config) (*Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderCfg.EncodeDuration = zapcore.StringDurationEncoder
	encoderCfg.TimeKey = "ts"
	encoderCfg.LevelKey = "level"
	encoderCfg.MessageKey = "msg"
	encoderCfg.CallerKey = "caller"

	infoFile, err := openLogFile(cfg.infoLogDir(), cfg.ServiceName, "INFO")
	if err != nil {
		return nil, err
	}
	errorFile, err := openLogFile(cfg.errorLogDir(), cfg.ServiceName, "ERROR")
	if err != nil {
		_ = infoFile.Close()
		return nil, err
	}

	infoSyncer := zapcore.AddSync(infoFile)
	errorSyncer := zapcore.AddSync(errorFile)
	if cfg.Console {
		infoSyncer = zapcore.NewMultiWriteSyncer(infoSyncer, zapcore.AddSync(os.Stdout))
		errorSyncer = zapcore.NewMultiWriteSyncer(errorSyncer, zapcore.AddSync(os.Stderr))
	}

	infoCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		infoSyncer,
		levelEnablerFunc(func(logLevel zapcore.Level) bool {
			return logLevel >= level && logLevel < zapcore.ErrorLevel
		}),
	)
	errorCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		errorSyncer,
		levelEnablerFunc(func(logLevel zapcore.Level) bool {
			return logLevel >= level && logLevel >= zapcore.ErrorLevel
		}),
	)

	options := []zap.Option{zap.Fields(String("service", cfg.ServiceName))}
	if cfg.AddCaller {
		options = append(options, zap.AddCaller(), zap.AddCallerSkip(1))
	}

	return &Logger{logger: zap.New(zapcore.NewTee(infoCore, errorCore), options...)}, nil
}

// newDevelopmentLogger 创建未初始化时使用的开发 logger
func newDevelopmentLogger() *Logger {
	logger, err := zap.NewDevelopment(zap.AddCaller(), zap.AddCallerSkip(1))
	if err != nil {
		return &Logger{logger: zap.NewNop()}
	}
	return &Logger{logger: logger}
}

// openLogFile 创建日志文件
func openLogFile(dir, serviceName, level string) (*os.File, error) {
	fileName := fmt.Sprintf("log_%s.log.%s.%s.%d", serviceName, level, time.Now().Format("20060102-150405"), os.Getpid())
	filePath := filepath.Join(dir, fileName)
	return os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

// parseLevel 解析日志级别
func parseLevel(level string) (zapcore.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return zapcore.DebugLevel, nil
	case "info", "":
		return zapcore.InfoLevel, nil
	case "warn", "warning":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	default:
		return zapcore.InfoLevel, errors.New("unsupported log level")
	}
}
