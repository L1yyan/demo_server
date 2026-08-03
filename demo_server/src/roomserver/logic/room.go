package logic

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"demo_server/pkg/glog"
	"demo_server/src/roomserver/protocol"
)

type roomEventType int

const (
	roomEventJoin roomEventType = iota + 1
	roomEventLeave
	roomEventInput
	roomEventInputBatch
	roomEventPlayerStatsQuery
)

type roomEvent struct {
	typeID       roomEventType
	player       *Player
	playerID     uint64
	targetID     uint64
	input        protocol.PlayerInput
	batch        protocol.PlayerInputBatch
	statsResp    chan playerStatsQueryResult
	joinReserved bool
}

// PlayerStatsSnapshot 玩家战绩查询快照
type PlayerStatsSnapshot struct {
	RoomID     string               // 房间ID
	ServerTick int64                // 查询时房间帧号
	Stats      protocol.PlayerStats // 玩家战绩
}

type playerStatsQueryResult struct {
	snapshot PlayerStatsSnapshot
	err      error
}

var (
	// ErrPlayerStatsQueryTimeout 表示玩家战绩查询超时
	ErrPlayerStatsQueryTimeout = errors.New("player stats query timeout")
	// ErrPlayerStatsNotFound 表示玩家战绩不存在
	ErrPlayerStatsNotFound = errors.New("player stats not found")
)

const (
	inputDiagnosticAccepted          = "accepted"
	inputDiagnosticLateRescheduled   = "late_rescheduled"
	inputDiagnosticLateDropped       = "late_dropped"
	inputDiagnosticFutureDropped     = "future_dropped"
	inputDiagnosticStaleCorrection   = "stale_correction"
	inputDiagnosticRescheduleDropped = "reschedule_dropped"
	inputDiagnosticIntervalTicks     = 20
	lateInputCorrectionDelayTicks    = 3
	defaultGameDuration              = 3 * time.Minute
	defaultPlayerHP                  = 100
	defaultRespawnInvincibleDuration = 5 * time.Second
	defaultFireDamage                = 20
	defaultFireMaxDistance           = 100.0
	defaultFireViewHeight            = 0.9
	playerStatsQueryTimeout          = time.Second
	gameOverReasonTimeLimit          = "time_limit"
)

// Room 单局房间
type Room struct {
	id                string
	maxPlayers        int
	tickRate          int
	snapshotRate      int
	syncConfig        SyncConfig
	syncMode          string
	mapID             string
	physicsHash       string
	gameDuration      time.Duration
	gameDurationTicks int64
	gameStarted       bool
	gameEnded         bool
	gameStartTick     int64
	gameEndTick       int64
	onFinished        func(room *Room, playerIDs []uint64)
	joinClosed        atomic.Bool
	joinSlots         atomic.Int64
	currentTick       atomic.Int64
	aoi               AOIFilter
	physics           PhysicsWorld
	events            chan roomEvent
	stop              chan struct{}
	players           map[uint64]*Player
	syncStates        map[uint64]*playerSyncState
	tick              int64
	lastSnapshotAt    int64
}

// NewRoom 创建房间
func NewRoom(id string, maxPlayers int, tickRate int, snapshotRate int, aoi AOIFilter, physics PhysicsWorld) *Room {
	return NewRoomWithSync(id, maxPlayers, tickRate, snapshotRate, aoi, physics, SyncConfig{}, "", "")
}

// NewRoomWithSync 创建带同步配置的房间
func NewRoomWithSync(id string, maxPlayers int, tickRate int, snapshotRate int, aoi AOIFilter, physics PhysicsWorld, syncConfig SyncConfig, mapID string, physicsHash string) *Room {
	return NewRoomWithOptions(id, maxPlayers, tickRate, snapshotRate, aoi, physics, syncConfig, mapID, physicsHash, defaultGameDuration, nil)
}

// NewRoomWithOptions 创建带完整运行参数的房间
func NewRoomWithOptions(id string, maxPlayers int, tickRate int, snapshotRate int, aoi AOIFilter, physics PhysicsWorld, syncConfig SyncConfig, mapID string, physicsHash string, gameDuration time.Duration, onFinished func(room *Room, playerIDs []uint64)) *Room {
	if maxPlayers <= 0 {
		maxPlayers = 2
	}
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
	if gameDuration <= 0 {
		gameDuration = defaultGameDuration
	}
	gameDurationTicks := durationToTicks(gameDuration, tickRate)
	syncConfig = syncConfig.Normalize(tickRate)
	syncMode := SyncModeSnapshotOnly
	if syncConfig.PredictionEnabled {
		syncMode = SyncModePredictionAuthoritative
	}
	return &Room{
		id:                id,
		maxPlayers:        maxPlayers,
		tickRate:          tickRate,
		snapshotRate:      snapshotRate,
		syncConfig:        syncConfig,
		syncMode:          syncMode,
		mapID:             mapID,
		physicsHash:       physicsHash,
		gameDuration:      gameDuration,
		gameDurationTicks: gameDurationTicks,
		onFinished:        onFinished,
		aoi:               aoi,
		physics:           physics,
		events:            make(chan roomEvent, 256),
		stop:              make(chan struct{}),
		players:           make(map[uint64]*Player),
		syncStates:        make(map[uint64]*playerSyncState),
	}
}

// durationToTicks 将对局时长转换为房间逻辑帧数
func durationToTicks(duration time.Duration, tickRate int) int64 {
	if duration <= 0 {
		duration = defaultGameDuration
	}
	if tickRate <= 0 {
		tickRate = 20
	}
	ticks := int64(duration * time.Duration(tickRate) / time.Second)
	if ticks <= 0 {
		return 1
	}
	return ticks
}

