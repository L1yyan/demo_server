package logic

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"demo_server/src/roomserver/protocol"
)

// TestDuplicateInputTickDoesNotOverrideProcessedInput 验证同 tick 重复输入不会覆盖先到输入
func TestDuplicateInputTickDoesNotOverrideProcessedInput(t *testing.T) {
	room := newPredictionTestRoom()
	player := newPredictionTestPlayer(1)
	room.handleJoin(context.Background(), player)

	room.handleInputBatch(context.Background(), player.ID, protocol.PlayerInputBatch{Frames: []protocol.PlayerInputFrame{
		{ClientTick: 1, MoveZ: 1, Yaw: 0, Pitch: 0},
		{ClientTick: 1, MoveZ: -1, Yaw: 0, Pitch: 0},
	}})
	room.update(context.Background())

	if math.Abs(player.Z-0.2) > 0.0001 {
		t.Fatalf("expected first input to move forward, got z %.4f", player.Z)
	}
}

// TestPredictionWithinToleranceDoesNotCorrect 验证预测误差在阈值内不会纠偏
func TestPredictionWithinToleranceDoesNotCorrect(t *testing.T) {
	room := newPredictionTestRoom()
	player := newPredictionTestPlayer(1)
	room.handleJoin(context.Background(), player)

	room.handleInputBatch(context.Background(), player.ID, protocol.PlayerInputBatch{Frames: []protocol.PlayerInputFrame{
		{ClientTick: 1, MoveZ: 0, Yaw: 0, Pitch: 0, PredictedState: &protocol.PredictedPlayerState{X: -4.05, Y: 0.1, Z: 0.05, Yaw: 0, Pitch: 0}},
	}})
	room.update(context.Background())

	if correctionMessage(t, player) != nil {
		t.Fatal("expected no correction within tolerance")
	}
}

// TestPredictionBeyondToleranceSendsCorrection 验证预测误差超阈值会下发纠偏
func TestPredictionBeyondToleranceSendsCorrection(t *testing.T) {
	room := newPredictionTestRoom()
	player := newPredictionTestPlayer(1)
	room.handleJoin(context.Background(), player)

	room.handleInputBatch(context.Background(), player.ID, protocol.PlayerInputBatch{Frames: []protocol.PlayerInputFrame{
		{ClientTick: 1, MoveZ: 0, Yaw: 0, Pitch: 0, PredictedState: &protocol.PredictedPlayerState{X: 10, Y: 0.1, Z: 0, Yaw: 0, Pitch: 0}},
	}})
	room.update(context.Background())

	correction := correctionMessage(t, player)
	if correction == nil {
		t.Fatal("expected state correction")
	}
	if correction.RollbackTick != 1 {
		t.Fatalf("expected rollback tick 1, got %d", correction.RollbackTick)
	}
	if correction.State.PlayerID != player.ID || correction.State.X != player.X || correction.State.Z != player.Z {
		t.Fatalf("unexpected correction state: %+v", correction.State)
	}
	if correction.PositionError <= room.syncConfig.PositionTolerance {
		t.Fatalf("expected position error beyond tolerance, got %.4f", correction.PositionError)
	}
}

// TestLateInputWithinHoldWindowIsRescheduled 验证弱网轻微迟到输入会排到后续tick执行
func TestLateInputWithinHoldWindowIsRescheduled(t *testing.T) {
	room := newPredictionTestRoom()
	player := newPredictionTestPlayer(1)
	room.handleJoin(context.Background(), player)

	room.update(context.Background())
	room.update(context.Background())
	room.handleInputBatch(context.Background(), player.ID, protocol.PlayerInputBatch{Frames: []protocol.PlayerInputFrame{
		{ClientTick: 1, MoveZ: 1, Yaw: 45, Pitch: 10},
	}})
	room.update(context.Background())

	if math.Abs(player.Yaw-45) > 0.0001 || math.Abs(player.Pitch-10) > 0.0001 {
		t.Fatalf("expected late input view applied, got yaw %.2f pitch %.2f", player.Yaw, player.Pitch)
	}
	if player.X <= -4 || player.Z <= 0 {
		t.Fatalf("expected late input movement applied, got x %.4f z %.4f", player.X, player.Z)
	}
}

// TestLateInputBeyondHoldWindowIsRescheduled 验证超过沿用窗口但仍在回滚窗口内的迟到输入会被重排
func TestLateInputBeyondHoldWindowIsRescheduled(t *testing.T) {
	room := newPredictionTestRoom()
	player := newPredictionTestPlayer(1)
	room.handleJoin(context.Background(), player)

	for tick := 0; tick < 8; tick++ {
		room.update(context.Background())
	}
	room.handleInputBatch(context.Background(), player.ID, protocol.PlayerInputBatch{Frames: []protocol.PlayerInputFrame{
		{ClientTick: 1, MoveZ: 1, Yaw: 45, Pitch: 10},
	}})
	room.update(context.Background())

	if math.Abs(player.Yaw-45) > 0.0001 || math.Abs(player.Pitch-10) > 0.0001 {
		t.Fatalf("expected late input view applied, got yaw %.2f pitch %.2f", player.Yaw, player.Pitch)
	}
	if player.X <= -4 || player.Z <= 0 {
		t.Fatalf("expected late input movement applied, got x %.4f z %.4f", player.X, player.Z)
	}
}

