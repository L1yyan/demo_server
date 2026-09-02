package logic

import (
	roompb "demo_server/gen/room"
	"demo_server/src/roomserver/protocol"
)

// Vector3 三维坐标或方向
type Vector3 struct {
	X float64 // X轴
	Y float64 // Y轴
	Z float64 // Z轴
}

// TODO: 加个equip_id字段
// Player 房间内玩家状态
type Player struct {
	ID                  uint64  // 玩家ID
	RoomID              string  // 房间ID
	X                   float64 // X坐标
	Y                   float64 // Y坐标
	Z                   float64 // Z坐标
	Yaw                 float64 // 水平视角
	Pitch               float64 // 垂直视角
	HP                  int     // 生命值
	KillCount           int     // 击杀数量
	DeathCount          int     // 死亡数量
	SpawnID             string  // 占用的出生点ID
	Session             Session // 玩家连接会话
	Alive               bool    // 是否存活
	InvincibleUntilTick int64   // 无敌结束帧号
	VerticalVelocity    float64 // 垂直速度
	Grounded            bool    // 是否处于地面
	Crouched            bool    // 是否处于下蹲状态
	GunId               int32   //手持武器id
	Move				Vector3 // 玩家移动方向
	LastMove 			Vector3 // 上一帧玩家移动方向
}

// Session logic 层依赖的连接抽象
type Session interface {
	ID() string
	Send(protocol.Message) bool
	SendSnapshot(protocol.Message) bool
	Close()
}

// IsInvincible 判断玩家在指定服务端帧是否处于无敌状态
func (p *Player) IsInvincible(serverTick int64) bool {
	return p != nil && p.InvincibleUntilTick > serverTick
}

// ToState 转换为协议快照状态
func (p *Player) ToState() *roompb.PlayerState {
	return p.ToStateAt(0)
}

// ToStateAt 按指定服务端帧转换为协议快照状态
func (p *Player) ToStateAt(serverTick int64) *roompb.PlayerState {
	if p == nil {
		return &roompb.PlayerState{}
	}
	return &roompb.PlayerState{
		PlayerId:            p.ID,
		SpawnId:             p.SpawnID,
		X:                   p.X,
		Y:                   p.Y,
		Z:                   p.Z,
		Yaw:                 p.Yaw,
		Pitch:               p.Pitch,
		Hp:                  int32(p.HP),
		KillCount:           int32(p.KillCount),
		DeathCount:          int32(p.DeathCount),
		Invincible:          p.IsInvincible(serverTick),
		InvincibleUntilTick: p.InvincibleUntilTick,
		GunId:               p.GunId,
		Crouched:            p.Crouched,
	}
}

// PlayerStats 玩家战绩快照
type PlayerStats struct {
	PlayerID   uint64 // 玩家ID
	KillCount  int    // 击杀数量
	DeathCount int    // 死亡数量
}

// ToStats 转换为玩家战绩数据
func (p *Player) ToStats() PlayerStats {
	if p == nil {
		return PlayerStats{}
	}
	return PlayerStats{
		PlayerID:   p.ID,
		KillCount:  p.KillCount,
		DeathCount: p.DeathCount,
	}
}
