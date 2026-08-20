package service

import (
	"context"
	"errors"

	logicpb "demo_server/gen/logic"
	"demo_server/pkg/glog"
	"demo_server/src/logicserver/logic"
)

// LogicService logicserver gRPC服务
type LogicService struct {
	logicpb.UnimplementedLogicServiceServer
	auth       *logic.AuthLogic       // 认证业务逻辑
	match      *logic.MatchLogic      // 匹配业务逻辑
	playerInfo *logic.PlayerInfoLogic // 玩家信息业务逻辑
	mall       *logic.MallLogic       //商店逻辑
}

// NewLogicService 创建 logicserver gRPC服务
func NewLogicService(auth *logic.AuthLogic, match *logic.MatchLogic, playerInfo *logic.PlayerInfoLogic, mall *logic.MallLogic) *LogicService {
	return &LogicService{auth: auth, match: match, playerInfo: playerInfo, mall: mall}
}

// Login 处理邮箱密码登录
func (s *LogicService) Login(ctx context.Context, req *logicpb.LoginReq) (*logicpb.AuthResp, error) {
	if s == nil || s.auth == nil {
		return authFailure("server unavailable"), nil
	}
	if req == nil {
		return authFailure("invalid login params"), nil
	}

	result, err := s.auth.Login(ctx, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, logic.ErrInvalidLoginParams) || errors.Is(err, logic.ErrInvalidCredential) {
			return authFailure(err.Error()), nil
		}
		glog.Error(ctx, "login failed", glog.Err(err))
		return authFailure("login failed"), nil
	}
	return authSuccess("login success", result), nil
}

// Register 处理邮箱密码注册
func (s *LogicService) Register(ctx context.Context, req *logicpb.RegisterReq) (*logicpb.AuthResp, error) {
	if s == nil || s.auth == nil {
		return authFailure("server unavailable"), nil
	}
	if req == nil {
		return authFailure("invalid login params"), nil
	}

	result, err := s.auth.Register(ctx, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, logic.ErrInvalidLoginParams) || errors.Is(err, logic.ErrAccountAlreadyExist) {
			return authFailure(err.Error()), nil
		}
		glog.Error(ctx, "register failed", glog.Err(err))
		return authFailure("register failed"), nil
	}
	return authSuccess("register success", result), nil
}

// SendVerifyCode 保留兼容，邮箱密码注册不再发送验证码
func (s *LogicService) SendVerifyCode(ctx context.Context, req *logicpb.SendVerifyCodeReq) (*logicpb.SendVerifyCodeResp, error) {
	return &logicpb.SendVerifyCodeResp{Status: false, Content: "verify code disabled"}, nil
}

// VerifyToken 校验登录 token
func (s *LogicService) VerifyToken(ctx context.Context, req *logicpb.VerifyTokenReq) (*logicpb.AuthResp, error) {
	if s == nil || s.auth == nil {
		return authFailure("server unavailable"), nil
	}
	if req == nil {
		return authFailure("unauthorized"), nil
	}

	result, err := s.auth.VerifyToken(ctx, req.AccessToken, req.RefreshToken)
	if err != nil {
		if errors.Is(err, logic.ErrUnauthorized) {
			return authFailure("unauthorized"), nil
		}
		glog.Error(ctx, "verify token failed", glog.Err(err))
		return authFailure("verify token failed"), nil
	}
	return authSuccess("token verified", result), nil
}

// ModifyPlayerNickname 修改玩家昵称
func (s *LogicService) ModifyPlayerNickname(ctx context.Context, req *logicpb.ModifyPlayerNicknameReq) (*logicpb.ModifyPlayerNicknameResp, error) {
	var resp logicpb.ModifyPlayerNicknameResp
	if s == nil || s.playerInfo == nil {
		resp.Status = false
		resp.Content = "server unavailable"
		return &resp, nil
	}
	if req == nil {
		resp.Status = false
		resp.Content = "invalid request"
		return &resp, nil
	}

	err := s.playerInfo.ModifyPlayerNickname(ctx, req.AccessToken, req.Player_Nickname)
	if err != nil {
		resp.Status = false
		resp.Content = err.Error()
		return &resp, nil
	}
	resp.Status = true
	resp.Content = "nickname modified successfully"
	return &resp, nil
}

