package glog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultServiceName = "server"     // 默认服务名
	defaultRootDir     = "./bin/logs" // 默认日志根目录
	defaultLevel       = "info"       // 默认日志级别
	goModFileName      = "go.mod"     // Go 模块文件名
)

// Config 日志配置
type Config struct {
	ServiceName string // 服务名，例如 logic_server
	RootDir     string // 日志根目录，例如 ./bin/logs
	Level       string // debug/info/warn/error
	Console     bool   // 是否同时输出到控制台
	AddCaller   bool   // 是否输出调用文件和行号
}

// DefaultConfig 返回默认日志配置
func DefaultConfig() Config {
	return Config{
		ServiceName: defaultServiceName,
		RootDir:     defaultRootDir,
		Level:       defaultLevel,
		Console:     true,
		AddCaller:   true,
	}
}

// validate 校验日志配置
func (c Config) validate() error {
	if strings.TrimSpace(c.ServiceName) == "" {
		return errors.New("service name is empty")
	}
	if strings.ContainsAny(c.ServiceName, `/\`) {
		return errors.New("service name cannot contain path separator")
	}
	if strings.TrimSpace(c.RootDir) == "" {
		return errors.New("root dir is empty")
	}
	if !isSupportedLevel(c.Level) {
		return errors.New("unsupported log level")
	}
	return nil
}

// normalizeRootDir 将相对日志目录解析到项目根目录
func (c Config) normalizeRootDir() (Config, error) {
	if filepath.IsAbs(c.RootDir) {
		c.RootDir = filepath.Clean(c.RootDir)
		return c, nil
	}

	projectRoot, err := findProjectRoot()
	if err != nil {
		return c, err
	}
	c.RootDir = filepath.Join(projectRoot, c.RootDir)
	return c, nil
}

// serviceLogDir 返回服务日志目录
func (c Config) serviceLogDir() string {
	return filepath.Join(c.RootDir, c.ServiceName)
}

// infoLogDir 返回非 error 日志目录
func (c Config) infoLogDir() string {
	return filepath.Join(c.serviceLogDir(), "info")
}

// errorLogDir 返回 error 日志目录
func (c Config) errorLogDir() string {
	return filepath.Join(c.serviceLogDir(), "error")
}

// isSupportedLevel 判断日志级别是否支持
func isSupportedLevel(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug", "info", "warn", "warning", "error":
		return true
	default:
		return false
	}
}

// findProjectRoot 从当前目录向上查找项目根目录
func findProjectRoot() (string, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	currentDir := workDir
	for {
		goModPath := filepath.Join(currentDir, goModFileName)
		if _, err := os.Stat(goModPath); err == nil {
			return currentDir, nil
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return "", errors.New("project root not found")
		}
		currentDir = parentDir
	}
}
