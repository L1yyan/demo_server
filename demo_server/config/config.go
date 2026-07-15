package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigPath      = "config/config.yaml" // 默认配置文件路径
	defaultLogicListenAddr = ":8080"              // logicserver 默认监听地址
	defaultProjectMarkFile = "go.mod"             // 项目根目录标记文件
)

// Config 项目总配置
type Config struct {
	Log           LogConfig         `yaml:"log"`             // 日志配置
	Email         EmailConfig       `yaml:"email"`           // 邮件配置
	JWT           JwtConfig         `yaml:"jwt"`             // JWT配置
	LogicServer01 LogicServerConfig `yaml:"logic_server_01"` // logicserver配置
}

// LogConfig 日志配置
type LogConfig struct {
	Path string `yaml:"path"` // 日志根目录
}

type RedisConfig struct {
	Addr     string `yaml:"addr"`      // Redis地址
	Password string `yaml:"password"`  // Redis密码
	DB       int    `yaml:"db"`        // Redis数据库
	PoolSize int    `yaml:"pool_size"` // 连接池大小
}

type MongoDBConfig struct {
	Addr            string `yaml:"addr"`               // MongoDB地址
	Username        string `yaml:"username"`           // MongoDB用户名
	Password        string `yaml:"password"`           // MongoDB密码
	Database        string `yaml:"database"`           // MongoDB数据库
	AuthSource      string `yaml:"authSource"`         // MongoDB认证库
	MaxIdleConns    int    `yaml:"max_idle_conns"`     // 最小空闲连接数
	MaxOpenConns    int    `yaml:"max_open_conns"`     // 最大连接数
	ConnMaxIdleTime int    `yaml:"conn_max_idle_time"` // 连接最大空闲秒数
}

type EmailConfig struct {
	Host     string `yaml:"host"`     // 邮件服务器地址
	Port     int    `yaml:"port"`     // 邮件服务器端口
	Username string `yaml:"username"` // 邮件服务器用户名
	Password string `yaml:"password"` // 邮件服务器密码
}

type JwtConfig struct {
	SecretKey         string        `yaml:"secretKey"`         // 签名密钥
	TokenExpire       time.Duration `yaml:"tokenExpire"`       // 访问令牌过期时间
	RefreshExpire     time.Duration `yaml:"refreshExpire"`     // 刷新令牌过期时间
	TokenHeaderKey    string        `yaml:"tokenHeaderKey"`    // 访问令牌请求头名称
	RefreshTokenKey   string        `yaml:"refreshTokenKey"`   // 刷新令牌请求头名称
	NewAccessTokenKey string        `yaml:"newAccessTokenKey"` // 新访问令牌响应头名称
	SkipPaths         []string      `yaml:"skipPaths"`         // 跳过鉴权路径
}

// LogicServerConfig logicserver运行配置
type LogicServerConfig struct {
	ListenAddr string        `yaml:"listen_addr"` // gRPC监听地址
	Redis      RedisConfig   `yaml:"redis"`       // Redis配置
	MongoDB    MongoDBConfig `yaml:"mongodb"`     // MongoDB配置
}

// Load 读取项目配置文件
func Load(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		resolvedPath, err := FindConfigPath()
		if err != nil {
			return nil, err
		}
		path = resolvedPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	cfg.normalize()
	return &cfg, nil
}

// FindConfigPath 从当前目录向上查找默认配置文件
func FindConfigPath() (string, error) {
	root, err := findProjectRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, defaultConfigPath), nil
}

// normalize 补齐配置默认值
func (c *Config) normalize() {
	if strings.TrimSpace(c.LogicServer01.ListenAddr) == "" {
		c.LogicServer01.ListenAddr = defaultLogicListenAddr
	}
}

// findProjectRoot 查找项目根目录
func findProjectRoot() (string, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	currentDir := workDir
	for {
		markPath := filepath.Join(currentDir, defaultProjectMarkFile)
		if _, err := os.Stat(markPath); err == nil {
			return currentDir, nil
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return "", errors.New("project root not found")
		}
		currentDir = parentDir
	}
}
