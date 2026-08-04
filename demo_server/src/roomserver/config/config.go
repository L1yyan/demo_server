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
	ServerID                   string        `yaml:"server_id"`                     // 服务唯一ID
	ListenAddr                 string        `yaml:"listen_addr"`                   // KCP监听地址
	TokenSecret                string        `yaml:"token_secret"`                  // 入房令牌签名密钥
	MaxRooms                   int           `yaml:"max_rooms"`                     // 单进程最大房间数
	MaxPlayersPerRoom          int           `yaml:"max_players_per_room"`          // 单房间最大玩家数
	TickRate                   int           `yaml:"tick_rate"`                     // 房间逻辑帧率
	SnapshotRate               int           `yaml:"snapshot_rate"`                 // 状态快照发送频率
	ReadTimeout                time.Duration `yaml:"read_timeout"`                  // 连接读超时时间
	WriteQueueSize             int           `yaml:"write_queue_size"`              // 单连接发送队列长度
	MaxPayloadSize             uint32        `yaml:"max_payload_size"`              // 单条消息最大负载大小
	PhysicsBackend             string        `yaml:"physics_backend"`               // 物理后端，默认 physx
	PlayerCapsuleRadius        float64       `yaml:"player_capsule_radius"`         // 玩家胶囊体半径
	PlayerCapsuleHeight        float64       `yaml:"player_capsule_height"`         // 玩家胶囊体高度
	PhysicsGroundPlane         bool          `yaml:"physics_ground_plane"`          // 是否创建默认地面
	DefaultMapID               string        `yaml:"default_map_id"`                // 默认地图ID
	MapCollisionPath           string        `yaml:"map_collision_path"`            // 地图碰撞文件路径
	PhysicsHash                string        `yaml:"physics_hash"`                  // 默认物理数据hash
	PredictionEnabled          bool          `yaml:"prediction_enabled"`            // 是否启用预测同步
	RollbackWindowTicks        int64         `yaml:"rollback_window_ticks"`         // 回滚历史窗口帧数
	FutureInputWindowTicks     int64         `yaml:"future_input_window_ticks"`     // 允许未来输入窗口帧数
	PredictionKeyframeInterval int64         `yaml:"prediction_keyframe_interval"`  // 预测关键帧校验间隔
	PositionTolerance          float64       `yaml:"position_tolerance"`            // 普通位置误差阈值
	HardPositionTolerance      float64       `yaml:"hard_position_tolerance"`       // 硬纠偏位置误差阈值
	AngleTolerance             float64       `yaml:"angle_tolerance"`               // 角度误差阈值
	MaxInputBatchFrames        int           `yaml:"max_input_batch_frames"`        // 单批输入最大帧数
	MaxInputHoldTicks          int64         `yaml:"max_input_hold_ticks"`          // 缺帧时移动输入最大沿用帧数
	CorrectionMinIntervalTicks int64         `yaml:"correction_min_interval_ticks"` // 普通纠偏最小间隔帧数
	GameDuration               time.Duration `yaml:"game_duration"`                 // 单局对局时长
}

// DefaultConfig 返回 roomserver 默认配置
func DefaultConfig() Config {
	return Config{
		ServerID:                   "room-01",
		ListenAddr:                 ":9001",
		TokenSecret:                "room-token-secret",
		MaxRooms:                   1000,
		MaxPlayersPerRoom:          2,
		TickRate:                   20,
		SnapshotRate:               10,
		ReadTimeout:                10 * time.Second,
		WriteQueueSize:             128,
		MaxPayloadSize:             64 * 1024,
		PhysicsBackend:             PhysicsBackendPhysX,
		PlayerCapsuleRadius:        0.35,
		PlayerCapsuleHeight:        1.8,
		PhysicsGroundPlane:         true,
		DefaultMapID:               "mfps_arena",
		MapCollisionPath:           "config/maps/mfps_arena/collision.json",
		PhysicsHash:                "sha256:70921a6cda71319a1bb4e203d23cc60dd09b42854bd5a3785ff892e2ec9387d8",
		PredictionEnabled:          true,
		RollbackWindowTicks:        60,
		FutureInputWindowTicks:     8,
		PredictionKeyframeInterval: 2,
		PositionTolerance:          0.15,
		HardPositionTolerance:      0.5,
		AngleTolerance:             2.0,
		MaxInputBatchFrames:        8,
		MaxInputHoldTicks:          8,
		CorrectionMinIntervalTicks: 2,
		GameDuration:               3 * time.Minute,
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
	if c.DefaultMapID == "" {
		c.DefaultMapID = defaults.DefaultMapID
	}
	if c.MapCollisionPath == "" {
		c.MapCollisionPath = defaults.MapCollisionPath
	}
	if c.PhysicsHash == "" {
		c.PhysicsHash = defaults.PhysicsHash
	}
	if c.RollbackWindowTicks <= 0 {
		c.RollbackWindowTicks = defaults.RollbackWindowTicks
	}
	if c.FutureInputWindowTicks <= 0 {
		c.FutureInputWindowTicks = defaults.FutureInputWindowTicks
	}
	if c.PredictionKeyframeInterval <= 0 {
		c.PredictionKeyframeInterval = defaults.PredictionKeyframeInterval
	}
	if c.PositionTolerance <= 0 {
		c.PositionTolerance = defaults.PositionTolerance
	}
	if c.HardPositionTolerance <= 0 {
		c.HardPositionTolerance = defaults.HardPositionTolerance
	}
	if c.HardPositionTolerance < c.PositionTolerance {
		c.HardPositionTolerance = c.PositionTolerance
	}
	if c.AngleTolerance <= 0 {
		c.AngleTolerance = defaults.AngleTolerance
	}
	if c.MaxInputBatchFrames <= 0 {
		c.MaxInputBatchFrames = defaults.MaxInputBatchFrames
	}
	if c.MaxInputHoldTicks < 0 {
		c.MaxInputHoldTicks = defaults.MaxInputHoldTicks
	}
	if c.CorrectionMinIntervalTicks <= 0 {
		c.CorrectionMinIntervalTicks = defaults.CorrectionMinIntervalTicks
	}
	if c.GameDuration <= 0 {
		c.GameDuration = defaults.GameDuration
	}
	return c
}