// ID 返回房间ID
func (r *Room) ID() string {
	return r.id
}

// Tick 返回当前房间帧号
func (r *Room) Tick() int64 {
	return r.currentTick.Load()
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
	if player == nil || r.joinClosed.Load() {
		return false
	}
	// 预占入房名额，避免并发入房在房间事件串行处理前超发
	joinedSlots := r.joinSlots.Add(1)
	if joinedSlots > int64(r.maxPlayers) {
		r.joinSlots.Add(-1)
		return false
	}
	if ok := r.pushEvent(roomEvent{typeID: roomEventJoin, player: player, joinReserved: true}); !ok {
		r.joinSlots.Add(-1)
		return false
	}
	return true
}

// Leave 投递玩家离开事件
func (r *Room) Leave(playerID uint64) bool {
	return r.pushEvent(roomEvent{typeID: roomEventLeave, playerID: playerID})
}

// IsJoinClosed 判断房间是否已关闭入房
func (r *Room) IsJoinClosed() bool {
	if r == nil {
		return true
	}
	return r.joinClosed.Load()
}

// PushInput 投递玩家输入事件
func (r *Room) PushInput(playerID uint64, input protocol.PlayerInput) bool {
	return r.pushEvent(roomEvent{typeID: roomEventInput, playerID: playerID, input: input})
}

// PushInputBatch 投递玩家批量输入事件
func (r *Room) PushInputBatch(playerID uint64, batch protocol.PlayerInputBatch) bool {
	return r.pushEvent(roomEvent{typeID: roomEventInputBatch, playerID: playerID, batch: batch})
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
			if r.update(ctx) {
				return
			}
		}
	}
}

// handleEvent 处理房间事件
func (r *Room) handleEvent(ctx context.Context, event roomEvent) {
	switch event.typeID {
	case roomEventJoin:
		r.handleJoinEvent(ctx, event)
	case roomEventLeave:
		r.handleLeave(ctx, event.playerID)
	case roomEventInput:
		r.handleInput(ctx, event.playerID, event.input)
	case roomEventInputBatch:
		r.handleInputBatch(ctx, event.playerID, event.batch)
	case roomEventPlayerStatsQuery:
		r.handlePlayerStatsQuery(event)
	}
}

// handleJoin 处理玩家加入房间
func (r *Room) handleJoin(ctx context.Context, player *Player) {
	r.handleJoinEvent(ctx, roomEvent{player: player})
}

// handleJoinEvent 处理玩家加入房间事件
func (r *Room) handleJoinEvent(ctx context.Context, event roomEvent) {
	player := event.player
	if player == nil {
		return
	}
	joined := false
	defer func() {
		if event.joinReserved {
			if !joined {
				r.releaseJoinSlot()
			}
			return
		}
		r.reconcileJoinSlots()
	}()
	if r.gameStarted || r.gameEnded {
		message, _ := protocol.NewJSONMessage(protocol.MsgJoinRoomAck, r.buildJoinAck(false, nil, "game already started"))
		player.Session.Send(message)
		return
	}
	if len(r.players) >= r.maxPlayers {
		message, _ := protocol.NewJSONMessage(protocol.MsgJoinRoomAck, r.buildJoinAck(false, nil, "room is full"))
		player.Session.Send(message)
		return
	}
	if _, exists := r.players[player.ID]; exists {
		message, _ := protocol.NewJSONMessage(protocol.MsgJoinRoomAck, r.buildJoinAck(false, nil, "player already joined"))
		player.Session.Send(message)
		return
	}

	spawnPoint, ok := r.nextSpawnPoint()
	if !ok {
		message, _ := protocol.NewJSONMessage(protocol.MsgJoinRoomAck, r.buildJoinAck(false, nil, "spawn point not available"))
		player.Session.Send(message)
		return
	}

	player.RoomID = r.id
	player.X = spawnPoint.Position.X
	player.Y = spawnPoint.Position.Y
	player.Z = spawnPoint.Position.Z
	player.Yaw = spawnPoint.Yaw
	player.Pitch = 0
	player.HP = defaultPlayerHP
	player.KillCount = 0
	player.DeathCount = 0
	player.SpawnID = spawnPoint.ID
	player.Alive = true
	player.InvincibleUntilTick = 0
	player.VerticalVelocity = 0
	player.Grounded = true
	player.SyncMode = r.playerSyncMode(player)
	if err := r.physics.AddPlayer(player.ID, spawnPoint.Position); err != nil {
		message, _ := protocol.NewJSONMessage(protocol.MsgJoinRoomAck, r.buildJoinAck(false, nil, "physics add player failed"))
		player.Session.Send(message)
		glog.Warn(ctx, "add physics player failed", glog.String("room_id", r.id), glog.Uint64("player_id", player.ID), glog.Err(err))
		return
	}
	r.players[player.ID] = player
	r.syncStates[player.ID] = newPlayerSyncState()
	r.saveAuthoritativeState(player.ID, player)
	joined = true

	shouldBroadcastGameStart := false
	if len(r.players) >= r.maxPlayers {
		shouldBroadcastGameStart = r.markGameStarted(ctx)
	}
	message, _ := protocol.NewJSONMessage(protocol.MsgJoinRoomAck, r.buildJoinAck(true, player, "ok"))
	player.Session.Send(message)
	glog.Info(ctx, "player joined room", glog.String("room_id", r.id), glog.Uint64("player_id", player.ID))
	if shouldBroadcastGameStart {
		r.broadcastGameStart(ctx)
	}
}

// releaseJoinSlot 释放失败入房事件的预占名额
func (r *Room) releaseJoinSlot() {
	if r == nil || r.joinSlots.Load() <= 0 {
		return
	}
	r.joinSlots.Add(-1)
}

