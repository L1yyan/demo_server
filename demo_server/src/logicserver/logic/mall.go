package logic

import (
	"context"
	jwttool "demo_server/pkg/jwt"
	"demo_server/src/logicserver/consts"
	"demo_server/src/logicserver/repo"
	"errors"
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

// BuyGun 购买武器
func (m *MallLogic) BuyGun(ctx context.Context, token string, gunId int32) error {
	// 解析令牌，expired 为 true 表示令牌已过期
	claims, expired, err := m.jwt.ParseToken(token)
	if err != nil {
		if expired {
			return errors.New("token过期了兄弟")
		}
		return err
	}
	userId := claims.UserID
	// 从商城查询武器完整信息（id/price/name），写入仓库时需要 price 和 name
	gun, err := m.mallRepo.GetGunById(ctx, gunId)
	if err != nil {
		return err
	}
	return m.userRepo.BuyGunToPlayerWare(ctx, userId, gun)
}
