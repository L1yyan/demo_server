package consts


type UserInfo struct {
	UserID         uint64             `bson:"user_id"`          // 业务用户ID
	Email          string             `bson:"email"`            // 邮箱
	Nickname       string             `bson:"nickname"`         // 昵称
	Level          string             `bson:"level"`            // 等级
	Experience     uint64             `bson:"experience"`       // 经验值
	Coins          uint64             `bson:"coins"`            // 货币数量
	ProfilePhotoID int32              `bson:"profile_photo_id"` // 头像ID
	PasswordHash   string             `bson:"password_hash"`    // 密码哈希
}