// reconcileJoinSlots 同步入房预占位和房间真实人数
func (r *Room) reconcileJoinSlots() {
	if r == nil {
		return
	}
	r.joinSlots.Store(int64(len(r.players)))
}

// playerSyncMode 判断玩家实际使用的同步模式
func (r *Room) playerSyncMode(player *Player) string {
	if player == nil || !r.syncConfig.PredictionEnabled || !player.PredictionEnabled || player.SyncVersion <= 0 {
		return SyncModeSnapshotOnly
	}
	if r.physicsHash != "" && player.PhysicsHash != r.physicsHash {
		return SyncModeSnapshotOnly
	}
	return SyncModePredictionAuthoritative
}

// gameDurationSeconds 返回对局时长秒数
func (r *Room) gameDurationSeconds() int64 {
	seconds := int64(r.gameDuration / time.Second)
	if seconds <= 0 {
		return int64(defaultGameDuration / time.Second)
	}
	return seconds
}

// markGameStarted 标记房间进入倒计时状态
func (r *Room) markGameStarted(ctx context.Context) bool {
	if r.gameStarted || r.gameEnded {
		return false
	}
	r.gameStarted = true
	r.joinClosed.Store(true)
	r.joinSlots.Store(int64(r.maxPlayers))
	r.gameStartTick = r.tick
	r.gameEndTick = r.tick + r.gameDurationTicks
	glog.Info(ctx, "room game started", glog.String("room_id", r.id), glog.Int64("start_tick", r.gameStartTick), glog.Int64("end_tick", r.gameEndTick), glog.Int64("duration_seconds", r.gameDurationSeconds()))
	return true
}

// buildJoinAck 构造加入房间响应
func (r *Room) buildJoinAck(ok bool, player *Player, content string) protocol.JoinRoomAck {
	tick := r.Tick()
	ack := protocol.JoinRoomAck{
		OK:                         ok,
		RoomID:                     r.id,
		Content:                    content,
		Tick:                       tick,
		TickRate:                   r.tickRate,
		SnapshotRate:               r.snapshotRate,
		ServerTime:                 time.Now().UnixMilli(),
		SyncMode:                   r.syncMode,
		MapID:                      r.mapID,
		PhysicsHash:                r.physicsHash,
		RollbackWindowTicks:        r.syncConfig.RollbackWindowTicks,
		FutureInputWindowTicks:     r.syncConfig.FutureInputWindowTicks,
		PredictionKeyframeInterval: r.syncConfig.PredictionKeyframeInterval,
		PositionTolerance:          r.syncConfig.PositionTolerance,
		HardPositionTolerance:      r.syncConfig.HardPositionTolerance,
		AngleTolerance:             r.syncConfig.AngleTolerance,
		GameDurationSeconds:        r.gameDurationSeconds(),
		GameStarted:                r.gameStarted,
		GameStartTick:              r.gameStartTick,
		GameEndTick:                r.gameEndTick,
	}
	if player != nil {
		if player.SyncMode == "" {
			player.SyncMode = r.playerSyncMode(player)
		}
		ack.SyncMode = player.SyncMode
		ack.SpawnID = player.SpawnID
		ack.X = player.X
		ack.Y = player.Y
		ack.Z = player.Z
		ack.Yaw = player.Yaw
		ack.Pitch = player.Pitch
	}
	return ack
}

// nextSpawnPoint 选择当前房间未被占用的出生点
func (r *Room) nextSpawnPoint() (SpawnPoint, bool) {
	spawnPoints := r.physics.SpawnPoints()
	if len(spawnPoints) == 0 {
		return SpawnPoint{}, false
	}
	used := make(map[string]struct{}, len(r.players))
	for _, player := range r.players {
		if player != nil && player.SpawnID != "" {
			used[player.SpawnID] = struct{}{}
		}
	}
	for _, spawnPoint := range spawnPoints {
		if spawnPoint.ID == "" {
			continue
		}
		if _, exists := used[spawnPoint.ID]; exists {
			continue
		}
		return spawnPoint, true
	}
	return SpawnPoint{}, false
}

// broadcastGameStart 广播对局开始通知
func (r *Room) broadcastGameStart(ctx context.Context) {
	message, err := protocol.NewJSONMessage(protocol.MsgGameStart, protocol.GameStart{
		RoomID:          r.id,
		ServerTick:      r.tick,
		StartTick:       r.gameStartTick,
		EndTick:         r.gameEndTick,
		DurationSeconds: r.gameDurationSeconds(),
		ServerTime:      time.Now().UnixMilli(),
	})
	if err != nil {
		glog.Error(ctx, "build game start failed", glog.String("room_id", r.id), glog.Err(err))
		return
	}
	for _, player := range r.players {
		if player == nil {
			continue
		}
		player.Session.Send(message)
	}
}

// shouldFinishGame 判断本局是否已达到限时
func (r *Room) shouldFinishGame() bool {
	return r.gameStarted && !r.gameEnded && r.gameEndTick > 0 && r.tick >= r.gameEndTick
}

// finishGame 广播对局结束并触发房间清理
func (r *Room) finishGame(ctx context.Context) {
	if r.gameEnded {
		return
	}
	r.gameEnded = true
	r.joinClosed.Store(true)
	r.broadcastGameOver(ctx)
	playerIDs := r.playerIDs()
	if r.onFinished != nil {
		r.onFinished(r, playerIDs)
	}
	glog.Info(ctx, "room game ended", glog.String("room_id", r.id), glog.Int64("server_tick", r.tick), glog.Int("player_count", len(playerIDs)), glog.String("reason", gameOverReasonTimeLimit))
}

