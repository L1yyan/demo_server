package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"demo_server/pkg/glog"
	roomconfig "demo_server/src/roomserver/config"
	"demo_server/src/roomserver/service"
)

// main roomserver 启动入口
func main() {
	logCfg := glog.DefaultConfig()
	logCfg.ServiceName = "room_server"
	if err := glog.Init(logCfg); err != nil {
		panic(err)
	}
	defer glog.Sync()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg := roomconfig.DefaultConfig()
	server := service.NewServer(cfg)
	if err := server.Start(ctx); err != nil {
		glog.Fatal(ctx, "start roomserver failed", glog.Err(err))
	}

	<-ctx.Done()
	server.Stop(context.Background())
}
