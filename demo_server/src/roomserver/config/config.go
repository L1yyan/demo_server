package config

import "time"

const (
	// PhysicsBackendPhysX 表示默认启用 PhysX 物理后端
	PhysicsBackendPhysX = "physx"
	// PhysicsBackendSimple 表示使用 Go 简化物理后端
	PhysicsBackendSimple = "simple"
)

// Config roomserver 运行配置
type Config struct {
	ServerID            string        // 服务唯一ID
	ListenAddr          string        // KCP监听地址
	TokenSecret         string        // 入房令牌签名密钥
	MaxRooms            int           // 单进程最大房间数
	MaxPlayersPerRoom   int           // 单房间最大玩家数
	TickRate            int           // 房间逻辑帧率
	SnapshotRate        int           // 状态快照发送频率
	ReadTimeout         time.Duration // 连接读超时时间
	WriteQueueSize      int           // 单连接发送队列长度
	MaxPayloadSize      uint32        // 单条消息最大负载大小
	PhysicsBackend      string        // 物理后端，默认 physx
	PlayerCapsuleRadius float64       // 玩家胶囊体半径
	PlayerCapsuleHeight float64       // 玩家胶囊体高度
	PhysicsGroundPlane  bool          // 是否创建默认地面
}

// DefaultConfig 返回 roomserver 默认配置
func DefaultConfig() Config {
	return Config{
		ServerID:            "room-01",
		ListenAddr:          ":9001",
		TokenSecret:         "room-token-secret",
		MaxRooms:            1000,
		MaxPlayersPerRoom:   2,
		TickRate:            20,
		SnapshotRate:        10,
		ReadTimeout:         10 * time.Second,
		WriteQueueSize:      128,
		MaxPayloadSize:      64 * 1024,
		PhysicsBackend:      PhysicsBackendPhysX,
		PlayerCapsuleRadius: 0.35,
		PlayerCapsuleHeight: 1.8,
		PhysicsGroundPlane:  true,
	}
}

// Normalize 补齐配置默认值
func (c Config) Normalize() Config {
	defaults := DefaultConfig()
	if c.ServerID == "" {
		c.ServerID = defaults.ServerID
	}
	if c.ListenAddr == "" {
		c.ListenAddr = defaults.ListenAddr
	}
	if c.TokenSecret == "" {
		c.TokenSecret = defaults.TokenSecret
	}
	if c.MaxRooms <= 0 {
		c.MaxRooms = defaults.MaxRooms
	}
	if c.MaxPlayersPerRoom <= 0 {
		c.MaxPlayersPerRoom = defaults.MaxPlayersPerRoom
	}
	if c.TickRate <= 0 {
		c.TickRate = defaults.TickRate
	}
	if c.SnapshotRate <= 0 {
		c.SnapshotRate = defaults.SnapshotRate
	}
	if c.ReadTimeout <= 0 {
		c.ReadTimeout = defaults.ReadTimeout
	}
	if c.WriteQueueSize <= 0 {
		c.WriteQueueSize = defaults.WriteQueueSize
	}
	if c.MaxPayloadSize == 0 {
		c.MaxPayloadSize = defaults.MaxPayloadSize
	}
	if c.PhysicsBackend == "" {
		c.PhysicsBackend = defaults.PhysicsBackend
	}
	if c.PlayerCapsuleRadius <= 0 {
		c.PlayerCapsuleRadius = defaults.PlayerCapsuleRadius
	}
	if c.PlayerCapsuleHeight <= 0 {
		c.PlayerCapsuleHeight = defaults.PlayerCapsuleHeight
	}
	return c
}