// broadcastGameOver 广播对局结束通知
func (r *Room) broadcastGameOver(ctx context.Context) {
	message, err := protocol.NewJSONMessage(protocol.MsgGameOver, protocol.GameOver{
		RoomID:     r.id,
		ServerTick: r.tick,
		StartTick:  r.gameStartTick,
		EndTick:    r.gameEndTick,
		Reason:     gameOverReasonTimeLimit,
		ServerTime: time.Now().UnixMilli(),
		Players:    r.playerStates(),
	})
	if err != nil {
		glog.Error(ctx, "build game over failed", glog.String("room_id", r.id), glog.Err(err))
		return
	}
	for _, player := range r.players {
		if player == nil {
			continue
		}
		player.Session.Send(message)
	}
}

// playerIDs 返回当前房间玩家ID列表
func (r *Room) playerIDs() []uint64 {
	ids := make([]uint64, 0, len(r.players))
	for playerID := range r.players {
		ids = append(ids, playerID)
	}
	return ids
}

// playerStates 返回当前房间玩家状态列表
func (r *Room) playerStates() []protocol.PlayerState {
	states := make([]protocol.PlayerState, 0, len(r.players))
	for _, player := range r.players {
		if player == nil {
			continue
		}
		states = append(states, player.ToStateAt(r.tick))
	}
	return states
}

// handleLeave 处理玩家离开房间
func (r *Room) handleLeave(ctx context.Context, playerID uint64) {
	if _, exists := r.players[playerID]; !exists {
		return
	}
	delete(r.players, playerID)
	delete(r.syncStates, playerID)
	r.reconcileJoinSlots()
	if err := r.physics.RemovePlayer(playerID); err != nil {
		glog.Warn(ctx, "remove physics player failed", glog.String("room_id", r.id), glog.Uint64("player_id", playerID), glog.Err(err))
	}
	glog.Info(ctx, "player left room", glog.String("room_id", r.id), glog.Uint64("player_id", playerID))
}

// QueryPlayerStats 查询房间内玩家战绩
func (r *Room) QueryPlayerStats(requesterID uint64, targetID uint64) (PlayerStatsSnapshot, error) {
	if r == nil {
		return PlayerStatsSnapshot{}, ErrPlayerStatsNotFound
	}
	if targetID == 0 {
		targetID = requesterID
	}
	response := make(chan playerStatsQueryResult, 1)
	if !r.pushEvent(roomEvent{typeID: roomEventPlayerStatsQuery, playerID: requesterID, targetID: targetID, statsResp: response}) {
		return PlayerStatsSnapshot{}, ErrRoomEventQueueFull
	}

	select {
	case result := <-response:
		return result.snapshot, result.err
	case <-r.stop:
		return PlayerStatsSnapshot{}, ErrPlayerStatsQueryTimeout
	case <-time.After(playerStatsQueryTimeout):
		return PlayerStatsSnapshot{}, ErrPlayerStatsQueryTimeout
	}
}

// handlePlayerStatsQuery 处理玩家战绩查询事件
func (r *Room) handlePlayerStatsQuery(event roomEvent) {
	if event.statsResp == nil {
		return
	}
	snapshot, err := r.lookupPlayerStats(event.playerID, event.targetID)
	select {
	case event.statsResp <- playerStatsQueryResult{snapshot: snapshot, err: err}:
	default:
	}
}

// lookupPlayerStats 查询当前房间内玩家战绩
func (r *Room) lookupPlayerStats(requesterID uint64, targetID uint64) (PlayerStatsSnapshot, error) {
	if requesterID == 0 {
		return PlayerStatsSnapshot{}, ErrPlayerStatsNotFound
	}
	if targetID == 0 {
		targetID = requesterID
	}
	if _, exists := r.players[requesterID]; !exists {
		return PlayerStatsSnapshot{}, ErrPlayerStatsNotFound
	}
	target := r.players[targetID]
	if target == nil {
		return PlayerStatsSnapshot{}, ErrPlayerStatsNotFound
	}
	return PlayerStatsSnapshot{RoomID: r.id, ServerTick: r.tick, Stats: target.ToStats()}, nil
}

// handleInput 处理旧单帧玩家输入
func (r *Room) handleInput(ctx context.Context, playerID uint64, input protocol.PlayerInput) {
	if r.gameEnded {
		return
	}
	syncState := r.ensureSyncState(playerID)
	targetTick := input.ClientTick
	if targetTick <= syncState.lastAppliedTick || targetTick < r.tick-r.syncConfig.RollbackWindowTicks || targetTick > r.tick+r.syncConfig.FutureInputWindowTicks {
		targetTick = r.tick + 1
	}
	if _, exists := syncState.inputs[targetTick]; exists {
		targetTick++
	}
	frame := protocol.PlayerInputFrame{
		ClientTick: targetTick,
		MoveX:      input.MoveX,
		MoveZ:      input.MoveZ,
		Yaw:        input.Yaw,
		Pitch:      input.Pitch,
		Fire:       input.Fire,
		Jump:       input.Jump,
	}
	r.handleInputBatch(ctx, playerID, protocol.PlayerInputBatch{BaseClientTick: targetTick, Frames: []protocol.PlayerInputFrame{frame}})
}

