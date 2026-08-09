package config

import (
	"strings"
	"time"
)

const (
	// PhysicsBackendPhysX 表示默认启用 PhysX 物理后端
	PhysicsBackendPhysX = "physx"
	// PhysicsBackendSimple 表示使用 Go 简化物理后端
	PhysicsBackendSimple = "simple"
	// DefaultPhysXPVDHost 表示默认 PhysX PVD 监听地址
	DefaultPhysXPVDHost = "127.0.0.1"
	// DefaultPhysXPVDPort 表示默认 PhysX PVD 监听端口
	DefaultPhysXPVDPort = 5425
	// DefaultPhysXPVDTimeoutMS 表示默认 PhysX PVD 连接超时毫秒数
	DefaultPhysXPVDTimeoutMS = 100
)

// Config roomserver 运行配置
type Config struct {
	ServerID            string        `yaml:"server_id"`             // 服务唯一ID
	ListenAddr          string        `yaml:"listen_addr"`           // KCP监听地址
	TokenSecret         string        `yaml:"token_secret"`          // 入房令牌签名密钥
	MaxRooms            int           `yaml:"max_rooms"`             // 单进程最大房间数
	MaxPlayersPerRoom   int           `yaml:"max_players_per_room"`  // 单房间最大玩家数
	TickRate            int           `yaml:"tick_rate"`             // 房间逻辑帧率
	SnapshotRate        int           `yaml:"snapshot_rate"`         // 状态快照发送频率
	ReadTimeout         time.Duration `yaml:"read_timeout"`          // 连接读超时时间
	WriteQueueSize      int           `yaml:"write_queue_size"`      // 单连接发送队列长度
	MaxPayloadSize      uint32        `yaml:"max_payload_size"`      // 单条消息最大负载大小
	PhysicsBackend      string        `yaml:"physics_backend"`       // 物理后端，默认 physx
	PlayerCapsuleRadius float64       `yaml:"player_capsule_radius"` // 玩家胶囊体半径
	PlayerCapsuleHeight float64       `yaml:"player_capsule_height"` // 玩家胶囊体高度
	PhysicsGroundPlane  bool          `yaml:"physics_ground_plane"`  // 是否创建默认地面
	PhysXPVDEnabled     bool          `yaml:"physx_pvd_enabled"`     // 是否启用 PhysX PVD
	PhysXPVDHost        string        `yaml:"physx_pvd_host"`        // PhysX PVD 监听地址
	PhysXPVDPort        int           `yaml:"physx_pvd_port"`        // PhysX PVD 监听端口
	PhysXPVDTimeoutMS   int           `yaml:"physx_pvd_timeout_ms"`  // PhysX PVD 连接超时毫秒数
	DefaultMapID        string        `yaml:"default_map_id"`        // 默认地图ID
	MapCollisionPath    string        `yaml:"map_collision_path"`    // 地图碰撞文件路径
	PhysicsHash         string        `yaml:"physics_hash"`          // 默认物理数据hash
	MaxInputHoldTicks   int64         `yaml:"max_input_hold_ticks"`  // 缺帧时移动输入最大沿用帧数
	GameDuration        time.Duration `yaml:"game_duration"`         // 单局对局时长
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
		PhysXPVDEnabled:     false,
		PhysXPVDHost:        DefaultPhysXPVDHost,
		PhysXPVDPort:        DefaultPhysXPVDPort,
		PhysXPVDTimeoutMS:   DefaultPhysXPVDTimeoutMS,
		DefaultMapID:        "mfps_arena",
		MapCollisionPath:    "config/maps/mfps_arena/collision.json",
		PhysicsHash:         "sha256:70921a6cda71319a1bb4e203d23cc60dd09b42854bd5a3785ff892e2ec9387d8",
		MaxInputHoldTicks:   8,
		GameDuration:        3 * time.Minute,
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
	if strings.TrimSpace(c.PhysXPVDHost) == "" {
		c.PhysXPVDHost = defaults.PhysXPVDHost
	} else {
		c.PhysXPVDHost = strings.TrimSpace(c.PhysXPVDHost)
	}
	if c.PhysXPVDPort <= 0 {
		c.PhysXPVDPort = defaults.PhysXPVDPort
	}
	if c.PhysXPVDTimeoutMS <= 0 {
		c.PhysXPVDTimeoutMS = defaults.PhysXPVDTimeoutMS
	}
	if c.DefaultMapID == "" {
		c.DefaultMapID = defaults.DefaultMapID
	}
	if c.MapCollisionPath == "" {
		c.MapCollisionPath = defaults.MapCollisionPath
	}
	if c.PhysicsHash == "" {
		c.PhysicsHash = defaults.PhysicsHash
	}
	if c.MaxInputHoldTicks < 0 {
		c.MaxInputHoldTicks = defaults.MaxInputHoldTicks
	}
	if c.GameDuration <= 0 {
		c.GameDuration = defaults.GameDuration
	}
	return c
}
