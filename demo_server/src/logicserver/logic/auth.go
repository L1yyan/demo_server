package logic

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	jwttool "demo_server/pkg/jwt"
	"demo_server/src/logicserver/repo"

	"golang.org/x/crypto/bcrypt"
)

const (
	invalidLoginParamsMessage = "invalid login params"      // 登录参数错误提示
	invalidCredentialMessage  = "invalid email or password" // 账号或密码错误提示
)

var (
	ErrInvalidLoginParams = errors.New(invalidLoginParamsMessage)
	ErrInvalidCredential  = errors.New(invalidCredentialMessage)
	ErrUnauthorized       = errors.New("unauthorized")
)

// AuthLogic 认证业务逻辑
type AuthLogic struct {
	users      *repo.UserRepo  // 用户仓库
	tokens     *repo.TokenRepo // token仓库
	jwt        *jwttool.JWT    // JWT工具
	accessTTL  time.Duration   // 短token存储时间
	refreshTTL time.Duration   // 长token存储时间
}

// LoginResult 登录结果
type LoginResult struct {
	AccessToken  string // 短token
	RefreshToken string // 长token
}

// NewAuthLogic 创建认证业务逻辑
func NewAuthLogic(users *repo.UserRepo, tokens *repo.TokenRepo, jwt *jwttool.JWT, accessTTL time.Duration, refreshTTL time.Duration) (*AuthLogic, error) {
	if users == nil {
		return nil, errors.New("user repo is nil")
	}
	if tokens == nil {
		return nil, errors.New("token repo is nil")
	}
	if jwt == nil {
		return nil, errors.New("jwt is nil")
	}
	if accessTTL <= 0 || refreshTTL <= 0 {
		return nil, errors.New("token ttl is invalid")
	}
	return &AuthLogic{users: users, tokens: tokens, jwt: jwt, accessTTL: accessTTL, refreshTTL: refreshTTL}, nil
}

// Login 使用邮箱和密码登录
func (l *AuthLogic) Login(ctx context.Context, email string, password string) (*LoginResult, error) {
	if l == nil {
		return nil, errors.New("auth logic is nil")
	}
	email = strings.ToLower(strings.TrimSpace(email))
	password = strings.TrimSpace(password)
	if !isValidEmail(email) || password == "" {
		return nil, ErrInvalidLoginParams
	}

	// 查询用户并统一隐藏账号是否存在
	user, err := l.users.FindByEmail(ctx, email)
	if errors.Is(err, repo.ErrUserNotFound) {
		return nil, ErrInvalidCredential
	}
	if err != nil {
		return nil, err
	}
	if user.UserID == 0 || user.PasswordHash == "" {
		return nil, ErrInvalidCredential
	}

	// 校验 bcrypt 密码哈希
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredential
	}

	// 生成并保存登录 token，保证返回给客户端的 token 服务端可校验
	accessToken, refreshToken, err := l.jwt.GenerateToken(user.UserID, user.Email)
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	if err := l.tokens.SaveLoginTokens(ctx, user.UserID, user.Email, accessToken, refreshToken, l.accessTTL, l.refreshTTL); err != nil {
		return nil, err
	}
	return &LoginResult{AccessToken: accessToken, RefreshToken: refreshToken}, nil
}

// VerifyToken 校验登录 token，必要时刷新短 token
func (l *AuthLogic) VerifyToken(ctx context.Context, accessToken string, refreshToken string) (*LoginResult, error) {
	if l == nil {
		return nil, errors.New("auth logic is nil")
	}
	accessToken = strings.TrimSpace(accessToken)
	refreshToken = strings.TrimSpace(refreshToken)
	if accessToken == "" {
		return nil, ErrUnauthorized
	}

	claims, expired, err := l.jwt.VerifyAccessToken(accessToken)
	if err == nil {
		return l.verifyActiveAccessToken(ctx, accessToken, refreshToken, claims.UserID)
	}
	if !expired {
		return nil, ErrUnauthorized
	}
	if refreshToken == "" {
		return nil, ErrUnauthorized
	}
	return l.refreshAccessToken(ctx, refreshToken)
}

// verifyActiveAccessToken 校验未过期短 token 是否仍在服务端有效
func (l *AuthLogic) verifyActiveAccessToken(ctx context.Context, accessToken string, refreshToken string, userID uint64) (*LoginResult, error) {
	session, err := l.tokens.GetAccessSession(ctx, accessToken)
	if err != nil {
		return nil, ErrUnauthorized
	}
	if session.UserID != userID {
		return nil, ErrUnauthorized
	}
	if refreshToken != "" && session.RefreshHash != repo.TokenHash(refreshToken) {
		return nil, ErrUnauthorized
	}
	return &LoginResult{AccessToken: accessToken, RefreshToken: refreshToken}, nil
}

// refreshAccessToken 使用长 token 刷新短 token
func (l *AuthLogic) refreshAccessToken(ctx context.Context, refreshToken string) (*LoginResult, error) {
	session, err := l.tokens.GetRefreshSession(ctx, refreshToken)
	if err != nil {
		return nil, ErrUnauthorized
	}
	newAccessToken, claims, err := l.jwt.RefreshAccessToken(refreshToken)
	if err != nil {
		return nil, ErrUnauthorized
	}
	if claims.UserID != session.UserID || claims.Email != session.Email {
		return nil, ErrUnauthorized
	}
	if err := l.tokens.SaveAccessToken(ctx, claims.UserID, claims.Email, newAccessToken, refreshToken, l.accessTTL); err != nil {
		return nil, err
	}
	return &LoginResult{AccessToken: newAccessToken, RefreshToken: refreshToken}, nil
}

// isValidEmail 校验邮箱格式
func isValidEmail(email string) bool {
	if email == "" {
		return false
	}
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email
}
