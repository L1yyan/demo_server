package email

import (
	conf "demo_server/config"
	"sync/atomic"
)

var (
	safeEmailConfig atomic.Value // 并发安全的邮件配置
)

// InitEmailConf 创建一个新的邮件客户端实例
func InitEmailConf(config *conf.EmailConfig) {
	safeEmailConfig.Store(config)
}

// GetEmailConfig 获取邮件配置
func GetEmailConfig() *conf.EmailConfig {
	return safeEmailConfig.Load().(*conf.EmailConfig)
}
