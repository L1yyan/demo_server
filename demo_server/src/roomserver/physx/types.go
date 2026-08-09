package physx

// Config PhysX 物理后端配置
type Config struct {
	PlayerCapsuleRadius float64 // 玩家胶囊体半径
	PlayerCapsuleHeight float64 // 玩家胶囊体高度
	CreateGroundPlane   bool    // 是否创建默认地面
	PVDEnabled          bool    // 是否启用 PhysX PVD
	PVDHost             string  // PhysX PVD 监听地址
	PVDPort             int     // PhysX PVD 监听端口
	PVDTimeoutMS        int     // PhysX PVD 连接超时毫秒数
	DefaultMapID        string  // 默认地图ID
	MapCollisionPath    string  // 地图碰撞文件路径
}
