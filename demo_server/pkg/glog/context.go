package glog

import "context"

type contextKey string

const (
	traceIDKey   contextKey = "trace_id"   // trace_id 的 context key
	requestIDKey contextKey = "request_id" // request_id 的 context key
)

// WithTraceID 写入 trace_id 到 context
func WithTraceID(ctx context.Context, traceID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, traceIDKey, traceID)
}

// TraceID 从 context 获取 trace_id
func TraceID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	traceID, _ := ctx.Value(traceIDKey).(string)
	return traceID
}

// WithRequestID 写入 request_id 到 context
func WithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestIDKey, requestID)
}

// RequestID 从 context 获取 request_id
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestIDKey).(string)
	return requestID
}

// fieldsFromContext 从 context 提取日志字段
func fieldsFromContext(ctx context.Context) []Field {
	fields := make([]Field, 0, 2)

	// 自动追加 trace_id，便于跨服务链路排查
	if traceID := TraceID(ctx); traceID != "" {
		fields = append(fields, String("trace_id", traceID))
	}

	// 自动追加 request_id，便于定位单次请求日志
	if requestID := RequestID(ctx); requestID != "" {
		fields = append(fields, String("request_id", requestID))
	}
	return fields
}
