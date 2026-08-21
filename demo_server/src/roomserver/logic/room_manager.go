package logic

import (
	"context"
	"errors"
	"sync"
	"time"

	logicpb "demo_server/gen/logic"
	roompb "demo_server/gen/room"
	"demo_server/pkg/glog"
)

var (
	// ErrRoomLimitReached 表示房间数量已达上限
	ErrRoomLimitReached = errors.New("room limit reached")
	// ErrRoomEventQueueFull 表示房间事件队列已满
	ErrRoomEventQueueFull = errors.New("room event queue full")
	// ErrRoomAlreadyStarted 表示房间已开始或已结束，不能再加入
	ErrRoomAlreadyStarted = errors.New("game already started")
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
	syncConfig        SyncConfig
	mapID             string
	physicsHash       string
	gameDuration      time.Duration
	aoi               AOIFilter
	physicsFactory    PhysicsWorldFactory
	logicClient       logicpb.LogicServiceClient
}

// // NewRoomManager 创建房间管理器
// func NewRoomManager(ctx context.Context, maxRooms int, maxPlayersPerRoom int, tickRate int, snapshotRate int, aoi AOIFilter, physicsFactory PhysicsWorldFactory) *RoomManager {
// 	return NewRoomManagerWithSync(ctx, maxRooms, maxPlayersPerRoom, tickRate, snapshotRate, SyncConfig{}, "", "", aoi, physicsFactory)
// }

// // NewRoomManagerWithSync 创建带同步配置的房间管理器
// func NewRoomManagerWithSync(ctx context.Context, maxRooms int, maxPlayersPerRoom int, tickRate int, snapshotRate int, syncConfig SyncConfig, mapID string, physicsHash string, aoi AOIFilter, physicsFactory PhysicsWorldFactory) *RoomManager {
// 	return NewRoomManagerWithOptions(ctx, maxRooms, maxPlayersPerRoom, tickRate, snapshotRate, syncConfig, mapID, physicsHash, defaultGameDuration, aoi, physicsFactory)
// }

// NewRoomManagerWithOptions 创建带完整运行参数的房间管理器
func NewRoomManagerWithOptions(ctx context.Context, maxRooms int, maxPlayersPerRoom int, tickRate int, snapshotRate int, syncConfig SyncConfig, mapID string, physicsHash string, gameDuration time.Duration, aoi AOIFilter, physicsFactory PhysicsWorldFactory, logicClient logicpb.LogicServiceClient) *RoomManager {
	if maxRooms <= 0 {
		maxRooms = 1000
	}
	if maxPlayersPerRoom <= 0 {
		maxPlayersPerRoom = 2
	}
	if tickRate <= 0 {
		tickRate = 20
	}
	if snapshotRate <= 0 || snapshotRate > tickRate {
		snapshotRate = tickRate
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
		syncConfig:        syncConfig.Normalize(tickRate),
		mapID:             mapID,
		physicsHash:       physicsHash,
		gameDuration:      gameDuration,
		aoi:               aoi,
		physicsFactory:    physicsFactory,
		logicClient:       logicClient,
	}
}

// JoinRoom 加入房间，不存在时自动创建房间
func (m *RoomManager) JoinRoom(roomID string, player *Player) error {

	//TODO:这里获取equip_gun id 绑到Player上
	if player == nil {
		return errors.New("player is nil")
	}
	var req logicpb.GetEquipGunReq
	req.PlayerId = player.ID
	resp, err := m.logicClient.GetEquipGun(context.Background(), &req)
	if err != nil {
		return err
	}
	player.GunId = resp.GunId
	room, err := m.getOrCreateRoom(roomID)
	if err != nil {
		return err
	}
	if room.IsJoinClosed() {
		return ErrRoomAlreadyStarted
	}
	if ok := room.Join(player); !ok {
		if room.IsJoinClosed() {
			return ErrRoomAlreadyStarted
		}
		return ErrRoomEventQueueFull
	}

	m.mu.Lock()
	m.playerRooms[player.ID] = roomID
	m.mu.Unlock()
	return nil
}

