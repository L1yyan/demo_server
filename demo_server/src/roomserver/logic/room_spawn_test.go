package logic

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"demo_server/src/roomserver/protocol"
)

// testSession 记录测试过程中发送给玩家的消息
type testSession struct {
	id       string
	messages []protocol.Message
	closed   bool
}

// ID 返回测试会话ID
func (s *testSession) ID() string {
	return s.id
}

// Send 记录待发送消息
func (s *testSession) Send(message protocol.Message) bool {
	s.messages = append(s.messages, message)
	return true
}

// SendSnapshot 记录待发送快照消息
func (s *testSession) SendSnapshot(message protocol.Message) bool {
	s.messages = append(s.messages, message)
	return true
}

// Close 关闭测试会话
func (s *testSession) Close() {
	s.closed = true
}

// TestRoomJoinAssignsSpawnAAndB 验证两名玩家分别分配到 A/B 出生点
func TestRoomJoinAssignsSpawnAAndB(t *testing.T) {
	room := NewRoom("room-test", 2, 20, 10, nil, NewSimplePhysicsWorld())
	playerA := &Player{ID: 1, Session: &testSession{id: "session-a"}}
	playerB := &Player{ID: 2, Session: &testSession{id: "session-b"}}

	room.handleJoin(context.Background(), playerA)
	room.handleJoin(context.Background(), playerB)

	assertJoinOK(t, playerA)
	assertJoinOK(t, playerB)
	if playerA.SpawnID != defaultSpawnAID {
		t.Fatalf("expected player A spawn %s, got %s", defaultSpawnAID, playerA.SpawnID)
	}
	if playerB.SpawnID != defaultSpawnBID {
		t.Fatalf("expected player B spawn %s, got %s", defaultSpawnBID, playerB.SpawnID)
	}
	if playerA.X != -4 || playerA.Y != 0.1 || playerA.Z != 0 {
		t.Fatalf("unexpected player A position: %.2f %.2f %.2f", playerA.X, playerA.Y, playerA.Z)
	}
	if playerB.X != 4 || playerB.Y != 0.1 || playerB.Z != 0 {
		t.Fatalf("unexpected player B position: %.2f %.2f %.2f", playerB.X, playerB.Y, playerB.Z)
	}
	if playerA.Yaw != 0 || playerB.Yaw != 180 {
		t.Fatalf("unexpected spawn yaw: A %.2f B %.2f", playerA.Yaw, playerB.Yaw)
	}
}

// TestRoomJoinReusesFreedSpawnPoint 验证玩家离开后出生点可被新玩家复用
func TestRoomJoinReusesFreedSpawnPoint(t *testing.T) {
	room := NewRoom("room-test", 3, 20, 10, nil, NewSimplePhysicsWorld())
	playerA := &Player{ID: 1, Session: &testSession{id: "session-a"}}
	playerB := &Player{ID: 2, Session: &testSession{id: "session-b"}}
	playerC := &Player{ID: 3, Session: &testSession{id: "session-c"}}

	room.handleJoin(context.Background(), playerA)
	room.handleJoin(context.Background(), playerB)
	room.handleLeave(context.Background(), playerA.ID)
	room.handleJoin(context.Background(), playerC)

	assertJoinOK(t, playerC)
	if playerC.SpawnID != defaultSpawnAID {
		t.Fatalf("expected player C reuse spawn %s, got %s", defaultSpawnAID, playerC.SpawnID)
	}
	if playerC.X != -4 || playerC.Y != 0.1 || playerC.Z != 0 {
		t.Fatalf("unexpected player C position: %.2f %.2f %.2f", playerC.X, playerC.Y, playerC.Z)
	}
}

// TestPlayerStateIncludesSpawnID 验证快照玩家状态携带出生点ID
func TestPlayerStateIncludesSpawnID(t *testing.T) {
	player := &Player{ID: 1, SpawnID: defaultSpawnAID, X: -4, Y: 0.1, Z: 0, HP: 100}

	state := player.ToState()

	if state.SpawnID != defaultSpawnAID {
		t.Fatalf("expected state spawn %s, got %s", defaultSpawnAID, state.SpawnID)
	}
}

// TestRoomGameStartsWhenRoomIsFull 验证房间满员后开始对局倒计时
func TestRoomGameStartsWhenRoomIsFull(t *testing.T) {
	room := NewRoomWithOptions("room-test", 2, 20, 10, nil, NewSimplePhysicsWorld(), SyncConfig{}, "", "", 3*time.Minute, nil)
	playerA := &Player{ID: 1, Session: &testSession{id: "session-a"}}
	playerB := &Player{ID: 2, Session: &testSession{id: "session-b"}}

	room.handleJoin(context.Background(), playerA)
	if room.gameStarted {
		t.Fatal("expected game not started before room is full")
	}
	room.handleJoin(context.Background(), playerB)

	if !room.gameStarted {
		t.Fatal("expected game started after room is full")
	}
	if room.gameStartTick != 0 || room.gameEndTick != 3600 {
		t.Fatalf("unexpected game ticks: start %d end %d", room.gameStartTick, room.gameEndTick)
	}
	ack := decodeJoinAck(t, playerB)
	if !ack.GameStarted || ack.GameStartTick != room.gameStartTick || ack.GameEndTick != room.gameEndTick || ack.GameDurationSeconds != 180 {
		t.Fatalf("unexpected game fields in join ack: %+v", ack)
	}
	assertGameStart(t, playerA, room)
	assertGameStart(t, playerB, room)
}

