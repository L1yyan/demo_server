package consts

type UserInfo struct {
	UserID         uint64 `bson:"user_id"`          // 业务用户ID
	Email          string `bson:"email"`            // 邮箱
	Nickname       string `bson:"nickname"`         // 昵称
	Level          string `bson:"level"`            // 等级
	Exp            int64  `bson:"exp"`              // 经验值
	Coins          int64  `bson:"coins"`            // 货币数量
	KillCount      int64  `bson:"kill_count"`       //击杀数量
	ProfilePhotoID int32  `bson:"profile_photo_id"` // 头像ID
	PasswordHash   string `bson:"password_hash"`    // 密码哈希
}

type UserWare struct {
	GunIds []int32 `bson:"gun_ids"`
}

type Gun struct {
	Id    int32  `bson:"id"`    //武器id
	Price int64  `bson:"price"` //武器价格
	Name  string `bson:"name"`  //武器名字
}

var Level = []int64{
	0,    // Level 1
	500,  // Level 2
	1500, // Level 3
	3000, // Level 4
	5000, // Level 5
}
