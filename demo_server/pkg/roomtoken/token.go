package roomtoken

import (
	"errors"
	"fmt"
	"time"

	"github.com/dgrijalva/jwt-go"
)

var (
	// ErrEmptyRoomToken 表示入房令牌为空
	ErrEmptyRoomToken = errors.New("room token is empty")
	// ErrInvalidRoomToken 表示入房令牌非法
	ErrInvalidRoomToken = errors.New("room token is invalid")
	// ErrRoomTokenExpired 表示入房令牌已过期
	ErrRoomTokenExpired = errors.New("room token expired")
)

// Claims 入房令牌声明
type Claims struct {
	PlayerID uint64 `json:"player_id"` // 玩家ID
	RoomID   string `json:"room_id"`   // 房间ID
	ServerID string `json:"server_id"` // 目标roomserver ID
	MatchID  string `json:"match_id"`  // 匹配ID
	Nonce    string `json:"nonce"`     // 随机串
	jwt.StandardClaims
}

// Generate 生成短期入房令牌
func Generate(secret string, claims Claims, expire time.Duration) (string, error) {
	if secret == "" {
		return "", errors.New("room token secret is empty")
	}
	if expire <= 0 {
		expire = time.Minute
	}
	claims.StandardClaims.ExpiresAt = time.Now().Add(expire).Unix()
	claims.StandardClaims.Issuer = "matchserver"

	// 使用 HMAC 签名，matchserver 和 roomserver 共享密钥
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("sign room token: %w", err)
	}
	return tokenString, nil
}

// Parse 解析并校验入房令牌
func Parse(secret string, tokenString string) (*Claims, error) {
	if tokenString == "" {
		return nil, ErrEmptyRoomToken
	}
	if secret == "" {
		return nil, errors.New("room token secret is empty")
	}

	// 解析令牌时同时限制签名算法，避免算法降级风险
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		var validationErr *jwt.ValidationError
		if errors.As(err, &validationErr) && validationErr.Errors&jwt.ValidationErrorExpired != 0 {
			return nil, ErrRoomTokenExpired
		}
		return nil, fmt.Errorf("parse room token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidRoomToken
	}
	return claims, nil
}
