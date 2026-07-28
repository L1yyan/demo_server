package logic

import (
	"errors"
	"math"
	"sync"
)

const (
	defaultPlayerCapsuleRadius = 0.35      // 玩家胶囊体半径
	defaultPlayerCapsuleHeight = 1.8       // 玩家胶囊体高度
	defaultSpawnAID            = "spawn_a" // 默认A出生点ID
	defaultSpawnBID            = "spawn_b" // 默认B出生点ID
)

var (
	// ErrPhysicsWorldClosed 表示物理世界已经释放
	ErrPhysicsWorldClosed = errors.New("physics world closed")
	// ErrPhysicsPlayerNotFound 表示物理世界中没有指定玩家
	ErrPhysicsPlayerNotFound = errors.New("physics player not found")
	// ErrInvalidPhysicsRequest 表示物理请求参数非法
	ErrInvalidPhysicsRequest = errors.New("invalid physics request")
)

// MovePlayerRequest 玩家移动物理请求
type MovePlayerRequest struct {
	PlayerID  uint64  // 玩家ID
	Direction Vector3 // 移动方向
	Distance  float64 // 移动距离
	DeltaTime float64 // 当前物理步长
}

// MovePlayerResult 玩家移动物理结果
type MovePlayerResult struct {
	Position Vector3 // 物理修正后的坐标
	Blocked  bool    // 是否被碰撞阻挡
}

// RaycastRequest 射线检测请求
type RaycastRequest struct {
	Origin      Vector3 // 射线起点
	Direction   Vector3 // 射线方向
	MaxDistance float64 // 最大检测距离
	Mask        uint32  // 碰撞过滤掩码
}

// RaycastHit 射线检测结果
type RaycastHit struct {
	Hit      bool    // 是否命中
	TargetID uint64  // 命中目标ID
	Point    Vector3 // 命中点
	Normal   Vector3 // 命中面法线
	Distance float64 // 命中距离
}

// SpawnPoint 地图出生点
type SpawnPoint struct {
	ID       string  // 出生点ID
	Position Vector3 // 出生坐标
	Yaw      float64 // 初始水平朝向
}

// PhysicsWorld 物理世界接口
type PhysicsWorld interface {
	AddPlayer(playerID uint64, position Vector3) error
	RemovePlayer(playerID uint64) error
	MovePlayer(MovePlayerRequest) (MovePlayerResult, error)
	GetPlayerPosition(playerID uint64) (Vector3, error)
	SetPlayerPosition(playerID uint64, position Vector3) error
	Raycast(RaycastRequest) (RaycastHit, error)
	BatchRaycast([]RaycastRequest) ([]RaycastHit, error)
	SpawnPoints() []SpawnPoint
	Close() error
}

// PhysicsWorldFactory 物理世界工厂
type PhysicsWorldFactory interface {
	NewWorld(roomID string) (PhysicsWorld, error)
}

// SimplePhysicsWorld 简化物理世界占位实现
type SimplePhysicsWorld struct {
	mu      sync.Mutex
	players map[uint64]Vector3
	closed  bool
}

// SimplePhysicsWorldFactory 简化物理世界工厂
type SimplePhysicsWorldFactory struct{}

// NewSimplePhysicsWorldFactory 创建简化物理世界工厂
func NewSimplePhysicsWorldFactory() SimplePhysicsWorldFactory {
	return SimplePhysicsWorldFactory{}
}

// NewWorld 创建简化物理世界
func (f SimplePhysicsWorldFactory) NewWorld(roomID string) (PhysicsWorld, error) {
	return NewSimplePhysicsWorld(), nil
}

// NewSimplePhysicsWorld 创建简化物理世界
func NewSimplePhysicsWorld() *SimplePhysicsWorld {
	return &SimplePhysicsWorld{players: make(map[uint64]Vector3)}
}

// SpawnPoints 返回简化后端默认出生点
func (w *SimplePhysicsWorld) SpawnPoints() []SpawnPoint {
	return []SpawnPoint{
		{ID: defaultSpawnAID, Position: Vector3{X: -4, Y: 0.1, Z: 0}, Yaw: 0},
		{ID: defaultSpawnBID, Position: Vector3{X: 4, Y: 0.1, Z: 0}, Yaw: 180},
	}
}

