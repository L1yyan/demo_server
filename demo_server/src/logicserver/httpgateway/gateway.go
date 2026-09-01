package httpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"demo_server/pkg/glog"
	"demo_server/src/logicserver/logic"
)

// maxRequestBodySize 请求体大小上限，防止异常客户端撑爆内存
const maxRequestBodySize = 64 * 1024

// Gateway logicserver HTTP/JSON 网关，复用 service 层相同的 logic 对象，
// 对外暴露等价于 LogicService 全部 unary 方法的 HTTP 接口。
type Gateway struct {
	auth       *logic.AuthLogic       // 认证业务逻辑
	match      *logic.MatchLogic      // 匹配业务逻辑
	playerInfo *logic.PlayerInfoLogic // 玩家信息业务逻辑
	mall       *logic.MallLogic       // 商城业务逻辑

	timeout time.Duration // 单请求业务处理超时
}

// New 创建 HTTP 网关
func New(auth *logic.AuthLogic, match *logic.MatchLogic, playerInfo *logic.PlayerInfoLogic, mall *logic.MallLogic, timeout time.Duration) *Gateway {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Gateway{
		auth:       auth,
		match:      match,
		playerInfo: playerInfo,
		mall:       mall,
		timeout:    timeout,
	}
}

// Handler 返回注册了全部路由的 http.Handler
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/login", g.handle(g.login))
	mux.HandleFunc("/v1/register", g.handle(g.register))
	mux.HandleFunc("/v1/verify_token", g.handle(g.verifyToken))
	mux.HandleFunc("/v1/match_room", g.handle(g.matchRoom))
	mux.HandleFunc("/v1/get_player_info", g.handle(g.getPlayerInfo))
	mux.HandleFunc("/v1/modify_player_nickname", g.handle(g.modifyPlayerNickname))
	mux.HandleFunc("/v1/modify_player_profile_photo", g.handle(g.modifyPlayerProfilePhoto))
	mux.HandleFunc("/v1/get_mall_details", g.handle(g.getMallDetails))
	mux.HandleFunc("/v1/buy_gun", g.handle(g.buyGun))
	mux.HandleFunc("/v1/equip_gun", g.handle(g.equipGun))
	mux.HandleFunc("/v1/get_equip_gun", g.handle(g.getEquipGun))
	mux.HandleFunc("/v1/get_player_ware_details", g.handle(g.getPlayerWareDetails))
	return mux
}

// handler 统一闭包：解析 body、注入超时上下文、执行业务、写 JSON、捕获 panic。
// 业务响应统一写入 ResponseWriter 的 JSON；这里只负责传输层错误（无 body / 非法 JSON / 方法 != POST）。
type handlerFunc func(ctx context.Context, w http.ResponseWriter, data []byte)

func (g *Gateway) handle(handler handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodySize))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), g.timeout)
		defer cancel()

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		handler(ctx, w, body)
	}
}

// ---- 认证 ----

func (g *Gateway) login(ctx context.Context, w http.ResponseWriter, body []byte) {
	var req loginReq
	if err := unmarshal(body, &req); err != nil {
		writeJSON(w, authResp{Status: false, Content: "invalid login params"})
		return
	}

	if g.auth == nil {
		writeJSON(w, authResp{Status: false, Content: "server unavailable"})
		return
	}

	result, err := g.auth.Login(ctx, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, logic.ErrInvalidLoginParams) || errors.Is(err, logic.ErrInvalidCredential) {
			writeJSON(w, authResp{Status: false, Content: err.Error()})
			return
		}
		glog.Error(ctx, "http login failed", glog.Err(err))
		writeJSON(w, authResp{Status: false, Content: "login failed"})
		return
	}
	writeJSON(w, authSuccess(result))
}

func (g *Gateway) register(ctx context.Context, w http.ResponseWriter, body []byte) {
	var req registerReq
	if err := unmarshal(body, &req); err != nil {
		writeJSON(w, authResp{Status: false, Content: "invalid login params"})
		return
	}

	if g.auth == nil {
		writeJSON(w, authResp{Status: false, Content: "server unavailable"})
		return
	}

	result, err := g.auth.Register(ctx, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, logic.ErrInvalidLoginParams) || errors.Is(err, logic.ErrAccountAlreadyExist) {
			writeJSON(w, authResp{Status: false, Content: err.Error()})
			return
		}
		glog.Error(ctx, "http register failed", glog.Err(err))
		writeJSON(w, authResp{Status: false, Content: "register failed"})
		return
	}
	writeJSON(w, authSuccess(result))
}

func (g *Gateway) verifyToken(ctx context.Context, w http.ResponseWriter, body []byte) {
	var req verifyTokenReq
	if err := unmarshal(body, &req); err != nil {
		writeJSON(w, authResp{Status: false, Content: "unauthorized"})
		return
	}

	if g.auth == nil {
		writeJSON(w, authResp{Status: false, Content: "server unavailable"})
		return
	}

	result, err := g.auth.VerifyToken(ctx, req.AccessToken, req.RefreshToken)
	if err != nil {
		if errors.Is(err, logic.ErrUnauthorized) {
			writeJSON(w, authResp{Status: false, Content: "unauthorized"})
			return
		}
		glog.Error(ctx, "http verify token failed", glog.Err(err))
		writeJSON(w, authResp{Status: false, Content: "verify token failed"})
		return
	}
	writeJSON(w, authSuccess(result))
}

