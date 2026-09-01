package logic

import (
	"context"
	jwttool "demo_server/pkg/jwt"
	"demo_server/src/logicserver/consts"
	"demo_server/src/logicserver/repo"
	"errors"
	"fmt"

	"github.com/hashicorp/go-multierror"
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
	// 解析令牌，expired 为 true 表示令牌已过期
	claims, expired, err := p.jwt.ParseToken(token)
	if err != nil {
		if expired {
			return errors.New("token过期了兄弟")
		}
		return err
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
	claims, expired, err := p.jwt.ParseToken(token)
	if err != nil {
		if expired {
			return consts.UserInfo{}, errors.New("token过期了兄弟")
		}
		return consts.UserInfo{}, err
	}
	userId := claims.UserID
	if playerId != 0 {
		return p.userRepo.FindByUserID(ctx, playerId)
	}
	return p.userRepo.FindByUserID(ctx, userId)
}

// Modifyplayerprofilephoto 修改玩家头像
func (p *PlayerInfoLogic) ModifyPlayerProfilePhoto(ctx context.Context, token string, photoId int32) error {
	if p == nil || p.userRepo == nil || p.tokens == nil {
		return errors.New("player info logic is nil")
	}
	if token == "" {
		return errors.New("token is empty")
	}
	claims, expired, err := p.jwt.ParseToken(token)
	if err != nil {
		if expired {
			return errors.New("token过期了兄弟")
		}
		return err
	}
	userId := claims.UserID
	_, err = p.userRepo.FindByUserID(ctx, userId)
	if err != nil {
		return err
	}
	return p.userRepo.ModifyProfilePhoto(ctx, userId, photoId)
}

// SettleUpRoom 结算房间
func (p *PlayerInfoLogic) SettleUpRoom(ctx context.Context, playerIds []uint64, playersExp []int64, playersCoin []int64, playersKillCount []int64) error {
	var result *multierror.Error
	for i := 0; i < len(playerIds); i++ {
		err := p.userRepo.AddPlayerExpLevelCoin(ctx, playerIds[i], playersExp[i], playersCoin[i], playersKillCount[i])
		if err != nil {
			err = fmt.Errorf("player %d Settle Up Room failed: %w", playerIds[i], err)
			result = multierror.Append(result, err)
		}
	}
	return result.ErrorOrNil()
}

// GetPlayerWareDetails 获取玩家仓库信息
func (p *PlayerInfoLogic) GetPlayerWareDetails(ctx context.Context, token string, playerId uint64) (consts.UserWare, error) {
	var nilUserWare consts.UserWare
	// 解析令牌，expired 为 true 表示令牌已过期
	claims, expired, err := p.jwt.ParseToken(token)
	if err != nil {
		if expired {
			return nilUserWare, errors.New("token过期了兄弟")
		}
		return nilUserWare, err
	}
	if playerId == 0 {
		playerId = claims.UserID
	}
	return p.userRepo.GetPlayerWareDetails(ctx, playerId)
}

// func (p *PlayerInfoLogic) GetPlayerEquipGun
// EquipGun  仓库界面装备武器
func (p *PlayerInfoLogic) EquipGun(ctx context.Context, token string, gunId int32) error {
	claims, expired, err := p.jwt.ParseToken(token)
	if err != nil {
		if expired {
			return errors.New("token过期了兄弟")
		}
		return err
	}
	return p.userRepo.SetPlayerEquipGun(ctx, claims.UserID, gunId)
}

// GetEquipGun 获取装备的武器。
// playerId 为 0 时视为查询自己，从 token 解析用户ID；非 0 时直接按传入ID查询（供内部服务调用）
func (p *PlayerInfoLogic) GetEquipGun(ctx context.Context, token string, playerId uint64) (int32, error) {
	if p == nil || p.userRepo == nil {
		return 0, errors.New("player info logic is nil")
	}
	// playerId 为 0 时从 token 解析自身ID
	if playerId == 0 {
		if token == "" {
			return 0, errors.New("token is empty")
		}
		claims, expired, err := p.jwt.ParseToken(token)
		if err != nil {
			if expired {
				return 0, errors.New("token过期了兄弟")
			}
			return 0, err
		}
		playerId = claims.UserID
	}
	return p.userRepo.GetPlayerEquipGunId(ctx, playerId)
}

// //SetPlayerEquipGun 设置玩家装备的武器
// func (r *UserRepo) SetPlayerEquipGun(ctx context.Context, userID uint64, gunID int32) error {
// 	if err := r.validate(); err != nil {
// 		return err
// 	}
// 	result, err := r.collection.UpdateOne(ctx, bson.M{"user_id": userID}, bson.M{"$set":bson.M{"equip_gun": gunID}})
// 	if err != nil {
// 		return err
// 	}

// 	if result.MatchedCount == 0 {
// 		return errors.New("update fail")
// 	}
// 	return nil
// }
