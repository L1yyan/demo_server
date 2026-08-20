package logic

import (
	jwttool "demo_server/pkg/jwt"
	"demo_server/src/logicserver/repo"
	"errors"
)

type MallLogic struct {
	mallRepo *repo.MallRepo
	userRepo *repo.UserRepo
	tokenRepo *repo.TokenRepo
	jwt *jwttool.JWT
}

func NewMallLogic(mallRepo *repo.MallRepo, userRepo *repo.UserRepo, tokenRepo *repo.TokenRepo, jwt *jwttool.JWT) (*MallLogic, error) {
	if userRepo == nil {
		return nil, errors.New("userRepo is nil")
	}
	if mallRepo == nil {
		return nil, errors.New("mallRepo is nil")
	}
	if tokenRepo == nil {
		return nil, errors.New("tokenRepo is nil")
	}
	if jwt == nil {
		return nil, errors.New("jwttool is nil")
	}
	return &MallLogic{
		mallRepo: mallRepo,
		userRepo: userRepo,
		tokenRepo: tokenRepo,
		jwt: jwt,
	}, nil
}