package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	conf "demo_server/config"
	logicpb "demo_server/gen/logic"
	"demo_server/pkg/glog"
	jwttool "demo_server/pkg/jwt"
	"demo_server/pkg/mongodb"
	redisx "demo_server/pkg/redis"
	"demo_server/src/logicserver/logic"
	"demo_server/src/logicserver/repo"
	"demo_server/src/logicserver/service"

	"google.golang.org/grpc"
)

// main logicserver 启动入口
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg, err := conf.Load("")
	if err != nil {
		panic(err)
	}

	logCfg := glog.DefaultConfig()
	logCfg.ServiceName = "logic_server"
	if cfg.Log.Path != "" {
		logCfg.RootDir = cfg.Log.Path
	}
	if err := glog.Init(logCfg); err != nil {
		panic(err)
	}
	defer glog.Sync()

	// 初始化基础组件配置，保证 repo 和 JWT 工具能拿到运行参数
	jwttool.InitJwtConf(&cfg.JWT)
	mongodb.InitMongoConf(&cfg.LogicServer01.MongoDB)
	redisx.InitRedisConf(&cfg.LogicServer01.Redis)

	userRepo, err := repo.NewUserRepo(repo.MongoClient(), &cfg.LogicServer01.MongoDB)
	if err != nil {
		glog.Fatal(ctx, "create user repo failed", glog.Err(err))
	}
	redisClient := repo.RedisClient()
	tokenRepo, err := repo.NewTokenRepo(redisClient.Rdb)
	if err != nil {
		glog.Fatal(ctx, "create token repo failed", glog.Err(err))
	}
	authLogic, err := logic.NewAuthLogic(userRepo, tokenRepo, jwttool.Instance(), cfg.JWT.TokenExpire, cfg.JWT.RefreshExpire)
	if err != nil {
		glog.Fatal(ctx, "create auth logic failed", glog.Err(err))
	}

	grpcServer := grpc.NewServer()
	logicpb.RegisterLogicServiceServer(grpcServer, service.NewLogicService(authLogic))

	listener, err := net.Listen("tcp", cfg.LogicServer01.ListenAddr)
	if err != nil {
		glog.Fatal(ctx, "listen logicserver failed", glog.String("addr", cfg.LogicServer01.ListenAddr), glog.Err(err))
	}
	glog.Info(ctx, "logicserver started", glog.String("addr", cfg.LogicServer01.ListenAddr))

	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()

	// 启动 gRPC 服务，直到进程收到退出信号
	if err := grpcServer.Serve(listener); err != nil {
		glog.Fatal(context.Background(), "serve logicserver failed", glog.Err(err))
	}
}
