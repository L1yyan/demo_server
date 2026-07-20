package physx

// Config PhysX 物理后端配置
type Config struct {
	PlayerCapsuleRadius float64 // 玩家胶囊体半径
	PlayerCapsuleHeight float64 // 玩家胶囊体高度
	CreateGroundPlane   bool    // 是否创建默认地面
}
