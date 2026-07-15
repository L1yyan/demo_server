package protocol

import (
	"time"

	"demo_server/pkg/roomtoken"
)

var (
	// ErrEmptyRoomToken 表示入房令牌为空
	ErrEmptyRoomToken = roomtoken.ErrEmptyRoomToken
	// ErrInvalidRoomToken 表示入房令牌非法
	ErrInvalidRoomToken = roomtoken.ErrInvalidRoomToken
	// ErrRoomTokenExpired 表示入房令牌已过期
	ErrRoomTokenExpired = roomtoken.ErrRoomTokenExpired
)

// RoomTokenClaims 入房令牌声明
type RoomTokenClaims = roomtoken.Claims

// GenerateRoomToken 生成短期入房令牌
func GenerateRoomToken(secret string, claims RoomTokenClaims, expire time.Duration) (string, error) {
	return roomtoken.Generate(secret, claims, expire)
}

// ParseRoomToken 解析并校验入房令牌
func ParseRoomToken(secret string, tokenString string) (*RoomTokenClaims, error) {
	return roomtoken.Parse(secret, tokenString)
}