// ---- 匹配 ----

func (g *Gateway) matchRoom(ctx context.Context, w http.ResponseWriter, body []byte) {
	var req matchRoomReq
	if err := unmarshal(body, &req); err != nil {
		writeJSON(w, matchRoomResp{Status: false, Content: "unauthorized"})
		return
	}

	if g.match == nil {
		writeJSON(w, matchRoomResp{Status: false, Content: "server unavailable"})
		return
	}

	result, err := g.match.MatchRoom(ctx, req.AccessToken, req.RefreshToken, req.Mode)
	if err != nil {
		if errors.Is(err, logic.ErrUnauthorized) {
			writeJSON(w, matchRoomResp{Status: false, Content: "unauthorized"})
			return
		}
		if errors.Is(err, logic.ErrMatchUnavailable) {
			glog.Error(ctx, "http match server unavailable", glog.Err(err))
			writeJSON(w, matchRoomResp{Status: false, Content: "match server unavailable"})
			return
		}
		writeJSON(w, matchRoomResp{Status: false, Content: err.Error()})
		return
	}

	writeJSON(w, matchRoomResp{
		Status:       true,
		Content:      "match room success",
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		RoomID:       result.RoomID,
		ServerID:     result.ServerID,
		ServerAddr:   result.ServerAddr,
		MatchID:      result.MatchID,
		RoomToken:    result.RoomToken,
		ExpireAt:     result.ExpireAt,
	})
}

// ---- 玩家资料 ----

func (g *Gateway) getPlayerInfo(ctx context.Context, w http.ResponseWriter, body []byte) {
	var req getPlayerInfoReq
	if err := unmarshal(body, &req); err != nil {
		writeJSON(w, getPlayerInfoResp{Status: false, Content: "invalid request"})
		return
	}

	resp := getPlayerInfoResp{Status: false, Content: "server unavailable"}
	if g.playerInfo == nil {
		writeJSON(w, resp)
		return
	}

	info, err := g.playerInfo.GetPlayerInfo(ctx, req.AccessToken, req.PlayerID)
	if err != nil {
		resp.Content = err.Error()
		writeJSON(w, resp)
		return
	}

	resp.Status = true
	resp.Content = "player info retrieved successfully"
	resp.PlayerID = info.UserID
	resp.PlayerExp = uint64(info.Exp)
	resp.PlayerLevel = strconv.FormatInt(info.Level, 10)
	resp.PlayerNickname = info.Nickname
	resp.PlayerCoins = uint64(info.Coins)
	resp.PlayerProfilePhoto = info.ProfilePhotoID
	writeJSON(w, resp)
}

func (g *Gateway) modifyPlayerNickname(ctx context.Context, w http.ResponseWriter, body []byte) {
	var req modifyNicknameReq
	if err := unmarshal(body, &req); err != nil {
		writeJSON(w, modifyNicknameResp{Status: false, Content: "invalid request"})
		return
	}

	resp := modifyNicknameResp{Status: false, Content: "server unavailable"}
	if g.playerInfo == nil {
		writeJSON(w, resp)
		return
	}

	if err := g.playerInfo.ModifyPlayerNickname(ctx, req.AccessToken, req.PlayerNickname); err != nil {
		resp.Content = err.Error()
		writeJSON(w, resp)
		return
	}
	resp.Status = true
	resp.Content = "nickname modified successfully"
	writeJSON(w, resp)
}

func (g *Gateway) modifyPlayerProfilePhoto(ctx context.Context, w http.ResponseWriter, body []byte) {
	var req modifyProfilePhotoReq
	if err := unmarshal(body, &req); err != nil {
		writeJSON(w, modifyProfilePhotoResp{Status: false, Content: "invalid request"})
		return
	}

	resp := modifyProfilePhotoResp{Status: false, Content: "server unavailable"}
	if g.playerInfo == nil {
		writeJSON(w, resp)
		return
	}

	if err := g.playerInfo.ModifyPlayerProfilePhoto(ctx, req.AccessToken, req.PlayerProfilePhoto); err != nil {
		resp.Content = err.Error()
		writeJSON(w, resp)
		return
	}
	resp.Status = true
	resp.Content = "profile photo modified successfully"
	writeJSON(w, resp)
}

// ---- 商城 ----

func (g *Gateway) getMallDetails(ctx context.Context, w http.ResponseWriter, body []byte) {
	resp := getMallDetailsResp{Status: false, Content: "mall unavailable"}
	if g.mall == nil {
		writeJSON(w, resp)
		return
	}

	gunList, err := g.mall.GetMallAllDetails(ctx)
	if err != nil {
		glog.Error(ctx, "http get mall details failed", glog.Err(err))
		resp.Content = "mall unavailable"
		writeJSON(w, resp)
		return
	}
	resp.Status = true
	resp.GunList = make([]gunDetails, 0, len(gunList))
	for _, gun := range gunList {
		resp.GunList = append(resp.GunList, gunDetails{ID: gun.Id, Price: gun.Price, Name: gun.Name})
	}
	writeJSON(w, resp)
}

