package logic

import "demo_server/src/roomserver/protocol"

// Vector3 三维坐标或方向
type Vector3 struct {
	X float64 // X轴
	Y float64 // Y轴
	Z float64 // Z轴
}

// Player 房间内玩家状态
type Player struct {
	ID      uint64  // 玩家ID
	RoomID  string  // 房间ID
	X       float64 // X坐标
	Y       float64 // Y坐标
	Z       float64 // Z坐标
	Yaw     float64 // 水平视角
	Pitch   float64 // 垂直视角
	HP      int     // 生命值
	Session Session // 玩家连接会话
	Alive   bool    // 是否存活
}

// Session logic 层依赖的连接抽象
type Session interface {
	ID() string
	Send(protocol.Message) bool
	Close()
}

// ToState 转换为协议快照状态
func (p *Player) ToState() protocol.PlayerState {
	return protocol.PlayerState{
		PlayerID: p.ID,
		X:        p.X,
		Y:        p.Y,
		Z:        p.Z,
		Yaw:      p.Yaw,
		Pitch:    p.Pitch,
		HP:       p.HP,
	}
}