// handleInputBatch 处理批量玩家输入
func (r *Room) handleInputBatch(ctx context.Context, playerID uint64, batch protocol.PlayerInputBatch) {
	if r.gameEnded {
		return
	}
	player, exists := r.players[playerID]
	if !exists || player == nil || !player.Alive {
		return
	}
	syncState := r.ensureSyncState(playerID)
	if len(batch.Frames) == 0 {
		return
	}
	if len(batch.Frames) > r.syncConfig.MaxInputBatchFrames {
		glog.Warn(ctx, "reject oversized input batch", glog.String("room_id", r.id), glog.Uint64("player_id", playerID), glog.Int("frames", len(batch.Frames)))
		return
	}

	acceptedInBatch := false
	var latestLateInput authoritativeInput
	hasLateInput := false
	for _, frame := range batch.Frames {
		inputTick := frame.ClientTick
		if inputTick == 0 {
			inputTick = batch.BaseClientTick
		}
		if inputTick < r.tick-r.syncConfig.RollbackWindowTicks {
			r.logInputDiagnostic(ctx, syncState, inputDiagnosticStaleCorrection, playerID, inputTick, 0)
			r.sendCurrentCorrection(player, syncState, correctionReasonStaleInput)
			continue
		}
		if inputTick > r.tick+r.syncConfig.FutureInputWindowTicks {
			r.logInputDiagnostic(ctx, syncState, inputDiagnosticFutureDropped, playerID, inputTick, 0)
			continue
		}

		frame.ClientTick = inputTick
		sanitized, ok := sanitizePlayerInput(inputFrameToPlayerInput(frame))
		if !ok {
			continue
		}
		if inputTick <= syncState.lastAppliedTick {
			if r.canRescheduleLateInput(inputTick, syncState) && (!hasLateInput || inputTick > latestLateInput.ClientTick) {
				latestLateInput = sanitized
				hasLateInput = true
			} else {
				r.logInputDiagnostic(ctx, syncState, inputDiagnosticLateDropped, playerID, inputTick, 0)
			}
			continue
		}
		if _, exists := syncState.inputs[inputTick]; exists {
			continue
		}

		r.acceptInput(syncState, inputTick, inputTick, sanitized)
		r.logInputDiagnostic(ctx, syncState, inputDiagnosticAccepted, playerID, inputTick, inputTick)
		acceptedInBatch = true
		if frame.PredictedState != nil && predictedStateFinite(*frame.PredictedState) {
			syncState.predictedStates[inputTick] = *frame.PredictedState
		}
	}
	if acceptedInBatch || !hasLateInput {
		return
	}
	targetTick, ok := r.nextAvailableInputTick(syncState)
	if !ok {
		r.logInputDiagnostic(ctx, syncState, inputDiagnosticRescheduleDropped, playerID, latestLateInput.ClientTick, 0)
		return
	}
	originalLateTick := latestLateInput.ClientTick
	latestLateInput.ClientTick = targetTick
	r.acceptInput(syncState, targetTick, originalLateTick, latestLateInput)
	syncState.lateRescheduledTicks[targetTick] = true
	r.logInputDiagnostic(ctx, syncState, inputDiagnosticLateRescheduled, playerID, originalLateTick, targetTick)
}

// canRescheduleLateInput 判断轻微迟到输入是否还能排到后续 tick 执行
func (r *Room) canRescheduleLateInput(inputTick int64, syncState *playerSyncState) bool {
	if syncState == nil || inputTick > syncState.lastAppliedTick {
		return false
	}
	if inputTick <= syncState.lastAcceptedInputTick {
		return false
	}
	return inputTick >= r.tick-r.syncConfig.RollbackWindowTicks
}

// nextAvailableInputTick 获取下一帧可写入的输入 tick
func (r *Room) nextAvailableInputTick(syncState *playerSyncState) (int64, bool) {
	if syncState == nil {
		return 0, false
	}
	targetTick := r.tick + 1
	if syncState.lastAppliedTick+1 > targetTick {
		targetTick = syncState.lastAppliedTick + 1
	}
	maxTick := r.tick + r.syncConfig.FutureInputWindowTicks
	for targetTick <= maxTick {
		if _, exists := syncState.inputs[targetTick]; !exists {
			return targetTick, true
		}
		targetTick++
	}
	return 0, false
}

// acceptInput 写入服务端待执行输入并推进客户端输入确认 tick
func (r *Room) acceptInput(syncState *playerSyncState, executeTick int64, acceptedClientTick int64, input authoritativeInput) {
	input.ClientTick = executeTick
	syncState.inputs[executeTick] = input
	if acceptedClientTick > syncState.lastAcceptedInputTick {
		syncState.lastAcceptedInputTick = acceptedClientTick
	}
}

// logInputDiagnostic 按原因节流输出输入处理诊断日志
func (r *Room) logInputDiagnostic(ctx context.Context, syncState *playerSyncState, reason string, playerID uint64, inputTick int64, targetTick int64) {
	if syncState == nil {
		return
	}
	if syncState.lastInputDiagnosticTicks == nil {
		syncState.lastInputDiagnosticTicks = make(map[string]int64)
	}
	lastLoggedTick := syncState.lastInputDiagnosticTicks[reason]
	if lastLoggedTick > 0 && r.tick-lastLoggedTick < inputDiagnosticIntervalTicks {
		return
	}
	syncState.lastInputDiagnosticTicks[reason] = r.tick

	glog.Info(ctx, "room input diagnostic",
		glog.String("room_id", r.id),
		glog.Uint64("player_id", playerID),
		glog.String("reason", reason),
		glog.Int64("server_tick", r.tick),
		glog.Int64("input_tick", inputTick),
		glog.Int64("target_tick", targetTick),
		glog.Int64("last_applied_tick", syncState.lastAppliedTick),
		glog.Int64("last_accepted_input_tick", syncState.lastAcceptedInputTick),
	)
}

// update 更新房间状态并按频率广播快照，返回 true 表示房间已结束
func (r *Room) update(ctx context.Context) bool {
	if r.gameEnded {
		return true
	}
	r.tick++
	r.currentTick.Store(r.tick)
	r.updatePlayers(ctx)
	if r.shouldFinishGame() {
		r.finishGame(ctx)
		return true
	}
	if r.snapshotRate <= 0 {
		return false
	}
	intervalTicks := int64(r.tickRate / r.snapshotRate)
	if intervalTicks <= 0 {
		intervalTicks = 1
	}
	if r.tick-r.lastSnapshotAt < intervalTicks {
		return false
	}
	r.lastSnapshotAt = r.tick
	r.broadcastAcks(ctx)
	r.broadcastSnapshots(ctx)
	return false
}

