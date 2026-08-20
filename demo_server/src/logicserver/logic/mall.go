package logic

import (
	"context"
	jwttool "demo_server/pkg/jwt"
	"demo_server/src/logicserver/consts"
	"demo_server/src/logicserver/repo"
	"errors"
	"strconv"
)

type MallLogic struct {
	mallRepo  *repo.MallRepo
	userRepo  *repo.UserRepo
	tokenRepo *repo.TokenRepo
	jwt       *jwttool.JWT
}

// NewMallLogic 创建商城业务逻辑
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
		mallRepo:  mallRepo,
		userRepo:  userRepo,
		tokenRepo: tokenRepo,
		jwt:       jwt,
	}, nil
}

// GetMallALLDetails 获取商城所有物品信息
func (m *MallLogic) GetMallAllDetails(ctx context.Context) ([]consts.Gun, error) {
	return m.mallRepo.GetMallAllDetails(ctx)
}

// GetGunPriceById 通过武器id获取武器价格
func (m *MallLogic) GetGunPriceById(ctx context.Context, id int32) (int64, error) {
	return m.mallRepo.GetGunPriceById(ctx, id)
}

// BuyGun 购买武器
func (m *MallLogic) BuyGun(ctx context.Context, token string, gunId int32) error {
	claims, ok, err := m.jwt.ParseToken(token)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("token过期了兄弟")
	}
	userIdStr := claims.Id
	userId, _ := strconv.ParseUint(userIdStr, 10, 64)
	price, err := m.mallRepo.GetGunPriceById(ctx, gunId)
	err = m.userRepo.BuyGunToPlayerWare(ctx, userId, gunId, price)
	if err != nil {
		return err
	}
	return nil
}
