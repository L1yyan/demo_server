package logic

import (
	"math"

	"demo_server/src/roomserver/protocol"
)

const (
	// SyncModeSnapshotOnly 表示只使用服务端快照同步
	SyncModeSnapshotOnly = "snapshot_only"
	// SyncModePredictionAuthoritative 表示启用客户端预测和服务端权威纠偏
	SyncModePredictionAuthoritative     = "prediction_authoritative"
	correctionReasonPositionError       = "position_error"
	correctionReasonAngleError          = "angle_error"
	correctionReasonStaleInput          = "stale_input"
	correctionReasonLateInputReschedule = "late_input_reschedule"
	correctionReasonRespawn             = "respawn"
)

// SyncConfig 房间同步配置
type SyncConfig struct {
	PredictionEnabled          bool    // 是否启用预测同步
	RollbackWindowTicks        int64   // 回滚历史窗口帧数
	FutureInputWindowTicks     int64   // 允许未来输入窗口帧数
	PredictionKeyframeInterval int64   // 预测关键帧校验间隔
	PositionTolerance          float64 // 普通位置误差阈值
	HardPositionTolerance      float64 // 硬纠偏位置误差阈值
	AngleTolerance             float64 // 角度误差阈值
	MaxInputBatchFrames        int     // 单批输入最大帧数
	MaxInputHoldTicks          int64   // 缺帧时移动输入最大沿用帧数
	CorrectionMinIntervalTicks int64   // 普通纠偏最小间隔帧数
}

// Normalize 按房间 tickRate 补齐同步配置默认值
func (c SyncConfig) Normalize(tickRate int) SyncConfig {
	if tickRate <= 0 {
		tickRate = 20
	}
	if c.RollbackWindowTicks <= 0 {
		c.RollbackWindowTicks = int64(tickRate * 3)
	}
	if c.FutureInputWindowTicks <= 0 {
		c.FutureInputWindowTicks = 8
	}
	if c.PredictionKeyframeInterval <= 0 {
		c.PredictionKeyframeInterval = 2
	}
	if c.PositionTolerance <= 0 {
		c.PositionTolerance = 0.15
	}
	if c.HardPositionTolerance <= 0 {
		c.HardPositionTolerance = 0.5
	}
	if c.HardPositionTolerance < c.PositionTolerance {
		c.HardPositionTolerance = c.PositionTolerance
	}
	if c.AngleTolerance <= 0 {
		c.AngleTolerance = 2.0
	}
	if c.MaxInputBatchFrames <= 0 {
		c.MaxInputBatchFrames = 8
	}
	if c.MaxInputHoldTicks < 0 {
		c.MaxInputHoldTicks = 8
	}
	if c.CorrectionMinIntervalTicks <= 0 {
		c.CorrectionMinIntervalTicks = 2
	}
	return c
}

type playerSyncState struct {
	inputs                   map[int64]authoritativeInput
	predictedStates          map[int64]protocol.PredictedPlayerState
	authoritativeHistory     map[int64]playerFrameState
	lateRescheduledTicks     map[int64]bool
	lastInputDiagnosticTicks map[string]int64
	lastInput                authoritativeInput
	hasLastInput             bool
	lastInputTick            int64
	lastAppliedTick          int64
	lastAcceptedInputTick    int64
	lastVerifiedTick         int64
	lastCorrectionTick       int64
}

type playerFrameState struct {
	Tick                int64
	PlayerID            uint64
	Position            Vector3
	Yaw                 float64
	Pitch               float64
	HP                  int
	KillCount           int
	DeathCount          int
	Alive               bool
	SpawnID             string
	InvincibleUntilTick int64
}

// newPlayerSyncState 创建玩家同步状态
func newPlayerSyncState() *playerSyncState {
	return &playerSyncState{
		inputs:                   make(map[int64]authoritativeInput),
		predictedStates:          make(map[int64]protocol.PredictedPlayerState),
		authoritativeHistory:     make(map[int64]playerFrameState),
		lateRescheduledTicks:     make(map[int64]bool),
		lastInputDiagnosticTicks: make(map[string]int64),
	}
}

// frameStateFromPlayer 从玩家状态构造权威帧状态
func frameStateFromPlayer(tick int64, player *Player) playerFrameState {
	if player == nil {
		return playerFrameState{Tick: tick}
	}
	return playerFrameState{
		Tick:                tick,
		PlayerID:            player.ID,
		Position:            Vector3{X: player.X, Y: player.Y, Z: player.Z},
		Yaw:                 player.Yaw,
		Pitch:               player.Pitch,
		HP:                  player.HP,
		KillCount:           player.KillCount,
		DeathCount:          player.DeathCount,
		Alive:               player.Alive,
		SpawnID:             player.SpawnID,
		InvincibleUntilTick: player.InvincibleUntilTick,
	}
}

// toPlayerState 转换为客户端协议状态
func (s playerFrameState) toPlayerState() protocol.PlayerState {
	return protocol.PlayerState{
		PlayerID:            s.PlayerID,
		SpawnID:             s.SpawnID,
		X:                   s.Position.X,
		Y:                   s.Position.Y,
		Z:                   s.Position.Z,
		Yaw:                 s.Yaw,
		Pitch:               s.Pitch,
		HP:                  s.HP,
		KillCount:           s.KillCount,
		DeathCount:          s.DeathCount,
		Invincible:          s.InvincibleUntilTick > s.Tick,
		InvincibleUntilTick: s.InvincibleUntilTick,
	}
}

// predictedStateFinite 判断预测状态是否为有效有限值
func predictedStateFinite(state protocol.PredictedPlayerState) bool {
	return isFinite(state.X) && isFinite(state.Y) && isFinite(state.Z) && isFinite(state.Yaw) && isFinite(state.Pitch)
}

// positionError 计算客户端预测位置和服务端位置误差
func positionError(predicted protocol.PredictedPlayerState, authoritative playerFrameState) float64 {
	dx := predicted.X - authoritative.Position.X
	dy := predicted.Y - authoritative.Position.Y
	dz := predicted.Z - authoritative.Position.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// angleError 计算客户端预测视角和服务端视角误差
func angleError(predicted protocol.PredictedPlayerState, authoritative playerFrameState) float64 {
	yawError := math.Abs(normalizeDegrees(predicted.Yaw - authoritative.Yaw))
	pitchError := math.Abs(predicted.Pitch - authoritative.Pitch)
	if yawError > pitchError {
		return yawError
	}
	return pitchError
}

// inputFrameToPlayerInput 转换批量输入帧为旧输入结构以复用校验逻辑
func inputFrameToPlayerInput(frame protocol.PlayerInputFrame) protocol.PlayerInput {
	return protocol.PlayerInput{
		ClientTick: frame.ClientTick,
		MoveX:      frame.MoveX,
		MoveZ:      frame.MoveZ,
		Yaw:        frame.Yaw,
		Pitch:      frame.Pitch,
		Fire:       frame.Fire,
	}
}