// LeaveRoom 离开房间
func (m *RoomManager) LeaveRoom(playerID uint64, roomID string) {
	m.mu.Lock()
	currentRoomID, exists := m.playerRooms[playerID]
	if roomID != "" && exists && currentRoomID != roomID {
		m.mu.Unlock()
		return
	}
	if exists {
		delete(m.playerRooms, playerID)
	}
	room := m.rooms[currentRoomID]
	m.mu.Unlock()

	if exists && room != nil {
		room.Leave(playerID)
	}
}

// PushInput 投递玩家输入
func (m *RoomManager) PushInput(playerID uint64, input *roompb.PlayerInput) error {
	room, err := m.playerRoom(playerID)
	if err != nil {
		return err
	}
	if ok := room.PushInput(playerID, input); !ok {
		return ErrRoomEventQueueFull
	}
	return nil
}

// RoomTick 查询玩家所在房间当前帧号
func (m *RoomManager) RoomTick(playerID uint64) int64 {
	room, err := m.playerRoom(playerID)
	if err != nil {
		return 0
	}
	return room.Tick()
}

// QueryPlayerStats 查询同房间玩家战绩
func (m *RoomManager) QueryPlayerStats(requesterID uint64, targetID uint64) (PlayerStatsSnapshot, error) {
	room, err := m.playerRoom(requesterID)
	if err != nil {
		return PlayerStatsSnapshot{}, err
	}
	return room.QueryPlayerStats(requesterID, targetID)
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

// playerRoom 查询玩家所在房间
func (m *RoomManager) playerRoom(playerID uint64) (*Room, error) {
	m.mu.RLock()
	roomID, exists := m.playerRooms[playerID]
	room := m.rooms[roomID]
	m.mu.RUnlock()
	if !exists || room == nil {
		return nil, errors.New("player room not found")
	}
	return room, nil
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
	room = NewRoomWithOptions(roomID, m.maxPlayersPerRoom, m.tickRate, m.snapshotRate, m.aoi, physicsWorld, m.syncConfig, m.mapID, m.physicsHash, m.gameDuration, m.finishRoom)
	room.Start(m.ctx)
	m.rooms[roomID] = room
	return room, nil
}

// finishRoom 清理已结束房间索引
func (m *RoomManager) finishRoom(room *Room, playerIDs []uint64) {
	if room == nil {
		return
	}
	m.mu.Lock()
	if m.rooms[room.ID()] == room {
		delete(m.rooms, room.ID())
	}

	//结算对局到logicserver落地
	//奖励先写死 按照击杀数量判定输赢,击杀多的获得500Exp 1000coin 击杀少的获得200Exp 300coin 平局算400Exp 800coin
	//奖励要和playerid索引对应着写入
	var req logicpb.SettleUpGameRewardAndKdReq

	for playerID, roomID := range m.playerRooms {
		req.PlayerIds = append(req.PlayerIds, playerID)
		req.KillCount = append(req.KillCount, int64(m.rooms[roomID].players[playerID].KillCount))
	}

	if req.KillCount[0] > req.KillCount[1] {
		req.Coin[0] = 500
		req.Exp[0] = 1000
		req.Coin[1] = 300
		req.Exp[1] = 200
	} else if req.KillCount[0] < req.KillCount[1] {
		req.Coin[1] = 500
		req.Exp[1] = 1000
		req.Coin[0] = 300
		req.Exp[0] = 200
	} else {
		req.Coin[0] = 800
		req.Coin[1] = 800
		req.Exp[0] = 400
		req.Exp[1] = 400

	}
	_, err := m.logicClient.SettleUpGameRewardAndKd(m.ctx, &req)
	glog.Error(m.ctx, "结算失败:", glog.String("room_id:", room.id), glog.String("err:", err.Error()))
	for playerID, roomID := range m.playerRooms {
		if roomID == room.ID() {
			delete(m.playerRooms, playerID)
		}
	}

	m.mu.Unlock()
}
