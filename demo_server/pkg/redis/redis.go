package redis

import (
	conf "demo_server/config"
	"sync"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

type Client struct {
	Rdb *redis.Client
	Nil error
}

var (
	safeRedisConfig atomic.Value  // 并发安全的 Redis 配置
	once            sync.Once     // 确保初始化只执行一次
	redisClient     *redis.Client // Redis 客户端实例
)

// InitRedisConf 初始化 Redis 配置，main 函数中调用
func InitRedisConf(redisConfig *conf.RedisConfig) {
	//存储在并发安全的Value中
	safeRedisConfig.Store(redisConfig)
}

// GetRedisConfig 获取Redis配置
func GetRedisConfig() *conf.RedisConfig {
	return safeRedisConfig.Load().(*conf.RedisConfig)
}

// InitRedis 创建一个新的 Redis 客户端
func initRedis() {
	once.Do(func() {
		redisConfig := GetRedisConfig()
		redisClient = redis.NewClient(&redis.Options{
			Addr:     redisConfig.Addr,
			Password: redisConfig.Password, // 如果没有密码，可以设置为 ""
			DB:       redisConfig.DB,       // 默认数据库
			PoolSize: redisConfig.PoolSize, // 连接池大小
		})
	})
}

// Instance 获取一个 Redis 客户端实例
func Instance() *Client {
	initRedis()
	return &Client{Rdb: redisClient, Nil: redis.Nil}
}
