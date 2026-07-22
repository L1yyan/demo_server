package logic

import (
	"context"
	"errors"
	"sync"

	"demo_server/src/roomserver/protocol"
)

var (
	// ErrRoomLimitReached 表示房间数量已达上限
	ErrRoomLimitReached = errors.New("room limit reached")
	// ErrRoomEventQueueFull 表示房间事件队列已满
	ErrRoomEventQueueFull = errors.New("room event queue full")
)

// RoomManager 房间管理器
type RoomManager struct {
	ctx               context.Context
	mu                sync.RWMutex
	rooms             map[string]*Room
	playerRooms       map[uint64]string
	maxRooms          int
	maxPlayersPerRoom int
	tickRate          int
	snapshotRate      int
	aoi               AOIFilter
	physicsFactory    PhysicsWorldFactory
}

// NewRoomManager 创建房间管理器
func NewRoomManager(ctx context.Context, maxRooms int, maxPlayersPerRoom int, tickRate int, snapshotRate int, aoi AOIFilter, physicsFactory PhysicsWorldFactory) *RoomManager {
	if maxRooms <= 0 {
		maxRooms = 1000
	}
	if maxPlayersPerRoom <= 0 {
		maxPlayersPerRoom = 2
	}
	if physicsFactory == nil {
		physicsFactory = NewSimplePhysicsWorldFactory()
	}
	return &RoomManager{
		ctx:               ctx,
		rooms:             make(map[string]*Room),
		playerRooms:       make(map[uint64]string),
		maxRooms:          maxRooms,
		maxPlayersPerRoom: maxPlayersPerRoom,
		tickRate:          tickRate,
		snapshotRate:      snapshotRate,
		aoi:               aoi,
		physicsFactory:    physicsFactory,
	}
}

// JoinRoom 加入房间，不存在时自动创建房间
func (m *RoomManager) JoinRoom(roomID string, player *Player) error {
	if player == nil {
		return errors.New("player is nil")
	}
	room, err := m.getOrCreateRoom(roomID)
	if err != nil {
		return err
	}
	if ok := room.Join(player); !ok {
		return ErrRoomEventQueueFull
	}

	m.mu.Lock()
	m.playerRooms[player.ID] = roomID
	m.mu.Unlock()
	return nil
}

// LeaveRoom 离开房间
func (m *RoomManager) LeaveRoom(playerID uint64) {
	m.mu.Lock()
	roomID, exists := m.playerRooms[playerID]
	if exists {
		delete(m.playerRooms, playerID)
	}
	room := m.rooms[roomID]
	m.mu.Unlock()

	if exists && room != nil {
		room.Leave(playerID)
	}
}

// PushInput 投递玩家输入
func (m *RoomManager) PushInput(playerID uint64, input protocol.PlayerInput) error {
	m.mu.RLock()
	roomID, exists := m.playerRooms[playerID]
	room := m.rooms[roomID]
	m.mu.RUnlock()
	if !exists || room == nil {
		return errors.New("player room not found")
	}
	if ok := room.PushInput(playerID, input); !ok {
		return ErrRoomEventQueueFull
	}
	return nil
}

// Stop 停止所有房间
func (m *RoomManager) Stop() {
	m.mu.RLock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, room := range m.rooms {
		rooms = append(rooms, room)
	}
	m.mu.RUnlock()

	for _, room := range rooms {
		room.Stop()
	}
}

// getOrCreateRoom 获取或创建房间
func (m *RoomManager) getOrCreateRoom(roomID string) (*Room, error) {
	m.mu.RLock()
	room := m.rooms[roomID]
	m.mu.RUnlock()
	if room != nil {
		return room, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if room = m.rooms[roomID]; room != nil {
		return room, nil
	}
	if len(m.rooms) >= m.maxRooms {
		return nil, ErrRoomLimitReached
	}

	// 每个房间创建独立物理世界，避免不同房间玩家发生碰撞串扰
	physicsWorld, err := m.physicsFactory.NewWorld(roomID)
	if err != nil {
		return nil, err
	}
	room = NewRoom(roomID, m.maxPlayersPerRoom, m.tickRate, m.snapshotRate, m.aoi, physicsWorld)
	room.Start(m.ctx)
	m.rooms[roomID] = room
	return room, nil
}
