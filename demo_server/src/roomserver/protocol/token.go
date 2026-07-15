package protocol

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

// RoomTokenClaims 入房令牌声明
type RoomTokenClaims struct {
	PlayerID uint64 `json:"player_id"`
	RoomID   string `json:"room_id"`
	ServerID string `json:"server_id"`
	MatchID  string `json:"match_id"`
	Nonce    string `json:"nonce"`
	jwt.StandardClaims
}

// GenerateRoomToken 生成短期入房令牌
func GenerateRoomToken(secret string, claims RoomTokenClaims, expire time.Duration) (string, error) {
	if secret == "" {
		return "", errors.New("room token secret is empty")
	}
	if expire <= 0 {
		expire = time.Minute
	}
	claims.StandardClaims.ExpiresAt = time.Now().Add(expire).Unix()
	claims.StandardClaims.Issuer = "roomserver"

	// 使用 HMAC 签名，后续 matchserver 和 roomserver 需要共享密钥
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("sign room token: %w", err)
	}
	return tokenString, nil
}

// ParseRoomToken 解析并校验入房令牌
func ParseRoomToken(secret string, tokenString string) (*RoomTokenClaims, error) {
	if tokenString == "" {
		return nil, ErrEmptyRoomToken
	}
	if secret == "" {
		return nil, errors.New("room token secret is empty")
	}

	// 解析令牌时同时限制签名算法，避免算法降级风险
	token, err := jwt.ParseWithClaims(tokenString, &RoomTokenClaims{}, func(token *jwt.Token) (interface{}, error) {
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

	claims, ok := token.Claims.(*RoomTokenClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidRoomToken
	}
	return claims, nil
}
