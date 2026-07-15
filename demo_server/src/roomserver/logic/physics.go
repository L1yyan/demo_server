package logic

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

// PhysicsWorld 物理世界接口
type PhysicsWorld interface {
	Raycast(RaycastRequest) (RaycastHit, error)
	BatchRaycast([]RaycastRequest) ([]RaycastHit, error)
}

// SimplePhysicsWorld 简化物理世界占位实现
type SimplePhysicsWorld struct{}

// NewSimplePhysicsWorld 创建简化物理世界
func NewSimplePhysicsWorld() *SimplePhysicsWorld {
	return &SimplePhysicsWorld{}
}

// Raycast 执行单条射线检测
func (w *SimplePhysicsWorld) Raycast(req RaycastRequest) (RaycastHit, error) {
	return RaycastHit{}, nil
}

// BatchRaycast 批量执行射线检测
func (w *SimplePhysicsWorld) BatchRaycast(reqs []RaycastRequest) ([]RaycastHit, error) {
	hits := make([]RaycastHit, len(reqs))
	return hits, nil
}
