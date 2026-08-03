package logic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	conf "demo_server/config"
	"demo_server/pkg/roomtoken"
)

const (
	defaultMatchMode = "default" // 默认匹配模式
)

var (
	ErrInvalidPlayer        = errors.New("invalid player")
	ErrNoAvailableServer    = errors.New("no available roomserver")
	ErrRoomServerFull       = errors.New("roomserver full")
	ErrRoomTokenSecretEmpty = errors.New("room token secret is empty")
)

// AllocateResult 房间分配结果
type AllocateResult struct {
	RoomID     string // 房间ID
	ServerID   string // roomserver ID
	ServerAddr string // roomserver连接地址
	MatchID    string // 匹配ID
	RoomToken  string // 入房令牌
	ExpireAt   int64  // 过期时间戳，毫秒
}

// Matcher 匹配分配器
type Matcher struct {
	mu           sync.Mutex                           // 分配状态锁
	tokenSecret  string                               // room token签名密钥
	tokenExpire  time.Duration                        // room token有效期
	servers      []*serverState                       // roomserver状态列表
	reservations map[reservationKey]*reservationState // 活跃匹配占位
	sequence     uint64                               // 分配序号
	now          func() time.Time                     // 当前时间函数，便于测试占位过期
}

type serverState struct {
	id                string       // roomserver ID
	addr              string       // roomserver连接地址
	maxRooms          int          // 最大房间数
	maxPlayersPerRoom int          // 单房间人数上限
	rooms             []*roomState // 房间状态列表
}

type roomState struct {
	id           string                               // 房间ID
	reservations map[reservationKey]*reservationState // 当前有效占位
}

type reservationKey struct {
	playerID uint64 // 玩家ID
	mode     string // 匹配模式
}

type reservationState struct {
	key        reservationKey // 占位索引
	serverID   string         // roomserver ID
	serverAddr string         // roomserver连接地址
	roomID     string         // 房间ID
	matchID    string         // 匹配ID
	roomToken  string         // 入房令牌
	expireAt   time.Time      // 占位过期时间
	room       *roomState     // 所属房间
}

// NewMatcher 创建匹配分配器
func NewMatcher(cfg conf.MatchServerConfig) (*Matcher, error) {
	cfg = normalizeConfig(cfg)
	if strings.TrimSpace(cfg.TokenSecret) == "" {
		return nil, ErrRoomTokenSecretEmpty
	}
	if len(cfg.RoomServers) == 0 {
		return nil, ErrNoAvailableServer
	}

	servers := make([]*serverState, 0, len(cfg.RoomServers))
	for _, node := range cfg.RoomServers {
		if strings.TrimSpace(node.ServerID) == "" || strings.TrimSpace(node.ServerAddr) == "" {
			continue
		}
		servers = append(servers, &serverState{
			id:                node.ServerID,
			addr:              node.ServerAddr,
			maxRooms:          node.MaxRooms,
			maxPlayersPerRoom: node.MaxPlayersPerRoom,
			rooms:             make([]*roomState, 0),
		})
	}
	if len(servers) == 0 {
		return nil, ErrNoAvailableServer
	}
	return &Matcher{
		tokenSecret:  cfg.TokenSecret,
		tokenExpire:  cfg.TokenExpire,
		servers:      servers,
		reservations: make(map[reservationKey]*reservationState),
		now:          time.Now,
	}, nil
}

// AllocateRoom 分配房间并签发入房令牌
func (m *Matcher) AllocateRoom(ctx context.Context, playerID uint64, mode string) (*AllocateResult, error) {
	if m == nil {
		return nil, errors.New("matcher is nil")
	}
	if playerID == 0 {
		return nil, ErrInvalidPlayer
	}
	mode = normalizeMatchMode(mode)
	now := m.currentTime()

	m.mu.Lock()
	defer m.mu.Unlock()

	// 清理超时占位，避免失败入房长期占满房间
	m.purgeExpiredReservations(now)

	key := reservationKey{playerID: playerID, mode: mode}
	if reservation := m.reservations[key]; reservation != nil && reservation.expireAt.After(now) {
		return reservation.toAllocateResult(), nil
	}

	server, room, seq, err := m.allocateLocked()
	if err != nil {
		return nil, err
	}

	matchID := newMatchID(mode, seq)
	claims := roomtoken.Claims{
		PlayerID: playerID,
		RoomID:   room.id,
		ServerID: server.id,
		MatchID:  matchID,
		Nonce:    newNonce(playerID, seq),
	}
	// 签发短期入房令牌，客户端后续直连 roomserver 时携带
	token, err := roomtoken.Generate(m.tokenSecret, claims, m.tokenExpire)
	if err != nil {
		return nil, fmt.Errorf("generate room token: %w", err)
	}

	reservation := &reservationState{
		key:        key,
		serverID:   server.id,
		serverAddr: server.addr,
		roomID:     room.id,
		matchID:    matchID,
		roomToken:  token,
		expireAt:   now.Add(m.tokenExpire),
		room:       room,
	}
	room.reservations[key] = reservation
	m.reservations[key] = reservation
	return reservation.toAllocateResult(), nil
}