// GetPlayerInfo 获取玩家信息
func (s *LogicService) GetPlayerInfo(ctx context.Context, req *logicpb.GetPlayerInfoReq) (*logicpb.GetPlayerInfoResp, error) {
	var resp logicpb.GetPlayerInfoResp
	if s == nil || s.playerInfo == nil {
		resp.Status = false
		resp.Content = "server unavailable"
		return &resp, nil
	}
	if req == nil {
		resp.Status = false
		resp.Content = "invalid request"
		return &resp, nil
	}
	userInfo, err := s.playerInfo.GetPlayerInfo(ctx, req.AccessToken, req.PlayerId)
	if err != nil {
		resp.Status = false
		resp.Content = err.Error()
		return &resp, nil
	}
	resp.Status = true
	resp.Content = "player info retrieved successfully"
	resp.PlayerId = userInfo.UserID
	resp.PlayerExp = uint64(userInfo.Exp)
	resp.PlayerLevel = userInfo.Level
	resp.Player_Nickname = userInfo.Nickname
	resp.PlayerCoins = uint64(userInfo.Coins)
	resp.PlayerProfilePhotoId = userInfo.ProfilePhotoID
	return &resp, nil
}

// ModifyPlayerProfilePhoto 修改玩家头像
func (s *LogicService) ModifyPlayerProfilePhoto(ctx context.Context, req *logicpb.ModifyPlayerProfilePhotoReq) (*logicpb.ModifyPlayerProfilePhotoResp, error) {
	var resp logicpb.ModifyPlayerProfilePhotoResp
	if s == nil || s.playerInfo == nil {
		resp.Status = false
		resp.Content = "server unavailable"
		return &resp, nil
	}
	if req == nil {
		resp.Status = false
		resp.Content = "invalid request"
		return &resp, nil
	}

	err := s.playerInfo.ModifyPlayerProfilePhoto(ctx, req.AccessToken, req.PlayerProfilePhotoId)
	if err != nil {
		resp.Status = false
		resp.Content = err.Error()
		return &resp, nil
	}
	resp.Status = true
	resp.Content = "profile photo modified successfully"
	return &resp, nil
}

// MatchRoom 处理客户端匹配请求
func (s *LogicService) MatchRoom(ctx context.Context, req *logicpb.MatchRoomReq) (*logicpb.MatchRoomResp, error) {
	if s == nil || s.match == nil {
		return matchFailure("server unavailable"), nil
	}
	if req == nil {
		return matchFailure("unauthorized"), nil
	}

	result, err := s.match.MatchRoom(ctx, req.AccessToken, req.RefreshToken, req.Mode)
	if err != nil {
		if errors.Is(err, logic.ErrUnauthorized) {
			return matchFailure("unauthorized"), nil
		}
		if errors.Is(err, logic.ErrMatchUnavailable) {
			glog.Error(ctx, "match server unavailable", glog.Err(err))
			return matchFailure("match server unavailable"), nil
		}
		return matchFailure(err.Error()), nil
	}
	return &logicpb.MatchRoomResp{
		Status:       true,
		Content:      "match room success",
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		RoomId:       result.RoomID,
		ServerId:     result.ServerID,
		ServerAddr:   result.ServerAddr,
		MatchId:      result.MatchID,
		RoomToken:    result.RoomToken,
		ExpireAt:     result.ExpireAt,
	}, nil
}

