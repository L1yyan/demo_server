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
	accountAlreadyExists      = "account already exists"    // 账号已存在提示
	maxBcryptPasswordBytes    = 72                          // bcrypt有效密码最大字节数
)

var (
	ErrInvalidLoginParams  = errors.New(invalidLoginParamsMessage)
	ErrInvalidCredential   = errors.New(invalidCredentialMessage)
	ErrAccountAlreadyExist = errors.New(accountAlreadyExists)
	ErrUnauthorized        = errors.New("unauthorized")
)

// AuthLogic 认证业务逻辑
type AuthLogic struct {
	users      *repo.UserRepo  // 用户仓库
	tokens     *repo.TokenRepo // token仓库
	jwt        *jwttool.JWT    // JWT工具
	accessTTL  time.Duration   // 短token存储时间
	refreshTTL time.Duration   // 长token存储时间
}

// LoginRegisterResult 登录注册结果
type LoginRegisterResult struct {
	UserID       uint64 // 用户ID
	Email        string // 邮箱
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

// Register 使用邮箱和密码注册账号
func (l *AuthLogic) Register(ctx context.Context, email string, password string) (*LoginRegisterResult, error) {
	if l == nil {
		return nil, errors.New("auth logic is nil")
	}
	email = normalizeLoginEmail(email)
	password = strings.TrimSpace(password)
	if !isValidEmail(email) || !isValidPassword(password) {
		return nil, ErrInvalidLoginParams
	}

	// 注册前先生成密码哈希，Mongo 只保存哈希后的密码
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	user, err := l.users.CreateUser(ctx, email, string(passwordHash))
	if errors.Is(err, repo.ErrUserAlreadyExists) {
		return nil, ErrAccountAlreadyExist
	}
	if err != nil {
		return nil, err
	}
	if user == nil || user.UserInfo.UserID == 0 || user.UserInfo.Email == "" {
		return nil, errors.New("created user is invalid")
	}

	return l.issueLoginTokens(ctx, user.UserInfo.UserID, user.UserInfo.Email)
}

// Login 使用邮箱和密码登录
func (l *AuthLogic) Login(ctx context.Context, email string, password string) (*LoginRegisterResult, error) {
	if l == nil {
		return nil, errors.New("auth logic is nil")
	}
	email = normalizeLoginEmail(email)
	password = strings.TrimSpace(password)
	if !isValidEmail(email) || !isValidPassword(password) {
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
	if user.UserInfo.UserID == 0 || user.UserInfo.PasswordHash == "" {
		return nil, ErrInvalidCredential
	}

	// 校验 bcrypt 密码哈希
	if err := bcrypt.CompareHashAndPassword([]byte(user.UserInfo.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredential
	}

	return l.issueLoginTokens(ctx, user.UserInfo.UserID, user.UserInfo.Email)
}

// VerifyToken 校验登录 token，必要时刷新短 token
func (l *AuthLogic) VerifyToken(ctx context.Context, accessToken string, refreshToken string) (*LoginRegisterResult, error) {
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
		return l.verifyActiveAccessToken(ctx, accessToken, refreshToken, claims.UserID, claims.Email)
	}
	if !expired {
		return nil, ErrUnauthorized
	}
	if refreshToken == "" {
		return nil, ErrUnauthorized
	}
	return l.refreshAccessToken(ctx, refreshToken)
}

// issueLoginTokens 为认证成功的用户签发并保存登录令牌
func (l *AuthLogic) issueLoginTokens(ctx context.Context, userID uint64, email string) (*LoginRegisterResult, error) {
	if userID == 0 || strings.TrimSpace(email) == "" {
		return nil, ErrInvalidCredential
	}

	// 生成并保存登录 token，保证返回给客户端的 token 服务端可校验
	accessToken, refreshToken, err := l.jwt.GenerateToken(userID, email)
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	if err := l.tokens.SaveLoginTokens(ctx, userID, email, accessToken, refreshToken, l.accessTTL, l.refreshTTL); err != nil {
		return nil, err
	}
	return &LoginRegisterResult{UserID: userID, Email: email, AccessToken: accessToken, RefreshToken: refreshToken}, nil
}

// verifyActiveAccessToken 校验未过期短 token 是否仍在服务端有效
func (l *AuthLogic) verifyActiveAccessToken(ctx context.Context, accessToken string, refreshToken string, userID uint64, email string) (*LoginRegisterResult, error) {
	session, err := l.tokens.GetAccessSession(ctx, accessToken)
	if err != nil {
		return nil, ErrUnauthorized
	}
	if session.UserID != userID {
		return nil, ErrUnauthorized
	}
	if session.Email != "" && email != "" && session.Email != email {
		return nil, ErrUnauthorized
	}
	if refreshToken != "" && session.RefreshHash != repo.TokenHash(refreshToken) {
		return nil, ErrUnauthorized
	}
	return &LoginRegisterResult{UserID: userID, Email: email, AccessToken: accessToken, RefreshToken: refreshToken}, nil
}

// refreshAccessToken 使用长 token 刷新短 token
func (l *AuthLogic) refreshAccessToken(ctx context.Context, refreshToken string) (*LoginRegisterResult, error) {
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
	return &LoginRegisterResult{UserID: claims.UserID, Email: claims.Email, AccessToken: newAccessToken, RefreshToken: refreshToken}, nil
}

// normalizeLoginEmail 统一登录注册邮箱格式
func normalizeLoginEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// isValidEmail 校验邮箱格式
func isValidEmail(email string) bool {
	if email == "" {
		return false
	}
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email
}

// isValidPassword 校验密码是否可用于 bcrypt
func isValidPassword(password string) bool {
	return password != "" && len([]byte(password)) <= maxBcryptPasswordBytes
}