// allocateLocked 在锁内选择服务器和房间
func (m *Matcher) allocateLocked() (*serverState, *roomState, uint64, error) {
	for _, server := range m.servers {
		room := server.findAvailableRoom()
		if room == nil {
			room = server.createRoom()
		}
		if room == nil {
			continue
		}
		m.sequence++
		return server, room, m.sequence, nil
	}
	return nil, nil, 0, ErrRoomServerFull
}

// purgeExpiredReservations 释放过期的房间占位
func (m *Matcher) purgeExpiredReservations(now time.Time) {
	for key, reservation := range m.reservations {
		if reservation == nil || reservation.expireAt.After(now) {
			continue
		}
		delete(m.reservations, key)
		if reservation.room != nil {
			delete(reservation.room.reservations, key)
		}
	}
}

// findAvailableRoom 查找未满房间
func (s *serverState) findAvailableRoom() *roomState {
	for _, room := range s.rooms {
		if room.reservationCount() < s.maxPlayersPerRoom {
			return room
		}
	}
	return nil
}

// createRoom 创建新房间
func (s *serverState) createRoom() *roomState {
	if s.maxRooms > 0 && len(s.rooms) >= s.maxRooms {
		return nil
	}
	room := &roomState{id: fmt.Sprintf("%s-%d", s.id, len(s.rooms)+1), reservations: make(map[reservationKey]*reservationState)}
	s.rooms = append(s.rooms, room)
	return room
}

// reservationCount 返回房间当前有效占位数量
func (r *roomState) reservationCount() int {
	if r == nil {
		return 0
	}
	return len(r.reservations)
}

// toAllocateResult 转换为对外分配结果
func (r *reservationState) toAllocateResult() *AllocateResult {
	if r == nil {
		return nil
	}
	return &AllocateResult{
		RoomID:     r.roomID,
		ServerID:   r.serverID,
		ServerAddr: r.serverAddr,
		MatchID:    r.matchID,
		RoomToken:  r.roomToken,
		ExpireAt:   r.expireAt.UnixMilli(),
	}
}

// currentTime 返回 matcher 当前时间
func (m *Matcher) currentTime() time.Time {
	if m.now == nil {
		return time.Now()
	}
	return m.now()
}

// normalizeMatchMode 规整匹配模式
func normalizeMatchMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return defaultMatchMode
	}
	return mode
}

// normalizeConfig 补齐匹配配置默认值
func normalizeConfig(cfg conf.MatchServerConfig) conf.MatchServerConfig {
	if cfg.TokenExpire <= 0 {
		cfg.TokenExpire = time.Minute
	}
	if cfg.MaxPlayersPerRoom <= 0 {
		cfg.MaxPlayersPerRoom = 2
	}
	for index := range cfg.RoomServers {
		if cfg.RoomServers[index].MaxRooms <= 0 {
			cfg.RoomServers[index].MaxRooms = 1000
		}
		if cfg.RoomServers[index].MaxPlayersPerRoom <= 0 {
			cfg.RoomServers[index].MaxPlayersPerRoom = cfg.MaxPlayersPerRoom
		}
	}
	return cfg
}

// newMatchID 生成匹配ID
func newMatchID(mode string, sequence uint64) string {
	return fmt.Sprintf("%s-%d-%d", mode, time.Now().UnixMilli(), sequence)
}

// newNonce 生成入房令牌随机串
func newNonce(playerID uint64, sequence uint64) string {
	return fmt.Sprintf("%d-%d-%d", playerID, time.Now().UnixNano(), sequence)
}
