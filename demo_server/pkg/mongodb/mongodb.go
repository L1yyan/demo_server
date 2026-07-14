package mongodb

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	conf "demo_server/config"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	safeMongoConfig atomic.Value  // 并发安全的 MongoDB 配置
	mongoClient     *mongo.Client // MongoDB 客户端实例
	once            sync.Once     // 确保初始化只执行一次
)

// InitMongoConf 初始化MongoDB配置
func InitMongoConf(mongoConfig *conf.MongoDBConfig) {
	safeMongoConfig.Store(mongoConfig)
}

// GetMongoConfig 获取MongoDB配置
func GetMongoConfig() *conf.MongoDBConfig {
	return safeMongoConfig.Load().(*conf.MongoDBConfig)
}

// InitMongo 创建一个新的MongoDB客户端
func initMongo() error {
	var err error
	once.Do(func() {
		mongoConfig := GetMongoConfig()

		// 构建连接 URI
		uri := fmt.Sprintf("mongodb://%s:%s@%s/%s?authSource=%s",
			mongoConfig.Username,
			mongoConfig.Password,
			mongoConfig.Addr,
			mongoConfig.Database,
			mongoConfig.AuthSource,
		)
		// 配置客户端选项
		clientOptions := options.Client().ApplyURI(uri)

		// 配置连接池
		clientOptions.SetMaxPoolSize(uint64(mongoConfig.MaxOpenConns))
		clientOptions.SetMinPoolSize(0) // 默认不设置最小连接数
		clientOptions.SetMaxConnIdleTime(time.Duration(mongoConfig.ConnMaxIdleTime) * time.Second)
		clientOptions.SetRetryReads(true) // 启用读取重试
		// 连接 MongoDB
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		mongoClient, err = mongo.Connect(ctx, clientOptions)
		if err != nil {
			return
		}

		// 验证连接
		if err = mongoClient.Ping(ctx, nil); err != nil {
			return
		}
	})
	return err
}

// Instance 获取一个MongoDB客户端实例
func Instance() *mongo.Client {
	initMongo()
	return mongoClient
}