// updatePlayers 按服务端固定 tick 推进玩家权威状态
func (r *Room) updatePlayers(ctx context.Context) {
	for playerID, player := range r.players {
		if player == nil || !player.Alive {
			continue
		}
		r.clearExpiredInvincibility(player)
		syncState := r.ensureSyncState(playerID)
		inputState, hasExactInput := r.inputForTick(syncState, r.tick)
		if hasExactInput || syncState.hasLastInput || !player.Grounded || player.VerticalVelocity != 0 {
			r.simulatePlayerTick(ctx, player, inputState, hasExactInput)
		}
		syncState.lastAppliedTick = r.tick
		r.saveAuthoritativeState(playerID, player)
		r.sendLateInputRescheduleCorrection(ctx, player, syncState, r.tick)
		r.verifyPredictedState(ctx, player, syncState, r.tick)
		r.cleanupSyncState(syncState)
	}
}

// clearExpiredInvincibility 清理已经过期的无敌状态
func (r *Room) clearExpiredInvincibility(player *Player) {
	if player == nil || player.InvincibleUntilTick == 0 || player.InvincibleUntilTick > r.tick {
		return
	}
	player.InvincibleUntilTick = 0
}

// inputForTick 获取当前服务端帧应使用的输入
func (r *Room) inputForTick(syncState *playerSyncState, tick int64) (authoritativeInput, bool) {
	if syncState == nil {
		return authoritativeInput{}, false
	}
	input, exists := syncState.inputs[tick]
	if exists {
		syncState.lastInput = input
		syncState.hasLastInput = true
		syncState.lastInputTick = tick
		return input, true
	}
	if syncState.hasLastInput && tick-syncState.lastInputTick <= r.syncConfig.MaxInputHoldTicks {
		held := syncState.lastInput
		held.ClientTick = tick
		held.Fire = false
		held.Jump = false
		return held, false
	}
	return authoritativeInput{}, false
}

// simulatePlayerTick 使用服务端权威输入模拟玩家一帧
func (r *Room) simulatePlayerTick(ctx context.Context, player *Player, input authoritativeInput, hasExactInput bool) {
	applyViewRotation(player, input)
	moveReq, ok := buildMovePlayerRequest(player, input, r.tickRate)
	if ok && shouldMovePlayer(moveReq) {
		// 由物理世界计算最终位置，避免逻辑层绕过碰撞规则
		result, err := r.physics.MovePlayer(moveReq)
		if err != nil {
			glog.Warn(ctx, "move physics player failed", glog.String("room_id", r.id), glog.Uint64("player_id", player.ID), glog.Err(err))
			r.sendCurrentCorrection(player, r.ensureSyncState(player.ID), "physics_error")
		} else {
			player.X = result.Position.X
			player.Y = result.Position.Y
			player.Z = result.Position.Z
			player.VerticalVelocity = result.VerticalVelocity
			player.Grounded = result.Grounded
		}
	}
	if hasExactInput && input.Fire {
		r.handlePlayerFire(ctx, player)
	}
}

// handlePlayerFire 处理玩家权威开火命中
func (r *Room) handlePlayerFire(ctx context.Context, shooter *Player) {
	if shooter == nil || !shooter.Alive {
		return
	}
	hit, err := r.physics.Raycast(RaycastRequest{
		Origin:         Vector3{X: shooter.X, Y: shooter.Y + defaultFireViewHeight, Z: shooter.Z},
		Direction:      viewDirection(shooter.Yaw, shooter.Pitch),
		MaxDistance:    defaultFireMaxDistance,
		IgnorePlayerID: shooter.ID,
	})
	if err != nil {
		glog.Warn(ctx, "player fire raycast failed", glog.String("room_id", r.id), glog.Uint64("player_id", shooter.ID), glog.Err(err))
		return
	}
	if !hit.Hit || hit.TargetID == 0 || hit.TargetID == shooter.ID {
		return
	}

	target := r.players[hit.TargetID]
	if target == nil || !target.Alive {
		return
	}
	r.applyFireDamage(ctx, shooter, target, hit)
}

// applyFireDamage 结算开火命中伤害
func (r *Room) applyFireDamage(ctx context.Context, shooter *Player, target *Player, hit RaycastHit) {
	if shooter == nil || target == nil || !target.Alive {
		return
	}
	if target.IsInvincible(r.tick) {
		glog.Info(ctx, "player fire ignored by invincibility", glog.String("room_id", r.id), glog.Uint64("shooter_player_id", shooter.ID), glog.Uint64("target_player_id", target.ID), glog.Int64("server_tick", r.tick), glog.Int64("invincible_until_tick", target.InvincibleUntilTick))
		return
	}
	target.HP -= defaultFireDamage
	if target.HP < 0 {
		target.HP = 0
	}
	glog.Info(ctx, "player fire hit", glog.String("room_id", r.id), glog.Uint64("shooter_player_id", shooter.ID), glog.Uint64("target_player_id", target.ID), glog.Int("target_hp", target.HP), glog.Float64("distance", hit.Distance))
	if target.HP > 0 {
		r.saveAuthoritativeState(target.ID, target)
		return
	}

	// 死亡只在存活状态切换时计数，避免重复命中重复累计
	if shooter.ID != target.ID {
		shooter.KillCount++
	}
	target.DeathCount++
	target.Alive = false
	if !r.respawnPlayerAtSpawn(ctx, target) {
		r.saveAuthoritativeState(target.ID, target)
		return
	}
	r.saveAuthoritativeState(target.ID, target)
	r.sendCurrentCorrection(target, r.ensureSyncState(target.ID), correctionReasonRespawn)
}