// AddPlayer 添加玩家物理对象
func (w *SimplePhysicsWorld) AddPlayer(playerID uint64, position Vector3) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrPhysicsWorldClosed
	}
	w.players[playerID] = position
	return nil
}

// RemovePlayer 移除玩家物理对象
func (w *SimplePhysicsWorld) RemovePlayer(playerID uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrPhysicsWorldClosed
	}
	delete(w.players, playerID)
	return nil
}

// MovePlayer 按简化边界推进玩家位置
func (w *SimplePhysicsWorld) MovePlayer(req MovePlayerRequest) (MovePlayerResult, error) {
	if req.PlayerID == 0 || !vectorFinite(req.Direction) || !isFinite(req.Distance) || req.Distance < 0 {
		return MovePlayerResult{}, ErrInvalidPhysicsRequest
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return MovePlayerResult{}, ErrPhysicsWorldClosed
	}
	position, exists := w.players[req.PlayerID]
	if !exists {
		return MovePlayerResult{}, ErrPhysicsPlayerNotFound
	}

	// 简化后端只做世界边界限制，真实碰撞由 PhysX 后端负责
	next := Vector3{
		X: clampFloat(position.X+req.Direction.X*req.Distance, -defaultWorldLimit, defaultWorldLimit),
		Y: clampFloat(position.Y+req.Direction.Y*req.Distance, 0, defaultWorldLimit),
		Z: clampFloat(position.Z+req.Direction.Z*req.Distance, -defaultWorldLimit, defaultWorldLimit),
	}
	blocked := next.X != position.X+req.Direction.X*req.Distance || next.Z != position.Z+req.Direction.Z*req.Distance
	w.players[req.PlayerID] = next
	return MovePlayerResult{Position: next, Blocked: blocked}, nil
}

// GetPlayerPosition 读取玩家当前物理位置
func (w *SimplePhysicsWorld) GetPlayerPosition(playerID uint64) (Vector3, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return Vector3{}, ErrPhysicsWorldClosed
	}
	position, exists := w.players[playerID]
	if !exists {
		return Vector3{}, ErrPhysicsPlayerNotFound
	}
	return position, nil
}

// SetPlayerPosition 设置玩家当前物理位置
func (w *SimplePhysicsWorld) SetPlayerPosition(playerID uint64, position Vector3) error {
	if playerID == 0 || !vectorFinite(position) {
		return ErrInvalidPhysicsRequest
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrPhysicsWorldClosed
	}
	if _, exists := w.players[playerID]; !exists {
		return ErrPhysicsPlayerNotFound
	}
	w.players[playerID] = position
	return nil
}

// Raycast 执行单条射线检测
func (w *SimplePhysicsWorld) Raycast(req RaycastRequest) (RaycastHit, error) {
	if !validRaycastRequest(req) {
		return RaycastHit{}, ErrInvalidPhysicsRequest
	}
	return RaycastHit{}, nil
}

// BatchRaycast 批量执行射线检测
func (w *SimplePhysicsWorld) BatchRaycast(reqs []RaycastRequest) ([]RaycastHit, error) {
	hits := make([]RaycastHit, len(reqs))
	for _, req := range reqs {
		if !validRaycastRequest(req) {
			return nil, ErrInvalidPhysicsRequest
		}
	}
	return hits, nil
}

// Close 释放简化物理世界
func (w *SimplePhysicsWorld) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	w.players = nil
	return nil
}

// validRaycastRequest 校验射线请求参数
func validRaycastRequest(req RaycastRequest) bool {
	return vectorFinite(req.Origin) && vectorFinite(req.Direction) && vectorLength(req.Direction) > 0 && isFinite(req.MaxDistance) && req.MaxDistance > 0
}

// vectorFinite 判断向量是否为有限值
func vectorFinite(value Vector3) bool {
	return isFinite(value.X) && isFinite(value.Y) && isFinite(value.Z)
}

// vectorLength 计算向量长度
func vectorLength(value Vector3) float64 {
	return math.Sqrt(value.X*value.X + value.Y*value.Y + value.Z*value.Z)
}
