package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	conf "demo_server/config"
	"demo_server/pkg/glog"
	"demo_server/src/roomserver/service"
)

// main roomserver 启动入口
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg, err := conf.Load("")
	if err != nil {
		panic(err)
	}

	logCfg := glog.DefaultConfig()
	logCfg.ServiceName = "room_server"
	if cfg.Log.Path != "" {
		logCfg.RootDir = cfg.Log.Path
	}
	if err := glog.Init(logCfg); err != nil {
		panic(err)
	}
	defer glog.Sync()

	server := service.NewServer(cfg.RoomServer01)
	if err := server.Start(ctx); err != nil {
		glog.Fatal(ctx, "start roomserver failed", glog.Err(err))
	}

	<-ctx.Done()
	server.Stop(context.Background())
}