// TestRoomGameOverAfterDuration 验证达到限时后广播对局结束
func TestRoomGameOverAfterDuration(t *testing.T) {
	room := NewRoomWithOptions("room-test", 2, 20, 10, nil, NewSimplePhysicsWorld(), SyncConfig{}, "", "", 100*time.Millisecond, nil)
	playerA := &Player{ID: 1, Session: &testSession{id: "session-a"}}
	playerB := &Player{ID: 2, Session: &testSession{id: "session-b"}}

	room.handleJoin(context.Background(), playerA)
	room.handleJoin(context.Background(), playerB)

	for !room.gameEnded {
		if room.update(context.Background()) {
			break
		}
	}
	if !room.gameEnded {
		t.Fatal("expected game ended")
	}
	over := gameOverMessage(t, playerA)
	if over == nil {
		t.Fatal("expected game over message")
	}
	if over.Reason != gameOverReasonTimeLimit || over.ServerTick != room.gameEndTick {
		t.Fatalf("unexpected game over: %+v", over)
	}
	if len(over.Players) != 2 {
		t.Fatalf("expected two final player states, got %d", len(over.Players))
	}
}

// TestRoomRejectsJoinAfterGameStarted 验证对局开始后拒绝新玩家加入
func TestRoomRejectsJoinAfterGameStarted(t *testing.T) {
	room := NewRoom("room-test", 2, 20, 10, nil, NewSimplePhysicsWorld())
	playerA := &Player{ID: 1, Session: &testSession{id: "session-a"}}
	playerB := &Player{ID: 2, Session: &testSession{id: "session-b"}}
	playerC := &Player{ID: 3, Session: &testSession{id: "session-c"}}

	room.handleJoin(context.Background(), playerA)
	room.handleJoin(context.Background(), playerB)
	room.handleJoin(context.Background(), playerC)

	ack := decodeJoinAck(t, playerC)
	if ack.OK || ack.Content != "game already started" {
		t.Fatalf("expected game already started failure, got %+v", ack)
	}
}

// assertGameStart 校验玩家收到对局开始通知
func assertGameStart(t *testing.T, player *Player, room *Room) {
	t.Helper()
	session, ok := player.Session.(*testSession)
	if !ok {
		t.Fatal("expected test session")
	}
	for index := len(session.messages) - 1; index >= 0; index-- {
		message := session.messages[index]
		if message.Type != protocol.MsgGameStart {
			continue
		}
		var start protocol.GameStart
		if err := json.Unmarshal(message.Payload, &start); err != nil {
			t.Fatalf("decode game start: %v", err)
		}
		if start.StartTick != room.gameStartTick || start.EndTick != room.gameEndTick || start.DurationSeconds != room.gameDurationSeconds() {
			t.Fatalf("unexpected game start: %+v", start)
		}
		return
	}
	t.Fatal("expected game start message")
}

// gameOverMessage 返回测试玩家收到的对局结束消息
func gameOverMessage(t *testing.T, player *Player) *protocol.GameOver {
	t.Helper()
	session, ok := player.Session.(*testSession)
	if !ok {
		t.Fatal("expected test session")
	}
	for index := len(session.messages) - 1; index >= 0; index-- {
		message := session.messages[index]
		if message.Type != protocol.MsgGameOver {
			continue
		}
		var over protocol.GameOver
		if err := json.Unmarshal(message.Payload, &over); err != nil {
			t.Fatalf("decode game over: %v", err)
		}
		return &over
	}
	return nil
}

// assertJoinOK 校验玩家收到入房成功响应
func assertJoinOK(t *testing.T, player *Player) {
	t.Helper()
	ack := decodeJoinAck(t, player)
	if !ack.OK {
		t.Fatalf("expected join ok, got %s", ack.Content)
	}
	if ack.SpawnID != player.SpawnID {
		t.Fatalf("expected ack spawn %s, got %s", player.SpawnID, ack.SpawnID)
	}
	if ack.X != player.X || ack.Y != player.Y || ack.Z != player.Z {
		t.Fatalf("unexpected ack position: %.2f %.2f %.2f", ack.X, ack.Y, ack.Z)
	}
	if ack.Yaw != player.Yaw || ack.Pitch != player.Pitch {
		t.Fatalf("unexpected ack view: yaw %.2f pitch %.2f", ack.Yaw, ack.Pitch)
	}
}

// decodeJoinAck 解码测试玩家最近一次入房响应
func decodeJoinAck(t *testing.T, player *Player) protocol.JoinRoomAck {
	t.Helper()
	session, ok := player.Session.(*testSession)
	if !ok {
		t.Fatal("expected test session")
	}
	for index := len(session.messages) - 1; index >= 0; index-- {
		message := session.messages[index]
		if message.Type != protocol.MsgJoinRoomAck {
			continue
		}
		var ack protocol.JoinRoomAck
		if err := json.Unmarshal(message.Payload, &ack); err != nil {
			t.Fatalf("decode join ack: %v", err)
		}
		return ack
	}
	t.Fatal("expected join ack message")
	return protocol.JoinRoomAck{}
}
