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
	auth *logic.AuthLogic // 认证业务逻辑
}

// NewLogicService 创建 logicserver gRPC服务
func NewLogicService(auth *logic.AuthLogic) *LogicService {
	return &LogicService{auth: auth}
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