// respawnPlayerAtSpawn 将死亡玩家复活到原出生点
func (r *Room) respawnPlayerAtSpawn(ctx context.Context, player *Player) bool {
	if player == nil || player.SpawnID == "" {
		return false
	}
	spawnPoint, ok := r.spawnPointByID(player.SpawnID)
	if !ok {
		glog.Warn(ctx, "respawn spawn point not found", glog.String("room_id", r.id), glog.Uint64("player_id", player.ID), glog.String("spawn_id", player.SpawnID))
		return false
	}

	// 先同步物理位置，成功后再恢复逻辑存活状态
	if err := r.physics.SetPlayerPosition(player.ID, spawnPoint.Position); err != nil {
		if err != ErrPhysicsPlayerNotFound {
			glog.Warn(ctx, "respawn physics player failed", glog.String("room_id", r.id), glog.Uint64("player_id", player.ID), glog.Err(err))
			return false
		}
		if addErr := r.physics.AddPlayer(player.ID, spawnPoint.Position); addErr != nil {
			glog.Warn(ctx, "respawn add physics player failed", glog.String("room_id", r.id), glog.Uint64("player_id", player.ID), glog.Err(addErr))
			return false
		}
	}

	player.X = spawnPoint.Position.X
	player.Y = spawnPoint.Position.Y
	player.Z = spawnPoint.Position.Z
	player.Yaw = spawnPoint.Yaw
	player.Pitch = 0
	player.HP = defaultPlayerHP
	player.Alive = true
	player.InvincibleUntilTick = r.tick + durationToTicks(defaultRespawnInvincibleDuration, r.tickRate)
	player.VerticalVelocity = 0
	player.Grounded = true
	r.discardFutureSyncState(player.ID)
	glog.Info(ctx, "player respawned", glog.String("room_id", r.id), glog.Uint64("player_id", player.ID), glog.String("spawn_id", player.SpawnID), glog.Int64("server_tick", r.tick), glog.Int64("invincible_until_tick", player.InvincibleUntilTick))
	return true
}

// spawnPointByID 按出生点ID查询地图出生点
func (r *Room) spawnPointByID(spawnID string) (SpawnPoint, bool) {
	if spawnID == "" {
		return SpawnPoint{}, false
	}
	for _, spawnPoint := range r.physics.SpawnPoints() {
		if spawnPoint.ID == spawnID {
			return spawnPoint, true
		}
	}
	return SpawnPoint{}, false
}

// discardFutureSyncState 清理复活后不再适用的未来同步状态
func (r *Room) discardFutureSyncState(playerID uint64) {
	syncState := r.ensureSyncState(playerID)
	for tick := range syncState.inputs {
		if tick > r.tick {
			delete(syncState.inputs, tick)
		}
	}
	for tick := range syncState.predictedStates {
		if tick > r.tick {
			delete(syncState.predictedStates, tick)
		}
	}
	for tick := range syncState.lateRescheduledTicks {
		if tick > r.tick {
			delete(syncState.lateRescheduledTicks, tick)
		}
	}
	syncState.hasLastInput = false
	syncState.lastInput = authoritativeInput{}
	syncState.lastInputTick = 0
}

// saveAuthoritativeState 保存玩家当前权威状态
func (r *Room) saveAuthoritativeState(playerID uint64, player *Player) {
	syncState := r.ensureSyncState(playerID)
	syncState.authoritativeHistory[r.tick] = frameStateFromPlayer(r.tick, player)
}

// sendLateInputRescheduleCorrection 对迟到重排后的权威状态做低频重同步
func (r *Room) sendLateInputRescheduleCorrection(ctx context.Context, player *Player, syncState *playerSyncState, tick int64) {
	if syncState == nil || player == nil || !syncState.lateRescheduledTicks[tick] {
		return
	}
	if syncState.lastAcceptedInputTick+lateInputCorrectionDelayTicks >= tick {
		return
	}
	if tick-syncState.lastCorrectionTick < r.syncConfig.CorrectionMinIntervalTicks {
		return
	}
	authoritative, exists := syncState.authoritativeHistory[tick]
	if !exists {
		return
	}
	if err := r.sendCorrection(player, syncState, authoritative, correctionReasonLateInputReschedule, 0, 0); err != nil {
		glog.Warn(ctx, "send late input reschedule correction failed", glog.String("room_id", r.id), glog.Uint64("player_id", player.ID), glog.Err(err))
	}
}

// verifyPredictedState 校验客户端预测状态并在超阈值时纠偏
func (r *Room) verifyPredictedState(ctx context.Context, player *Player, syncState *playerSyncState, tick int64) {
	if !r.syncConfig.PredictionEnabled || syncState == nil || player == nil || player.SyncMode != SyncModePredictionAuthoritative {
		return
	}
	predicted, exists := syncState.predictedStates[tick]
	if !exists {
		return
	}
	if r.syncConfig.PredictionKeyframeInterval > 1 && tick%r.syncConfig.PredictionKeyframeInterval != 0 {
		return
	}
	authoritative, exists := syncState.authoritativeHistory[tick]
	if !exists {
		return
	}
	posError := positionError(predicted, authoritative)
	angError := angleError(predicted, authoritative)
	syncState.lastVerifiedTick = tick
	if posError <= r.syncConfig.PositionTolerance && angError <= r.syncConfig.AngleTolerance {
		return
	}

	reason := correctionReasonPositionError
	if posError <= r.syncConfig.PositionTolerance && angError > r.syncConfig.AngleTolerance {
		reason = correctionReasonAngleError
	}
	force := posError > r.syncConfig.HardPositionTolerance
	if !force && tick-syncState.lastCorrectionTick < r.syncConfig.CorrectionMinIntervalTicks {
		return
	}
	if err := r.sendCorrection(player, syncState, authoritative, reason, posError, angError); err != nil {
		glog.Warn(ctx, "send state correction failed", glog.String("room_id", r.id), glog.Uint64("player_id", player.ID), glog.Err(err))
	}
}