// TestLateInputRescheduleKeepsClientAckTick 验证迟到输入重排后仍按原始客户端tick确认
func TestLateInputRescheduleKeepsClientAckTick(t *testing.T) {
	room := newPredictionTestRoom()
	player := newPredictionTestPlayer(1)
	room.handleJoin(context.Background(), player)

	room.update(context.Background())
	room.update(context.Background())
	room.handleInputBatch(context.Background(), player.ID, protocol.PlayerInputBatch{Frames: []protocol.PlayerInputFrame{
		{ClientTick: 1, MoveZ: 0, Yaw: 0, Pitch: 0, PredictedState: &protocol.PredictedPlayerState{X: 10, Y: 0.1, Z: 0, Yaw: 0, Pitch: 0}},
	}})
	room.update(context.Background())

	syncState := room.ensureSyncState(player.ID)
	if syncState.lastAcceptedInputTick != 1 {
		t.Fatalf("expected last accepted client tick 1, got %d", syncState.lastAcceptedInputTick)
	}
	if _, exists := syncState.predictedStates[3]; exists {
		t.Fatal("expected rescheduled input not to bind old predicted state to target tick")
	}
	if correctionMessage(t, player) != nil {
		t.Fatal("expected no immediate reschedule correction while input stream is close to server tick")
	}
}

// TestLateInputRescheduleCorrectionWaitsForAckGap 验证重排纠偏只在确认tick明显落后时触发
func TestLateInputRescheduleCorrectionWaitsForAckGap(t *testing.T) {
	room := newPredictionTestRoom()
	player := newPredictionTestPlayer(1)
	room.handleJoin(context.Background(), player)

	for tick := 0; tick < 8; tick++ {
		room.update(context.Background())
	}
	room.handleInputBatch(context.Background(), player.ID, protocol.PlayerInputBatch{Frames: []protocol.PlayerInputFrame{
		{ClientTick: 1, MoveZ: 0, Yaw: 0, Pitch: 0, PredictedState: &protocol.PredictedPlayerState{X: 10, Y: 0.1, Z: 0, Yaw: 0, Pitch: 0}},
	}})
	room.update(context.Background())

	correction := correctionMessage(t, player)
	if correction == nil {
		t.Fatal("expected authority resync correction after sustained late input gap")
	}
	if correction.Reason != correctionReasonLateInputReschedule {
		t.Fatalf("expected late input reschedule correction, got %s", correction.Reason)
	}
	if correction.LastAcceptedInputTick != 1 {
		t.Fatalf("expected correction ack tick 1, got %d", correction.LastAcceptedInputTick)
	}
	if correction.RollbackTick != 9 {
		t.Fatalf("expected rollback tick 9, got %d", correction.RollbackTick)
	}
	if correction.PositionError != 0 || correction.AngleError != 0 {
		t.Fatalf("expected reschedule correction without prediction error, got pos %.4f angle %.4f", correction.PositionError, correction.AngleError)
	}
}

// newPredictionTestRoom 创建开启预测同步的测试房间
func newPredictionTestRoom() *Room {
	return NewRoomWithSync("room-test", 2, 20, 10, nil, NewSimplePhysicsWorld(), SyncConfig{
		PredictionEnabled:          true,
		RollbackWindowTicks:        60,
		FutureInputWindowTicks:     8,
		PredictionKeyframeInterval: 1,
		PositionTolerance:          0.15,
		HardPositionTolerance:      0.5,
		AngleTolerance:             2,
		MaxInputBatchFrames:        8,
		MaxInputHoldTicks:          8,
		CorrectionMinIntervalTicks: 2,
	}, "map_001", "sha256:test")
}

// newPredictionTestPlayer 创建声明预测能力的测试玩家
func newPredictionTestPlayer(playerID uint64) *Player {
	return &Player{ID: playerID, Session: &testSession{id: "session-test"}, SyncVersion: 1, PredictionEnabled: true, PhysicsHash: "sha256:test"}
}

// correctionMessage 返回测试玩家收到的最后一条纠偏消息
func correctionMessage(t *testing.T, player *Player) *protocol.StateCorrection {
	t.Helper()
	session, ok := player.Session.(*testSession)
	if !ok {
		t.Fatal("expected test session")
	}
	for index := len(session.messages) - 1; index >= 0; index-- {
		message := session.messages[index]
		if message.Type != protocol.MsgStateCorrection {
			continue
		}
		var correction protocol.StateCorrection
		if err := json.Unmarshal(message.Payload, &correction); err != nil {
			t.Fatalf("decode correction: %v", err)
		}
		return &correction
	}
	return nil
}
