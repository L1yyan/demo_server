package logic

import (
	"context"
	"time"

	"demo_server/pkg/glog"
	"demo_server/src/roomserver/protocol"
)

type roomEventType int

const (
	roomEventJoin roomEventType = iota + 1
	roomEventLeave
	roomEventInput
)

type roomEvent struct {
	typeID   roomEventType
	player   *Player
	playerID uint64
	input    protocol.PlayerInput
}

type playerInputState struct {
	input   authoritativeInput // 最近一次有效输入
	hasData bool               // 是否已收到过有效输入
}

// Room 单局房间
type Room struct {
	id             string
	maxPlayers     int
	tickRate       int
	snapshotRate   int
	aoi            AOIFilter
	physics        PhysicsWorld
	events         chan roomEvent
	stop           chan struct{}
	players        map[uint64]*Player
	inputs         map[uint64]playerInputState
	tick           int64
	lastSnapshotAt int64
}

// NewRoom 创建房间
func NewRoom(id string, maxPlayers int, tickRate int, snapshotRate int, aoi AOIFilter, physics PhysicsWorld) *Room {
	if tickRate <= 0 {
		tickRate = 20
	}
	if snapshotRate <= 0 || snapshotRate > tickRate {
		snapshotRate = tickRate
	}
	if aoi == nil {
		aoi = NewSimpleAOIFilter()
	}
	if physics == nil {
		physics = NewSimplePhysicsWorld()
	}
	return &Room{
		id:           id,
		maxPlayers:   maxPlayers,
		tickRate:     tickRate,
		snapshotRate: snapshotRate,
		aoi:          aoi,
		physics:      physics,
		events:       make(chan roomEvent, 256),
		stop:         make(chan struct{}),
		players:      make(map[uint64]*Player),
		inputs:       make(map[uint64]playerInputState),
	}
}

// ID 返回房间ID
func (r *Room) ID() string {
	return r.id
}

// Tick 返回当前房间帧号
func (r *Room) Tick() int64 {
	return r.tick
}

// Start 启动房间循环
func (r *Room) Start(ctx context.Context) {
	go r.loop(ctx)
}

// Stop 停止房间循环
func (r *Room) Stop() {
	select {
	case <-r.stop:
		return
	default:
		close(r.stop)
	}
}

// Join 投递玩家加入事件
func (r *Room) Join(player *Player) bool {
	return r.pushEvent(roomEvent{typeID: roomEventJoin, player: player})
}

// Leave 投递玩家离开事件
func (r *Room) Leave(playerID uint64) bool {
	return r.pushEvent(roomEvent{typeID: roomEventLeave, playerID: playerID})
}

// PushInput 投递玩家输入事件
func (r *Room) PushInput(playerID uint64, input protocol.PlayerInput) bool {
	return r.pushEvent(roomEvent{typeID: roomEventInput, playerID: playerID, input: input})
}

// pushEvent 写入房间事件队列
func (r *Room) pushEvent(event roomEvent) bool {
	select {
	case r.events <- event:
		return true
	default:
		return false
	}
}

