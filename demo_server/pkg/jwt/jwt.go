package Jwt

import (
	conf "demo_server/config"
	"errors"
	"fmt"
	"time"

	"github.com/dgrijalva/jwt-go"
)

var (
	instance = &JWT{}

	// ErrJWTNotInitialized 表示JWT配置尚未初始化
	ErrJWTNotInitialized = errors.New("jwt配置未初始化")
	// ErrEmptyAccessToken 表示访问令牌为空
	ErrEmptyAccessToken = errors.New("访问令牌不能为空")
	// ErrEmptyRefreshToken 表示刷新令牌为空
	ErrEmptyRefreshToken = errors.New("刷新令牌不能为空")
	// ErrEmptySecretKey 表示JWT密钥为空
	ErrEmptySecretKey = errors.New("jwt密钥不能为空")
	// ErrTokenExpired 表示令牌已过期
	ErrTokenExpired = errors.New("令牌已过期")
)

// JWTClaims 自定义JWT声明
type JWTClaims struct {
	UserID uint64 `json:"user_id"`
	Email  string `json:"email"`
	jwt.StandardClaims
}

// JwtConfig JWT工具配置
type JwtConfig struct {
	SecretKey         string        `yaml:"secretKey"`         // 签名密钥
	TokenExpire       time.Duration `yaml:"tokenExpire"`       // 访问令牌过期时间
	RefreshExpire     time.Duration `yaml:"refreshExpire"`     // 刷新令牌过期时间
	TokenHeaderKey    string        `yaml:"tokenHeaderKey"`    // 访问令牌请求头名称
	RefreshTokenKey   string        `yaml:"refreshTokenKey"`   // 刷新令牌请求头名称
	NewAccessTokenKey string        `yaml:"newAccessTokenKey"` // 新访问令牌响应头名称
	SkipPaths         []string      `yaml:"skipPaths"`         // 跳过鉴权的路径
	SkipPathsMap      map[string]bool
}

// JWT JWT令牌
type JWT struct {
	config *JwtConfig
}

// AuthResult JWT认证结果
type AuthResult struct {
	Claims         *JWTClaims // 用户声明
	NewAccessToken string     // 刷新后生成的新访问令牌
	Refreshed      bool       // 是否已刷新访问令牌
}

// InitJwtConf 初始化JWT配置
func InitJwtConf(jwtConfig *conf.JwtConfig) {
	if jwtConfig == nil {
		instance.config = nil
		return
	}

	instance.config = &JwtConfig{
		SecretKey:         jwtConfig.SecretKey,
		TokenExpire:       jwtConfig.TokenExpire,
		RefreshExpire:     jwtConfig.RefreshExpire,
		TokenHeaderKey:    jwtConfig.TokenHeaderKey,
		RefreshTokenKey:   jwtConfig.RefreshTokenKey,
		NewAccessTokenKey: jwtConfig.NewAccessTokenKey,
		SkipPaths:         jwtConfig.SkipPaths,
		SkipPathsMap:      make(map[string]bool, len(jwtConfig.SkipPaths)),
	}
	for _, path := range jwtConfig.SkipPaths {
		instance.config.SkipPathsMap[path] = true
	}
}

// NewJWT 获取JWT工具实例
func NewJWT() *JWT {
	return instance
}

// Instance 获取JWT工具实例
func Instance() *JWT {
	return instance
}

// ShouldSkipPath 判断路径是否跳过JWT校验
func (m *JWT) ShouldSkipPath(path string) bool {
	cfg, err := m.getConfig()
	if err != nil {
		return false
	}
	return cfg.SkipPathsMap[path]
}

// VerifyAccessToken 校验访问令牌
func (m *JWT) VerifyAccessToken(accessToken string) (*JWTClaims, bool, error) {
	if accessToken == "" {
		return nil, false, ErrEmptyAccessToken
	}
	return m.ParseToken(accessToken)
}

// AuthenticateTokens 校验访问令牌，过期时使用刷新令牌生成新访问令牌
func (m *JWT) AuthenticateTokens(accessToken string, refreshToken string) (*AuthResult, error) {
	claims, isTokenExpired, err := m.VerifyAccessToken(accessToken)
	if err == nil {
		return &AuthResult{Claims: claims}, nil
	}
	if !isTokenExpired {
		return nil, err
	}
	if refreshToken == "" {
		return nil, ErrEmptyRefreshToken
	}

	// 访问令牌过期时，通过刷新令牌换取新的访问令牌
	newAccessToken, claims, err := m.RefreshAccessToken(refreshToken)
	if err != nil {
		return nil, err
	}
	return &AuthResult{Claims: claims, NewAccessToken: newAccessToken, Refreshed: true}, nil
}

