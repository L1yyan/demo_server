package logic

// SyncConfig 房间状态同步配置
type SyncConfig struct {
	MaxInputHoldTicks int64 // 缺帧时移动输入最大沿用帧数
}

// Normalize 按房间 tickRate 补齐同步配置默认值
func (c SyncConfig) Normalize(tickRate int) SyncConfig {
	if c.MaxInputHoldTicks < 0 {
		c.MaxInputHoldTicks = 8
	}
	return c
}

type playerSyncState struct {
	inputs              map[int64]authoritativeInput
	lastInput           authoritativeInput
	hasLastInput        bool
	lastInputTick       int64
	lastAppliedTick     int64
	lastQueuedInputTick int64
}

// newPlayerSyncState 创建玩家同步状态
func newPlayerSyncState() *playerSyncState {
	return &playerSyncState{
		inputs: make(map[int64]authoritativeInput),
	}
}
