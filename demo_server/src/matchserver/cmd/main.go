package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	conf "demo_server/config"
	matchpb "demo_server/gen/match"
	"demo_server/pkg/glog"
	"demo_server/src/matchserver/logic"
	"demo_server/src/matchserver/service"

	"google.golang.org/grpc"
)

// main matchserver 启动入口
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg, err := conf.Load("")
	if err != nil {
		panic(err)
	}

	logCfg := glog.DefaultConfig()
	logCfg.ServiceName = "match_server"
	if cfg.Log.Path != "" {
		logCfg.RootDir = cfg.Log.Path
	}
	if err := glog.Init(logCfg); err != nil {
		panic(err)
	}
	defer glog.Sync()

	matcher, err := logic.NewMatcher(cfg.MatchServer01)
	if err != nil {
		glog.Fatal(ctx, "create matcher failed", glog.Err(err))
	}

	grpcServer := grpc.NewServer()
	matchpb.RegisterMatchServiceServer(grpcServer, service.NewMatchService(matcher))

	listener, err := net.Listen("tcp", cfg.MatchServer01.ListenAddr)
	if err != nil {
		glog.Fatal(ctx, "listen matchserver failed", glog.String("addr", cfg.MatchServer01.ListenAddr), glog.Err(err))
	}
	glog.Info(ctx, "matchserver started", glog.String("addr", cfg.MatchServer01.ListenAddr))

	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()

	// 启动 gRPC 服务，直到进程收到退出信号
	if err := grpcServer.Serve(listener); err != nil {
		glog.Fatal(context.Background(), "serve matchserver failed", glog.Err(err))
	}
}
