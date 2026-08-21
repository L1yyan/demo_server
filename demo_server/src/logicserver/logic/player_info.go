package logic

import (
	"context"
	jwttool "demo_server/pkg/jwt"
	"demo_server/src/logicserver/consts"
	"demo_server/src/logicserver/repo"
	"errors"
	"fmt"
	"strconv"

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
	claims, ok, err := p.jwt.ParseToken(token)
	if !ok {
		return nilUserWare, fmt.Errorf("token过期了兄弟")
	}
	if err != nil {
		return nilUserWare, err
	}
	tokenPlayerId := claims.Id
	if playerId == 0 {
		playerId, _ = strconv.ParseUint(tokenPlayerId, 10, 64)
	}
	return p.userRepo.GetPlayerWareDetails(ctx, playerId)
}

// func (p *PlayerInfoLogic) GetPlayerEquipGun
// EquipGun  仓库界面装备武器
func (p *PlayerInfoLogic) EquipGun(ctx context.Context, token string, gunId int32) error {
	claims, ok, err := p.jwt.ParseToken(token)
	if !ok {
		return fmt.Errorf("token过期了兄弟")
	}
	if err != nil {
		return err
	}
	playerIdStr := claims.Id
	playerId, _ := strconv.ParseUint(playerIdStr, 10, 64)
	return p.userRepo.SetPlayerEquipGun(ctx, playerId, gunId)
}

// GetEquipGun 获取装备的武器
func (p *PlayerInfoLogic) GetEquipGun(ctx context.Context, playerId uint64) (int32, error) {
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
