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
		MaxInputHoldTicks:          3,
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
