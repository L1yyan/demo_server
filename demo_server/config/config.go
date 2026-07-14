package config

import "time"

type RedisConfig struct {
	Addr     string `yaml:"addr"`      // Redis地址
	Password string `yaml:"password"`  // Redis密码
	DB       int    `yaml:"mysql"`     // Redis数据库
	PoolSize int    `yaml:"pool_size"` // 连接池大小
}

type MongoDBConfig struct {
	Addr            string `yaml:"addr"`
	Username        string `yaml:"username"`
	Password        string `yaml:"password"`
	Database        string `yaml:"database"`
	AuthSource      string `yaml:"authSource"`
	MaxIdleConns    int    `yaml:"max_idle_conns"`
	MaxOpenConns    int    `yaml:"max_open_conns"`
	ConnMaxIdleTime int    `yaml:"conn_max_idle_time"`
}

type EmailConfig struct {
	Host     string `yaml:"host"`     // 邮件服务器地址
	Port     int    `yaml:"port"`     // 邮件服务器端口
	Username string `yaml:"username"` // 邮件服务器用户名
	Password string `yaml:"password"` // 邮件服务器密码
}

type JwtConfig struct {
	SecretKey         string        `yaml:"secretKey"`
	TokenExpire       time.Duration `yaml:"tokenExpire"`
	RefreshExpire     time.Duration `yaml:"refreshExpire"`
	TokenHeaderKey    string        `yaml:"tokenHeaderKey"`
	RefreshTokenKey   string        `yaml:"refreshTokenKey"`
	NewAccessTokenKey string        `yaml:"newAccessTokenKey"`
	SkipPaths         []string      `yaml:"skipPaths"`
}
