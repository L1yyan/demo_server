package service

import (
	"context"
	"errors"

	matchpb "demo_server/gen/match"
	"demo_server/pkg/glog"
	"demo_server/src/matchserver/logic"
)

// MatchService matchserver gRPC服务
type MatchService struct {
	matchpb.UnimplementedMatchServiceServer
	matcher *logic.Matcher // 匹配分配器
}

// NewMatchService 创建 matchserver gRPC服务
func NewMatchService(matcher *logic.Matcher) *MatchService {
	return &MatchService{matcher: matcher}
}

// AllocateRoom 分配房间并签发入房令牌
func (s *MatchService) AllocateRoom(ctx context.Context, req *matchpb.AllocateRoomReq) (*matchpb.AllocateRoomResp, error) {
	if s == nil || s.matcher == nil {
		return allocateFailure("server unavailable"), nil
	}
	if req == nil {
		return allocateFailure("invalid player"), nil
	}

	result, err := s.matcher.AllocateRoom(ctx, req.PlayerId, req.Mode)
	if err != nil {
		if errors.Is(err, logic.ErrInvalidPlayer) || errors.Is(err, logic.ErrNoAvailableServer) || errors.Is(err, logic.ErrRoomServerFull) {
			return allocateFailure(err.Error()), nil
		}
		glog.Error(ctx, "allocate room failed", glog.Err(err))
		return allocateFailure("allocate room failed"), nil
	}
	return &matchpb.AllocateRoomResp{
		Status:     true,
		Content:    "allocate room success",
		RoomId:     result.RoomID,
		ServerId:   result.ServerID,
		ServerAddr: result.ServerAddr,
		MatchId:    result.MatchID,
		RoomToken:  result.RoomToken,
		ExpireAt:   result.ExpireAt,
	}, nil
}

// allocateFailure 构造分配失败响应
func allocateFailure(content string) *matchpb.AllocateRoomResp {
	return &matchpb.AllocateRoomResp{Status: false, Content: content}
}
