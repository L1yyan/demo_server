package logic

import (
	"context"
	"errors"
	"strings"

	matchpb "demo_server/gen/match"
)

var ErrMatchUnavailable = errors.New("match server unavailable")

// MatchLogic 匹配入口业务逻辑
type MatchLogic struct {
	auth        *AuthLogic                 // 认证业务逻辑
	matchClient matchpb.MatchServiceClient // matchserver客户端
}

// MatchRoomResult 匹配房间结果
type MatchRoomResult struct {
	AccessToken  string // 当前可用短token
	RefreshToken string // 长token
	RoomID       string // 房间ID
	ServerID     string // roomserver ID
	ServerAddr   string // roomserver连接地址
	MatchID      string // 匹配ID
	RoomToken    string // 入房令牌
	ExpireAt     int64  // 过期时间戳，毫秒
}

// NewMatchLogic 创建匹配入口业务逻辑
func NewMatchLogic(auth *AuthLogic, matchClient matchpb.MatchServiceClient) (*MatchLogic, error) {
	if auth == nil {
		return nil, errors.New("auth logic is nil")
	}
	if matchClient == nil {
		return nil, errors.New("match client is nil")
	}
	return &MatchLogic{auth: auth, matchClient: matchClient}, nil
}

// MatchRoom 校验登录态后请求 matchserver 分配房间
func (l *MatchLogic) MatchRoom(ctx context.Context, accessToken string, refreshToken string, mode string) (*MatchRoomResult, error) {
	if l == nil {
		return nil, errors.New("match logic is nil")
	}
	accessToken = strings.TrimSpace(accessToken)
	refreshToken = strings.TrimSpace(refreshToken)
	mode = strings.TrimSpace(mode)

	// 用户必须先通过 logicserver 登录态校验，matchserver 不直接面向客户端鉴权
	authResult, err := l.auth.VerifyToken(ctx, accessToken, refreshToken)
	if err != nil {
		return nil, err
	}
	if authResult.UserID == 0 {
		return nil, ErrUnauthorized
	}

	resp, err := l.matchClient.AllocateRoom(ctx, &matchpb.AllocateRoomReq{PlayerId: authResult.UserID, Mode: mode})
	if err != nil {
		return nil, ErrMatchUnavailable
	}
	if resp == nil {
		return nil, ErrMatchUnavailable
	}
	if !resp.Status {
		return nil, errors.New(resp.Content)
	}
	if resp.RoomToken == "" || resp.ServerAddr == "" || resp.RoomId == "" || resp.ServerId == "" {
		return nil, errors.New("match failed")
	}

	return &MatchRoomResult{
		AccessToken:  authResult.AccessToken,
		RefreshToken: authResult.RefreshToken,
		RoomID:       resp.RoomId,
		ServerID:     resp.ServerId,
		ServerAddr:   resp.ServerAddr,
		MatchID:      resp.MatchId,
		RoomToken:    resp.RoomToken,
		ExpireAt:     resp.ExpireAt,
	}, nil
}
