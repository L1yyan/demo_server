package httpgateway

// 本包为 logicserver 提供 HTTP/JSON 网关，是给移动端（Android/iOS）用的。
// 移动端 Unity 客户端引入 gRPC 原生层会导致 APK 打包失败，因此把 logicserver
// 的 unary gRPC 方法等价暴露为 HTTP JSON 接口。
//
// 约定：
//   - 所有路由统一 POST + JSON body。
//   - 字段名 snake_case，与 pb/logic/logic.proto 一致，避免客户端语义错位。
//   - 业务成败看 body 里的 status/content，不看 HTTP 状态码（HTTP 非 2xx 只表示传输层/参数错误）。
//   - content 文案与 gRPC service 层保持一致，客户端错误提示映射表无需改动。
//
// roomserver 与 logicserver 之间、logicserver 与 matchserver 之间的内部调用仍走 gRPC，
// 不受本网关影响。

// ---- 认证 ----

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type registerReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type verifyTokenReq struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type authResp struct {
	Status       bool   `json:"status"`
	Content      string `json:"content"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// ---- 匹配 ----

type matchRoomReq struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Mode         string `json:"mode"`
}

type matchRoomResp struct {
	Status       bool   `json:"status"`
	Content      string `json:"content"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	RoomID       string `json:"room_id"`
	ServerID     string `json:"server_id"`
	ServerAddr   string `json:"server_addr"`
	MatchID      string `json:"match_id"`
	RoomToken    string `json:"room_token"`
	ExpireAt     int64  `json:"expire_at"`
}

// ---- 玩家资料 ----

type getPlayerInfoReq struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	PlayerID     uint64 `json:"player_id"`
}

type getPlayerInfoResp struct {
	Status             bool   `json:"status"`
	Content            string `json:"content"`
	AccessToken        string `json:"access_token"`
	RefreshToken       string `json:"refresh_token"`
	PlayerNickname     string `json:"player_nickname"`
	PlayerLevel        string `json:"player_level"`
	PlayerExp          uint64 `json:"player_exp"`
	PlayerID           uint64 `json:"player_id"`
	PlayerCoins        uint64 `json:"player_coins"`
	PlayerProfilePhoto int32  `json:"player_profile_photo_id"`
}

type modifyNicknameReq struct {
	AccessToken     string `json:"access_token"`
	RefreshToken    string `json:"refresh_token"`
	PlayerNickname  string `json:"player_nickname"`
}

type modifyNicknameResp struct {
	Status  bool   `json:"status"`
	Content string `json:"content"`
}

type modifyProfilePhotoReq struct {
	AccessToken        string `json:"access_token"`
	RefreshToken       string `json:"refresh_token"`
	PlayerProfilePhoto int32  `json:"player_profile_photo_id"`
}

type modifyProfilePhotoResp struct {
	Status  bool   `json:"status"`
	Content string `json:"content"`
}

// ---- 商城 ----

type getMallDetailsReq struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type getMallDetailsResp struct {
	Status  bool         `json:"status"`
	Content string       `json:"content"`
	GunList []gunDetails `json:"gun_list"`
}

type buyGunReq struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	GunID        int32  `json:"gun_id"`
}

type buyGunResp struct {
	Status  bool   `json:"status"`
	Content string `json:"content"`
}

// ---- 仓库 / 装备 ----

type getWareDetailsReq struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	PlayerID     uint64 `json:"player_id"`
}

type getWareDetailsResp struct {
	Status  bool         `json:"status"`
	Content string       `json:"content"`
	GunList []gunDetails `json:"gun_list"`
}

type equipGunReq struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	GunID        int32  `json:"gun_id"`
}

type equipGunResp struct {
	Status  bool   `json:"status"`
	Content string `json:"content"`
}

type getEquipGunReq struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	PlayerID     uint64 `json:"player_id"`
}

type getEquipGunResp struct {
	Status  bool   `json:"status"`
	Content string `json:"content"`
	GunID   int32  `json:"gun_id"`
}

// gunDetails 与 proto GunDetails 字段对应
type gunDetails struct {
	ID    int32  `json:"id"`
	Price int64  `json:"price"`
	Name  string `json:"name"`
}
