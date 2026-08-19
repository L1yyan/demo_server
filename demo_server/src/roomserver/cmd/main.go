package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	conf "demo_server/config"
	logicpb "demo_server/gen/logic"
	"demo_server/pkg/glog"
	"demo_server/src/roomserver/service"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

	logicConn, err := grpc.NewClient(cfg.RoomServer01.LogicServerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		glog.Fatal(ctx, "connect logicserver failed", glog.String("addr", cfg.RoomServer01.LogicServerAddr), glog.Err(err))
	}
	defer logicConn.Close()
	logicClient := logicpb.NewLogicServiceClient(logicConn)
	// matchLogic, err := logic.NewMatchLogic(authLogic, matchpb.NewMatchServiceClient(matchConn))
	// if err != nil {
	// 	glog.Fatal(ctx, "create match logic failed", glog.Err(err))
	// }
	
	server := service.NewServer(cfg.RoomServer01)
	if err := server.Start(ctx, logicClient); err != nil {
		glog.Fatal(ctx, "start roomserver failed", glog.Err(err))
	}

	<-ctx.Done()
	server.Stop(context.Background())
}