func (g *Gateway) buyGun(ctx context.Context, w http.ResponseWriter, body []byte) {
	var req buyGunReq
	if err := unmarshal(body, &req); err != nil {
		writeJSON(w, buyGunResp{Status: false, Content: "invalid request"})
		return
	}

	resp := buyGunResp{Status: false, Content: "server unavailable"}
	if g.mall == nil {
		writeJSON(w, resp)
		return
	}

	if err := g.mall.BuyGun(ctx, req.AccessToken, req.GunID); err != nil {
		glog.Error(ctx, "http buy gun failed", glog.Int("gun_id", int(req.GunID)), glog.Err(err))
		resp.Content = classifyBuyGunError(err)
		writeJSON(w, resp)
		return
	}
	resp.Status = true
	resp.Content = "ok"
	writeJSON(w, resp)
}

func classifyBuyGunError(err error) string {
	if err == nil {
		return "buy gun failed"
	}

	message := strings.TrimSpace(err.Error())
	switch message {
	case "token过期了兄弟":
		return "unauthorized"
	case "gun not found":
		return "invalid gun"
	case "金币不足":
		return "not enough coins"
	case "已拥有该武器":
		return "gun already owned"
	}

	// Mongo/用户数据等内部错误不直接暴露给客户端，避免泄露数据库细节。
	return "buy gun failed"
}

// ---- 仓库 / 装备 ----

func (g *Gateway) getPlayerWareDetails(ctx context.Context, w http.ResponseWriter, body []byte) {
	var req getWareDetailsReq
	if err := unmarshal(body, &req); err != nil {
		writeJSON(w, getWareDetailsResp{Status: false, Content: "invalid request"})
		return
	}

	resp := getWareDetailsResp{Status: false, Content: "server unvailable"}
	if g.playerInfo == nil {
		writeJSON(w, resp)
		return
	}

	ware, err := g.playerInfo.GetPlayerWareDetails(ctx, req.AccessToken, req.PlayerID)
	if err != nil {
		resp.Content = err.Error()
		writeJSON(w, resp)
		return
	}
	resp.Status = true
	resp.Content = "ok"
	resp.GunList = make([]gunDetails, 0, len(ware.Gun))
	for _, gun := range ware.Gun {
		resp.GunList = append(resp.GunList, gunDetails{ID: gun.Id, Price: gun.Price, Name: gun.Name})
	}
	writeJSON(w, resp)
}

func (g *Gateway) equipGun(ctx context.Context, w http.ResponseWriter, body []byte) {
	var req equipGunReq
	if err := unmarshal(body, &req); err != nil {
		writeJSON(w, equipGunResp{Status: false, Content: "invalid request"})
		return
	}

	resp := equipGunResp{Status: false, Content: "server unvailable"}
	if g.playerInfo == nil {
		writeJSON(w, resp)
		return
	}

	if err := g.playerInfo.EquipGun(ctx, req.AccessToken, req.GunID); err != nil {
		resp.Content = "equip fail"
		writeJSON(w, resp)
		return
	}
	resp.Status = true
	resp.Content = "ok"
	writeJSON(w, resp)
}

func (g *Gateway) getEquipGun(ctx context.Context, w http.ResponseWriter, body []byte) {
	var req getEquipGunReq
	if err := unmarshal(body, &req); err != nil {
		writeJSON(w, getEquipGunResp{Status: false, Content: "invalid request"})
		return
	}

	resp := getEquipGunResp{Status: false, Content: "server unvailable"}
	if g.playerInfo == nil {
		writeJSON(w, resp)
		return
	}

	gunID, err := g.playerInfo.GetEquipGun(ctx, req.AccessToken, req.PlayerID)
	if err != nil {
		resp.Content = "equip fail"
		writeJSON(w, resp)
		return
	}
	resp.Status = true
	resp.Content = "ok"
	resp.GunID = gunID
	writeJSON(w, resp)
}

// ---- 公共 ----

func authSuccess(result *logic.LoginRegisterResult) authResp {
	resp := authResp{Status: true, Content: "login success"}
	if result != nil {
		resp.AccessToken = result.AccessToken
		resp.RefreshToken = result.RefreshToken
	}
	return resp
}

// unmarshal 解析 JSON body。body 为空时不报错（各 handler 自行判空兜底）。
func unmarshal(body []byte, target interface{}) error {
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, target)
}

// writeJSON 写业务响应 JSON
func writeJSON(w http.ResponseWriter, payload interface{}) {
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		glog.Error(context.Background(), "http write json failed", glog.Err(err))
	}
}

// writeError 写传输层错误（非业务响应），内容仅用于调试，客户端不应依赖展示
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": false, "content": message})
}
