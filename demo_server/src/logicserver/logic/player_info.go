package logic

import (
	"context"
	jwttool "demo_server/pkg/jwt"
	"demo_server/src/logicserver/consts"
	"demo_server/src/logicserver/repo"
	"errors"
)

type PlayerInfoLogic struct {
	userRepo *repo.UserRepo
	tokens   *repo.TokenRepo // token仓库
	jwt      *jwttool.JWT    // JWT工具
}

func NewPlayerInfoLogic(users *repo.UserRepo, tokens *repo.TokenRepo, jwt *jwttool.JWT) (*PlayerInfoLogic, error) {
	if users == nil {
		return nil, errors.New("user repo is nil")
	}
	if tokens == nil {
		return nil, errors.New("token repo is nil")
	}
	if jwt == nil {
		return nil, errors.New("JWT tool is nil")
	}
	return &PlayerInfoLogic{userRepo: users, tokens: tokens, jwt: jwt}, nil
}

func (p *PlayerInfoLogic) ModifyPlayerNickname(ctx context.Context, token string, nickname string) error {
	if p == nil || p.userRepo == nil || p.tokens == nil {
		return errors.New("player info logic is nil")
	}
	if token == "" {
		return errors.New("token is empty")
	}
	if nickname == "" {
		return errors.New("nickname is empty")
	}
	claims, ok, err := p.jwt.ParseToken(token)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("invalid token")
	}
	userId := claims.UserID
	_, err = p.userRepo.FindByUserID(ctx, userId)
	if err != nil {
		return err
	}
	return p.userRepo.ModifyNickname(ctx, userId, nickname)
}

func (p *PlayerInfoLogic) GetPlayerInfo(ctx context.Context, token string, playerId uint64) (consts.UserInfo, error) {
	if p == nil || p.userRepo == nil || p.tokens == nil {
		return consts.UserInfo{}, errors.New("player info logic is nil")
	}
	if token == "" {
		return consts.UserInfo{}, errors.New("token is empty")
	}
	claims, ok, err := p.jwt.ParseToken(token)
	if err != nil {
		return consts.UserInfo{}, err
	}
	if !ok {
		return consts.UserInfo{}, errors.New("invalid token")
	}
	userId := claims.UserID
	if playerId != 0 {
		return p.userRepo.FindByUserID(ctx, playerId)
	} else {
		return p.userRepo.FindByUserID(ctx, userId)
	}
}

// Modifyplayerprofilephoto 修改玩家头像
func (p *PlayerInfoLogic) ModifyPlayerProfilePhoto(ctx context.Context, token string, photoId int32) error {
	if p == nil || p.userRepo == nil || p.tokens == nil {
		return errors.New("player info logic is nil")
	}
	if token == "" {
		return errors.New("token is empty")
	}
	claims, ok, err := p.jwt.ParseToken(token)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("invalid token")
	}
	userId := claims.UserID
	_, err = p.userRepo.FindByUserID(ctx, userId)
	if err != nil {
		return err
	}
	return p.userRepo.ModifyProfilePhoto(ctx, userId, photoId)
}