// loop 执行房间固定帧循环
func (r *Room) loop(ctx context.Context) {
	defer func() {
		if err := r.physics.Close(); err != nil {
			glog.Warn(ctx, "close physics world failed", glog.String("room_id", r.id), glog.Err(err))
		}
		if recovered := recover(); recovered != nil {
			glog.Error(ctx, "room loop panic", glog.String("room_id", r.id), glog.Any("panic", recovered))
		}
	}()

	interval := time.Second / time.Duration(r.tickRate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	glog.Info(ctx, "room started", glog.String("room_id", r.id))
	for {
		select {
		case <-ctx.Done():
			glog.Info(ctx, "room stopped by context", glog.String("room_id", r.id))
			return
		case <-r.stop:
			glog.Info(ctx, "room stopped", glog.String("room_id", r.id))
			return
		case event := <-r.events:
			r.handleEvent(ctx, event)
		case <-ticker.C:
			r.update(ctx)
		}
	}
}

// handleEvent 处理房间事件
func (r *Room) handleEvent(ctx context.Context, event roomEvent) {
	switch event.typeID {
	case roomEventJoin:
		r.handleJoin(ctx, event.player)
	case roomEventLeave:
		r.handleLeave(ctx, event.playerID)
	case roomEventInput:
		r.handleInput(event.playerID, event.input)
	}
}

// handleJoin 处理玩家加入房间
func (r *Room) handleJoin(ctx context.Context, player *Player) {
	if player == nil {
		return
	}
	if len(r.players) >= r.maxPlayers {
		message, _ := protocol.NewJSONMessage(protocol.MsgJoinRoomAck, protocol.JoinRoomAck{OK: false, RoomID: r.id, Content: "room is full", Tick: r.tick})
		player.Session.Send(message)
		return
	}
	if _, exists := r.players[player.ID]; exists {
		message, _ := protocol.NewJSONMessage(protocol.MsgJoinRoomAck, protocol.JoinRoomAck{OK: false, RoomID: r.id, Content: "player already joined", Tick: r.tick})
		player.Session.Send(message)
		return
	}

	player.RoomID = r.id
	player.HP = 100
	player.Alive = true
	if err := r.physics.AddPlayer(player.ID, Vector3{X: player.X, Y: player.Y, Z: player.Z}); err != nil {
		message, _ := protocol.NewJSONMessage(protocol.MsgJoinRoomAck, protocol.JoinRoomAck{OK: false, RoomID: r.id, Content: "physics add player failed", Tick: r.tick})
		player.Session.Send(message)
		glog.Warn(ctx, "add physics player failed", glog.String("room_id", r.id), glog.Uint64("player_id", player.ID), glog.Err(err))
		return
	}
	r.players[player.ID] = player

	message, _ := protocol.NewJSONMessage(protocol.MsgJoinRoomAck, protocol.JoinRoomAck{OK: true, RoomID: r.id, Content: "ok", Tick: r.tick})
	player.Session.Send(message)
	glog.Info(ctx, "player joined room", glog.String("room_id", r.id), glog.Uint64("player_id", player.ID))
}

// handleLeave 处理玩家离开房间
func (r *Room) handleLeave(ctx context.Context, playerID uint64) {
	if _, exists := r.players[playerID]; !exists {
		return
	}
	delete(r.players, playerID)
	delete(r.inputs, playerID)
	if err := r.physics.RemovePlayer(playerID); err != nil {
		glog.Warn(ctx, "remove physics player failed", glog.String("room_id", r.id), glog.Uint64("player_id", playerID), glog.Err(err))
	}
	glog.Info(ctx, "player left room", glog.String("room_id", r.id), glog.Uint64("player_id", playerID))
}

// handleInput 处理玩家输入
func (r *Room) handleInput(playerID uint64, input protocol.PlayerInput) {
	player, exists := r.players[playerID]
	if !exists || !player.Alive {
		return
	}

	// 客户端只提交输入意图，最终移动由服务端固定 tick 计算
	sanitized, ok := sanitizePlayerInput(input)
	if !ok {
		return
	}
	r.inputs[playerID] = playerInputState{input: sanitized, hasData: true}
}

// update 更新房间状态并按频率广播快照
func (r *Room) update(ctx context.Context) {
	r.tick++
	r.updatePlayers(ctx)
	if r.snapshotRate <= 0 {
		return
	}
	intervalTicks := int64(r.tickRate / r.snapshotRate)
	if intervalTicks <= 0 {
		intervalTicks = 1
	}
	if r.tick-r.lastSnapshotAt < intervalTicks {
		return
	}
	r.lastSnapshotAt = r.tick
	r.broadcastSnapshots(ctx)
}

// updatePlayers 按服务端固定 tick 推进玩家权威状态
func (r *Room) updatePlayers(ctx context.Context) {
	for playerID, player := range r.players {
		if player == nil || !player.Alive {
			continue
		}
		inputState, ok := r.inputs[playerID]
		if !ok || !inputState.hasData {
			continue
		}

		applyViewRotation(player, inputState.input)
		moveReq, ok := buildMovePlayerRequest(playerID, inputState.input, r.tickRate)
		if ok && moveReq.Distance > 0 {
			// 由物理世界计算最终位置，避免逻辑层绕过碰撞规则
			result, err := r.physics.MovePlayer(moveReq)
			if err != nil {
				glog.Warn(ctx, "move physics player failed", glog.String("room_id", r.id), glog.Uint64("player_id", playerID), glog.Err(err))
			} else {
				player.X = result.Position.X
				player.Y = result.Position.Y
				player.Z = result.Position.Z
			}
		}
		if inputState.input.Fire {
			_, _ = r.physics.Raycast(RaycastRequest{Origin: Vector3{X: player.X, Y: player.Y, Z: player.Z}, Direction: viewDirection(player.Yaw, player.Pitch), MaxDistance: 100})
		}
	}
}

// broadcastSnapshots 按 AOI 向玩家广播状态快照
func (r *Room) broadcastSnapshots(ctx context.Context) {
	players := make([]*Player, 0, len(r.players))
	for _, player := range r.players {
		players = append(players, player)
	}
	for _, player := range players {
		visible := r.aoi.FilterVisible(player, players)
		states := make([]protocol.PlayerState, 0, len(visible)+1)
		states = append(states, player.ToState())
		for _, visiblePlayer := range visible {
			states = append(states, visiblePlayer.ToState())
		}
		message, err := protocol.NewJSONMessage(protocol.MsgSnapshot, protocol.Snapshot{ServerTick: r.tick, Players: states})
		if err != nil {
			glog.Error(ctx, "build snapshot failed", glog.String("room_id", r.id), glog.Err(err))
			continue
		}
		player.Session.Send(message)
	}
}