// SettleUpRoom 结算房间 req里的expcoin的数组索引对应的是相同索引序号的playerids数组里的玩家收益
func (s *LogicService) SettleUpRoom(ctx context.Context, req *logicpb.SettleUpGameRewardAndKdReq) (*logicpb.SettleUpGameRewardAndKdResp, error) {
	var resp logicpb.SettleUpGameRewardAndKdResp
	resp.Status = false
	if s == nil || s.playerInfo == nil {
		resp.Content = "server unavailable"
		return &resp, nil
	}
	if req == nil {
		resp.Content = "req is nil"
		return &resp, nil
	}
	s.playerInfo.SettleUpRoom(ctx, req.PlayerIds, req.Exp, req.Coin, req.KillCount)
	resp.Status = true
	return &resp, nil
}

// GetMallDetails 获取商店列表信息
func (s *LogicService) GetMallDetails(ctx context.Context, req *logicpb.GetMallDetailsReq) (*logicpb.GetMallDetailsResp, error) {
	var resp logicpb.GetMallDetailsResp
	resp.Status = false
	if s == nil || s.mall == nil {
		resp.Content = "server unavailable"
		return &resp, nil
	}
	if req == nil {
		resp.Content = "req is nil"
		return &resp, nil
	}
	gunList, err := s.mall.GetMallAllDetails(ctx)
	if err != nil {
		resp.Content = "GetmallAllDetails error"
		return &resp, err
	}
	var list []*logicpb.GunDetails
	for _, gun := range gunList {
		list = append(list, &logicpb.GunDetails{Id: gun.Id, Price: gun.Price, Name: gun.Name})
	}
	resp.GunList = list
	resp.Status = true
	return &resp, nil
}

// BuyGun 买东西
func (s *LogicService) BuyGun(ctx context.Context, req *logicpb.BuyGunReq) (*logicpb.BuyGunResp, error) {
	var resp logicpb.BuyGunResp
	resp.Status = false
	if s == nil || s.mall == nil {
		resp.Content = "server unavailable"
		return &resp, nil
	}
	if req == nil {
		resp.Content = "req is nil"
		return &resp, nil
	}
	err := s.mall.BuyGun(ctx, req.AccessToken, req.GunId)
	if err != nil {
		resp.Content = "Buy Gun Fail"
		return &resp, err
	}
	resp.Status = true
	resp.Content = "ok"
	return &resp, nil
}

//GetPlayerWareDetails 获取玩家仓库信息 
func (s *LogicService) GetPlayerWareDetails(ctx context.Context, req *logicpb.GetPlayerWareDetailsReq) (*logicpb.GetPlayerWareDetailsResp, error) {
	var resp logicpb.GetPlayerWareDetailsResp
	resp.Status = false
	if s == nil || s.playerInfo == nil {
		resp.Content = "server unvailable"
		return &resp, nil
	}
	if req == nil {
		resp.Content = "req is nil"
		return &resp, nil
	}
	wareDetails, err := s.playerInfo.GetPlayerWareDetails(ctx, req.AccessToken, req.PlayerId)
	if err != nil {
		return &resp, err
	}
	resp.GunList = make([]*logicpb.GunDetails, 0)
	for _, gun := range wareDetails.Gun {
		resp.GunList = append(resp.GunList, &logicpb.GunDetails{Id: gun.Id, Price: gun.Price, Name: gun.Name})
	}
	resp.Status = true
	resp.Content = "ok"
	return &resp, nil
}
 
// authSuccess 构造认证成功响应
func authSuccess(content string, result *logic.LoginRegisterResult) *logicpb.AuthResp {
	resp := &logicpb.AuthResp{Status: true, Content: content}
	if result != nil {
		resp.AccessToken = result.AccessToken
		resp.RefreshToken = result.RefreshToken
	}
	return resp
}

// authFailure 构造认证失败响应
func authFailure(content string) *logicpb.AuthResp {
	return &logicpb.AuthResp{Status: false, Content: content}
}

// matchFailure 构造匹配失败响应
func matchFailure(content string) *logicpb.MatchRoomResp {
	return &logicpb.MatchRoomResp{Status: false, Content: content}
}