// sendCurrentCorrection 发送玩家当前权威状态纠偏
func (r *Room) sendCurrentCorrection(player *Player, syncState *playerSyncState, reason string) {
	if player == nil || syncState == nil || player.SyncMode != SyncModePredictionAuthoritative {
		return
	}
	state := frameStateFromPlayer(r.tick, player)
	if err := r.sendCorrection(player, syncState, state, reason, 0, 0); err != nil {
		return
	}
}

// sendCorrection 向玩家发送权威纠偏消息
func (r *Room) sendCorrection(player *Player, syncState *playerSyncState, state playerFrameState, reason string, posError float64, angError float64) error {
	message, err := protocol.NewJSONMessage(protocol.MsgStateCorrection, protocol.StateCorrection{
		PlayerID:              player.ID,
		RollbackTick:          state.Tick,
		ServerTick:            r.tick,
		LastAcceptedInputTick: syncState.lastAcceptedInputTick,
		State:                 state.toPlayerState(),
		Reason:                reason,
		PositionError:         posError,
		AngleError:            angError,
	})
	if err != nil {
		return err
	}
	if player.Session.Send(message) {
		syncState.lastCorrectionTick = r.tick
		glog.Info(context.Background(), "state correction sent",
			glog.String("room_id", player.RoomID),
			glog.Uint64("player_id", player.ID),
			glog.String("reason", reason),
			glog.Int64("rollback_tick", state.Tick),
			glog.Int64("server_tick", r.tick),
			glog.Int64("last_accepted_input_tick", syncState.lastAcceptedInputTick),
			glog.Float64("position_error", posError),
			glog.Float64("angle_error", angError),
		)
	}
	return nil
}

// broadcastAcks 按快照频率向玩家发送输入确认
func (r *Room) broadcastAcks(ctx context.Context) {
	for playerID, player := range r.players {
		if player == nil || player.SyncMode != SyncModePredictionAuthoritative {
			continue
		}
		syncState := r.ensureSyncState(playerID)
		message, err := protocol.NewJSONMessage(protocol.MsgInputAck, protocol.InputAck{
			ServerTick:            r.tick,
			LastAcceptedInputTick: syncState.lastAcceptedInputTick,
			LastVerifiedInputTick: syncState.lastVerifiedTick,
		})
		if err != nil {
			glog.Error(ctx, "build input ack failed", glog.String("room_id", r.id), glog.Err(err))
			continue
		}
		player.Session.Send(message)
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
		states = append(states, player.ToStateAt(r.tick))
		for _, visiblePlayer := range visible {
			states = append(states, visiblePlayer.ToStateAt(r.tick))
		}
		message, err := protocol.NewJSONMessage(protocol.MsgSnapshot, protocol.Snapshot{ServerTick: r.tick, Players: states})
		if err != nil {
			glog.Error(ctx, "build snapshot failed", glog.String("room_id", r.id), glog.Err(err))
			continue
		}
		if r.tick%int64(r.tickRate) == 0 {
			glog.Info(ctx, "room snapshot broadcast", glog.String("room_id", r.id), glog.Int64("server_tick", r.tick), glog.Uint64("receiver_player_id", player.ID), glog.Int("room_player_count", len(players)), glog.Int("visible_player_count", len(visible)), glog.Any("snapshot_player_ids", playerStateIDs(states)))
		}
		if !player.Session.SendSnapshot(message) {
			glog.Warn(ctx, "room snapshot send dropped", glog.String("room_id", r.id), glog.Int64("server_tick", r.tick), glog.Uint64("receiver_player_id", player.ID), glog.Int("snapshot_player_count", len(states)), glog.Any("snapshot_player_ids", playerStateIDs(states)))
		}
	}
}

// playerStateIDs 提取快照里的玩家ID用于诊断日志
func playerStateIDs(states []protocol.PlayerState) []uint64 {
	ids := make([]uint64, 0, len(states))
	for _, state := range states {
		ids = append(ids, state.PlayerID)
	}
	return ids
}

// ensureSyncState 获取或创建玩家同步状态
func (r *Room) ensureSyncState(playerID uint64) *playerSyncState {
	syncState := r.syncStates[playerID]
	if syncState == nil {
		syncState = newPlayerSyncState()
		r.syncStates[playerID] = syncState
	}
	return syncState
}

// cleanupSyncState 清理超出回滚窗口的同步历史
func (r *Room) cleanupSyncState(syncState *playerSyncState) {
	if syncState == nil {
		return
	}
	minTick := r.tick - r.syncConfig.RollbackWindowTicks
	for tick := range syncState.inputs {
		if tick < minTick || tick <= syncState.lastAppliedTick {
			delete(syncState.inputs, tick)
		}
	}
	for tick := range syncState.predictedStates {
		if tick < minTick || tick <= syncState.lastVerifiedTick-r.syncConfig.RollbackWindowTicks {
			delete(syncState.predictedStates, tick)
		}
	}
	for tick := range syncState.lateRescheduledTicks {
		if tick < minTick || tick <= syncState.lastAppliedTick {
			delete(syncState.lateRescheduledTicks, tick)
		}
	}
	for tick := range syncState.authoritativeHistory {
		if tick < minTick {
			delete(syncState.authoritativeHistory, tick)
		}
	}
}
