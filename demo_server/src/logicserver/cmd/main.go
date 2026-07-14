package main

import (
	"context"
	"demo_server/pkg/glog"
)

func main() {
	cfg := glog.DefaultConfig()
	cfg.ServiceName = "logic_server"

	// 初始化日志系统
	if err := glog.Init(cfg); err != nil {
		panic(err)
	}
	defer glog.Sync()

	ctx := context.Background()
	ctx = glog.WithTraceID(ctx, "trace-001")
	ctx = glog.WithRequestID(ctx, "request-001")

	glog.Info(ctx, "server started", glog.String("addr", ":8080"))
	glog.Warn(ctx, "server warning", glog.String("reason", "demo warning"))

}
