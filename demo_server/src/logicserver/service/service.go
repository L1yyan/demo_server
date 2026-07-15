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
	auth  *logic.AuthLogic  // 认证业务逻辑
	match *logic.MatchLogic // 匹配业务逻辑
}

// NewLogicService 创建 logicserver gRPC服务
func NewLogicService(auth *logic.AuthLogic, match *logic.MatchLogic) *LogicService {
	return &LogicService{auth: auth, match: match}
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

// Register 处理注册请求，当前暂未实现
func (s *LogicService) Register(ctx context.Context, req *logicpb.RegisterReq) (*logicpb.AuthResp, error) {
	return authFailure("register not implemented"), nil
}

// SendVerifyCode 处理验证码发送请求，当前暂未实现
func (s *LogicService) SendVerifyCode(ctx context.Context, req *logicpb.SendVerifyCodeReq) (*logicpb.SendVerifyCodeResp, error) {
	return &logicpb.SendVerifyCodeResp{Status: false, Content: "send verify code not implemented"}, nil
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

// authSuccess 构造认证成功响应
func authSuccess(content string, result *logic.LoginResult) *logicpb.AuthResp {
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
