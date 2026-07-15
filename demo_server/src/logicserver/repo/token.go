package repo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	redisx "demo_server/pkg/redis"

	"github.com/redis/go-redis/v9"
)

const (
	accessTokenKeyPrefix  = "logic:auth:access:"  // 短token Redis key前缀
	refreshTokenKeyPrefix = "logic:auth:refresh:" // 长token Redis key前缀
	userTokenKeyPrefix    = "logic:auth:user:"    // 用户token Redis key前缀
)

var ErrTokenNotFound = errors.New("token not found")

// TokenSession 登录态数据
type TokenSession struct {
	UserID      uint64 // 用户ID
	Email       string // 邮箱
	AccessHash  string // 短token哈希
	RefreshHash string // 长token哈希
}

// TokenRepo 登录令牌仓库
type TokenRepo struct {
	client *redis.Client // Redis客户端
}

// NewTokenRepo 创建登录令牌仓库
func NewTokenRepo(client *redis.Client) (*TokenRepo, error) {
	if client == nil {
		return nil, errors.New("redis client is nil")
	}
	return &TokenRepo{client: client}, nil
}

// SaveLoginTokens 保存登录生成的短令牌和长令牌
func (r *TokenRepo) SaveLoginTokens(ctx context.Context, userID uint64, email string, accessToken string, refreshToken string, accessTTL time.Duration, refreshTTL time.Duration) error {
	if err := r.validate(); err != nil {
		return err
	}
	if userID == 0 || strings.TrimSpace(email) == "" {
		return errors.New("token session user is empty")
	}
	if accessToken == "" || refreshToken == "" {
		return errors.New("token is empty")
	}

	accessHash := TokenHash(accessToken)
	refreshHash := TokenHash(refreshToken)
	accessKey := accessTokenKey(accessHash)
	refreshKey := refreshTokenKey(refreshHash)
	userKey := userTokenKey(userID)

	accessValues := map[string]any{
		"user_id":      strconv.FormatUint(userID, 10),
		"email":        email,
		"refresh_hash": refreshHash,
	}
	refreshValues := map[string]any{
		"user_id": strconv.FormatUint(userID, 10),
		"email":   email,
	}
	userValues := map[string]any{
		"access_hash":  accessHash,
		"refresh_hash": refreshHash,
		"email":        email,
	}

	pipe := r.client.TxPipeline()
	pipe.HSet(ctx, accessKey, accessValues)
	pipe.Expire(ctx, accessKey, accessTTL)
	pipe.HSet(ctx, refreshKey, refreshValues)
	pipe.Expire(ctx, refreshKey, refreshTTL)
	pipe.HSet(ctx, userKey, userValues)
	pipe.Expire(ctx, userKey, refreshTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("save login tokens: %w", err)
	}
	return nil
}

// SaveAccessToken 保存刷新后的短令牌
func (r *TokenRepo) SaveAccessToken(ctx context.Context, userID uint64, email string, accessToken string, refreshToken string, accessTTL time.Duration) error {
	if err := r.validate(); err != nil {
		return err
	}
	if userID == 0 || strings.TrimSpace(email) == "" {
		return errors.New("token session user is empty")
	}
	if accessToken == "" || refreshToken == "" {
		return errors.New("token is empty")
	}

	accessHash := TokenHash(accessToken)
	refreshHash := TokenHash(refreshToken)
	accessKey := accessTokenKey(accessHash)
	accessValues := map[string]any{
		"user_id":      strconv.FormatUint(userID, 10),
		"email":        email,
		"refresh_hash": refreshHash,
	}

	pipe := r.client.TxPipeline()
	pipe.HSet(ctx, accessKey, accessValues)
	pipe.Expire(ctx, accessKey, accessTTL)
	pipe.HSet(ctx, userTokenKey(userID), map[string]any{"access_hash": accessHash, "refresh_hash": refreshHash, "email": email})
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("save access token: %w", err)
	}
	return nil
}

// GetAccessSession 查询短令牌登录态
func (r *TokenRepo) GetAccessSession(ctx context.Context, accessToken string) (*TokenSession, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	accessHash := TokenHash(accessToken)
	values, err := r.client.HGetAll(ctx, accessTokenKey(accessHash)).Result()
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}
	return parseTokenSession(values, accessHash, values["refresh_hash"])
}

// GetRefreshSession 查询长令牌登录态
func (r *TokenRepo) GetRefreshSession(ctx context.Context, refreshToken string) (*TokenSession, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	refreshHash := TokenHash(refreshToken)
	values, err := r.client.HGetAll(ctx, refreshTokenKey(refreshHash)).Result()
	if err != nil {
		return nil, fmt.Errorf("get refresh token: %w", err)
	}
	return parseTokenSession(values, "", refreshHash)
}

// TokenHash 计算令牌哈希
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// RedisClient 返回已初始化的 Redis 客户端
func RedisClient() *redisx.Client {
	return redisx.Instance()
}

// validate 校验仓库状态
func (r *TokenRepo) validate() error {
	if r == nil || r.client == nil {
		return errors.New("token repo is nil")
	}
	return nil
}

// parseTokenSession 解析 Redis 登录态
func parseTokenSession(values map[string]string, accessHash string, refreshHash string) (*TokenSession, error) {
	if len(values) == 0 {
		return nil, ErrTokenNotFound
	}
	userID, err := strconv.ParseUint(values["user_id"], 10, 64)
	if err != nil || userID == 0 {
		return nil, errors.New("invalid token session user")
	}
	return &TokenSession{
		UserID:      userID,
		Email:       values["email"],
		AccessHash:  accessHash,
		RefreshHash: refreshHash,
	}, nil
}

// accessTokenKey 返回短令牌 Redis key
func accessTokenKey(hash string) string {
	return accessTokenKeyPrefix + hash
}

// refreshTokenKey 返回长令牌 Redis key
func refreshTokenKey(hash string) string {
	return refreshTokenKeyPrefix + hash
}

// userTokenKey 返回用户令牌 Redis key
func userTokenKey(userID uint64) string {
	return userTokenKeyPrefix + strconv.FormatUint(userID, 10)
}
