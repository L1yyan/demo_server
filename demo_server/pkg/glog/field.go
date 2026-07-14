package glog

import (
	"time"

	"go.uber.org/zap"
)

// Field 日志字段
type Field = zap.Field

// String 构造字符串字段
func String(key, value string) Field {
	return zap.String(key, value)
}

// Int 构造 int 字段
func Int(key string, value int) Field {
	return zap.Int(key, value)
}

// Int64 构造 int64 字段
func Int64(key string, value int64) Field {
	return zap.Int64(key, value)
}

// Uint64 构造 uint64 字段
func Uint64(key string, value uint64) Field {
	return zap.Uint64(key, value)
}

// Bool 构造 bool 字段
func Bool(key string, value bool) Field {
	return zap.Bool(key, value)
}

// Float64 构造 float64 字段
func Float64(key string, value float64) Field {
	return zap.Float64(key, value)
}

// Duration 构造耗时字段
func Duration(key string, value time.Duration) Field {
	return zap.Duration(key, value)
}

// Any 构造任意类型字段
func Any(key string, value any) Field {
	return zap.Any(key, value)
}

// Err 构造错误字段
func Err(err error) Field {
	if err == nil {
		return zap.Skip()
	}
	return zap.Error(err)
}
