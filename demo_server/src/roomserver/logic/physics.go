package logic

import (
	"errors"
	"math"
	"sync"
)

const (
	defaultPlayerCapsuleRadius = 0.35      // 玩家胶囊体半径
	defaultPlayerCapsuleHeight = 1.8       // 玩家胶囊体高度
	defaultPlayerJumpSpeed     = 5.0       // 玩家跳跃初速度
	defaultPlayerGravity       = -9.8      // 玩家垂直重力加速度
	defaultSimpleGroundHeight  = 0.0       // simple 后端默认地面高度
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
	PlayerID         uint64  // 玩家ID
	Direction        Vector3 // 水平移动方向
	Distance         float64 // 水平移动距离
	DeltaTime        float64 // 当前物理步长
	Jump             bool    // 是否请求跳跃
	Squat            bool    // 是否请求下蹲
	Crouched         bool    // 当前是否处于下蹲状态
	Grounded         bool    // 当前是否处于地面
	VerticalVelocity float64 // 当前垂直速度
}

// MovePlayerResult 玩家移动物理结果
type MovePlayerResult struct {
	Position         Vector3 // 物理修正后的坐标
	Blocked          bool    // 是否被碰撞阻挡
	Grounded         bool    // 移动后是否处于地面
	Crouched         bool    // 移动后是否处于下蹲状态
	VerticalVelocity float64 // 移动后的垂直速度
}

// RaycastRequest 射线检测请求
type RaycastRequest struct {
	Origin         Vector3 // 射线起点
	Direction      Vector3 // 射线方向
	MaxDistance    float64 // 最大检测距离
	Mask           uint32  // 碰撞过滤掩码
	IgnorePlayerID uint64  // 忽略的玩家ID
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
	if req.PlayerID == 0 || !vectorFinite(req.Direction) || !isFinite(req.Distance) || req.Distance < 0 || !isFinite(req.DeltaTime) || req.DeltaTime < 0 || !isFinite(req.VerticalVelocity) {
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

	deltaTime := req.DeltaTime
	if deltaTime <= 0 {
		deltaTime = 1.0 / 60.0
	}
	verticalVelocity := nextVerticalVelocity(req.VerticalVelocity, req.Jump, req.Grounded, deltaTime)
	verticalMove := verticalVelocity * deltaTime

	// 简化后端只做世界边界和默认地面限制，真实地图碰撞由 PhysX 后端负责
	rawNext := Vector3{
		X: position.X + req.Direction.X*req.Distance,
		Y: position.Y + verticalMove,
		Z: position.Z + req.Direction.Z*req.Distance,
	}
	next := Vector3{
		X: clampFloat(rawNext.X, -defaultWorldLimit, defaultWorldLimit),
		Y: clampFloat(rawNext.Y, defaultSimpleGroundHeight, defaultWorldLimit),
		Z: clampFloat(rawNext.Z, -defaultWorldLimit, defaultWorldLimit),
	}
	grounded := (req.Grounded && !req.Jump && verticalVelocity == 0) || (next.Y <= defaultSimpleGroundHeight && verticalVelocity <= 0)
	if grounded {
		verticalVelocity = 0
	}
	blocked := next.X != rawNext.X || next.Y != rawNext.Y || next.Z != rawNext.Z
	w.players[req.PlayerID] = next
	return MovePlayerResult{Position: next, Blocked: blocked, Grounded: grounded, VerticalVelocity: verticalVelocity}, nil
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
	direction, ok := normalizedVector(req.Direction)
	if !ok {
		return RaycastHit{}, ErrInvalidPhysicsRequest
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return RaycastHit{}, ErrPhysicsWorldClosed
	}

	closestDistance := req.MaxDistance + 1
	var closest RaycastHit
	for playerID, position := range w.players {
		if playerID == req.IgnorePlayerID {
			continue
		}

		// simple 后端用玩家胶囊体中点球体近似命中体，真实遮挡交给 PhysX 后端
		center := Vector3{X: position.X, Y: position.Y + defaultPlayerCapsuleHeight*0.5, Z: position.Z}
		distance, hit := raySphereDistance(req.Origin, direction, center, defaultPlayerCapsuleRadius, req.MaxDistance)
		if !hit || distance >= closestDistance {
			continue
		}
		point := rayPoint(req.Origin, direction, distance)
		normal, _ := normalizedVector(subVector(point, center))
		closestDistance = distance
		closest = RaycastHit{Hit: true, TargetID: playerID, Point: point, Normal: normal, Distance: distance}
	}
	return closest, nil
}

// BatchRaycast 批量执行射线检测
func (w *SimplePhysicsWorld) BatchRaycast(reqs []RaycastRequest) ([]RaycastHit, error) {
	hits := make([]RaycastHit, len(reqs))
	for index, req := range reqs {
		hit, err := w.Raycast(req)
		if err != nil {
			return nil, err
		}
		hits[index] = hit
	}
	return hits, nil
}

// nextVerticalVelocity 计算跳跃和重力作用后的垂直速度
func nextVerticalVelocity(current float64, jump bool, grounded bool, deltaTime float64) float64 {
	if jump && grounded {
		return defaultPlayerJumpSpeed + defaultPlayerGravity*deltaTime
	}
	if grounded {
		return 0
	}
	return current + defaultPlayerGravity*deltaTime
}

// raySphereDistance 计算射线命中球体的最近距离
func raySphereDistance(origin Vector3, direction Vector3, center Vector3, radius float64, maxDistance float64) (float64, bool) {
	if radius <= 0 || maxDistance <= 0 {
		return 0, false
	}
	oc := subVector(origin, center)
	b := dotVector(oc, direction)
	c := dotVector(oc, oc) - radius*radius
	discriminant := b*b - c
	if discriminant < 0 {
		return 0, false
	}

	sqrtDiscriminant := math.Sqrt(discriminant)
	distance := -b - sqrtDiscriminant
	if distance < 0 {
		distance = -b + sqrtDiscriminant
	}
	if distance < 0 || distance > maxDistance {
		return 0, false
	}
	return distance, true
}

// rayPoint 计算射线上指定距离的点
func rayPoint(origin Vector3, direction Vector3, distance float64) Vector3 {
	return Vector3{
		X: origin.X + direction.X*distance,
		Y: origin.Y + direction.Y*distance,
		Z: origin.Z + direction.Z*distance,
	}
}

// subVector 计算两个向量差值
func subVector(a Vector3, b Vector3) Vector3 {
	return Vector3{X: a.X - b.X, Y: a.Y - b.Y, Z: a.Z - b.Z}
}

// dotVector 计算两个向量点积
func dotVector(a Vector3, b Vector3) float64 {
	return a.X*b.X + a.Y*b.Y + a.Z*b.Z
}

// normalizedVector 返回单位向量
func normalizedVector(value Vector3) (Vector3, bool) {
	length := vectorLength(value)
	if length <= 0 {
		return Vector3{}, false
	}
	return Vector3{X: value.X / length, Y: value.Y / length, Z: value.Z / length}, true
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