// GenerateToken 生成JWT访问令牌和刷新令牌
func (m *JWT) GenerateToken(userID uint64, email string) (string, string, error) {
	cfg, err := m.getConfig()
	if err != nil {
		return "", "", err
	}

	// 创建访问令牌声明
	claims := JWTClaims{
		UserID: userID,
		Email:  email,
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: time.Now().Add(cfg.TokenExpire).Unix(),
			Issuer:    "ryann",
		},
	}

	// 创建刷新令牌声明
	refreshClaims := JWTClaims{
		UserID: userID,
		Email:  email,
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: time.Now().Add(cfg.RefreshExpire).Unix(),
			Issuer:    "ryann",
		},
	}

	// 签发访问令牌
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString([]byte(cfg.SecretKey))
	if err != nil {
		return "", "", fmt.Errorf("生成令牌失败: %w", err)
	}

	// 签发刷新令牌
	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshTokenStr, err := refreshToken.SignedString([]byte(cfg.SecretKey))
	if err != nil {
		return "", "", fmt.Errorf("生成刷新令牌失败: %w", err)
	}

	return tokenStr, refreshTokenStr, nil
}

// RefreshToken 根据刷新令牌生成新的访问令牌
func (m *JWT) RefreshToken(refreshTokenStr string) (string, error) {
	newAccessToken, _, err := m.RefreshAccessToken(refreshTokenStr)
	return newAccessToken, err
}

// RefreshAccessToken 根据刷新令牌生成新的访问令牌并返回用户声明
func (m *JWT) RefreshAccessToken(refreshTokenStr string) (string, *JWTClaims, error) {
	cfg, err := m.getConfig()
	if err != nil {
		return "", nil, err
	}
	if refreshTokenStr == "" {
		return "", nil, ErrEmptyRefreshToken
	}

	// 解析刷新令牌，确认用户身份和令牌有效性
	claims, _, err := m.ParseToken(refreshTokenStr)
	if err != nil {
		return "", nil, err
	}

	// 根据刷新令牌中的用户信息签发新的访问令牌
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, JWTClaims{
		UserID: claims.UserID,
		Email:  claims.Email,
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: time.Now().Add(cfg.TokenExpire).Unix(),
			Issuer:    "ryann",
		},
	})

	newAccessToken, err := token.SignedString([]byte(cfg.SecretKey))
	if err != nil {
		return "", nil, fmt.Errorf("生成令牌失败: %w", err)
	}

	newClaims, _, err := m.ParseToken(newAccessToken)
	if err != nil {
		return "", nil, err
	}
	return newAccessToken, newClaims, nil
}

// ParseToken 解析JWT令牌，第二个返回值表示令牌是否过期
func (m *JWT) ParseToken(tokenStr string) (*JWTClaims, bool, error) {
	cfg, err := m.getConfig()
	if err != nil {
		return nil, false, err
	}
	if tokenStr == "" {
		return nil, false, ErrEmptyAccessToken
	}

	// 解析令牌并校验签名算法
	token, err := jwt.ParseWithClaims(tokenStr, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("无效的签名方法: %v", token.Header["alg"])
		}
		return []byte(cfg.SecretKey), nil
	})

	if err != nil {
		// 区分过期、签名错误和其他解析错误，便于上层决定返回内容
		var ve *jwt.ValidationError
		if errors.As(err, &ve) {
			if ve.Errors&jwt.ValidationErrorExpired != 0 {
				return nil, true, ErrTokenExpired
			}
			if ve.Errors&jwt.ValidationErrorSignatureInvalid != 0 {
				return nil, false, errors.New("无效的签名")
			}
		}
		return nil, false, fmt.Errorf("解析令牌失败: %w", err)
	}

	// 提取业务声明
	claims, ok := token.Claims.(*JWTClaims)
	if !ok || !token.Valid {
		return nil, false, errors.New("无效的令牌")
	}

	return claims, false, nil
}

// getConfig 获取已初始化的JWT配置
func (m *JWT) getConfig() (*JwtConfig, error) {
	if m == nil {
		m = instance
	}
	if m.config == nil {
		return nil, ErrJWTNotInitialized
	}
	if m.config.SecretKey == "" {
		return nil, ErrEmptySecretKey
	}
	return m.config, nil
}
