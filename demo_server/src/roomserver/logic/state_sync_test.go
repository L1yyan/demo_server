package logic

import (
	"context"
	"testing"

	roompb "demo_server/gen/room"
	"demo_server/src/roomserver/protocol"
)

type noopSession struct{}

// ID 返回测试会话ID
func (noopSession) ID() string { return "noop-session" }

// Send 丢弃测试控制消息
func (noopSession) Send(protocol.Message) bool { return true }

// SendSnapshot 丢弃测试快照消息
func (noopSession) SendSnapshot(protocol.Message) bool { return true }

// Close 关闭测试会话
func (noopSession) Close() {}

// TestServerAuthoritativeInputQueuedByReceiveOrder 验证纯状态同步按服务端收到顺序排帧
func TestServerAuthoritativeInputQueuedByReceiveOrder(t *testing.T) {
	room := NewRoomWithOptions("room-test", 2, 20, 10, nil, NewSimplePhysicsWorld(), SyncConfig{MaxInputHoldTicks: 1}, "", "", defaultGameDuration, nil)
	player := &Player{ID: 1, RoomID: room.ID(), Session: noopSession{}, Alive: true, Grounded: true, HP: defaultPlayerHP}
	spawnPoint := room.physics.SpawnPoints()[0]
	player.X = spawnPoint.Position.X
	player.Y = spawnPoint.Position.Y
	player.Z = spawnPoint.Position.Z
	if err := room.physics.AddPlayer(player.ID, spawnPoint.Position); err != nil {
		t.Fatalf("add player: %v", err)
	}
	room.players[player.ID] = player
	room.syncStates[player.ID] = newPlayerSyncState()

	room.handleInput(context.Background(), player.ID, &roompb.PlayerInput{ClientTick: 9999, MoveZ: 1, Yaw: 0})
	room.handleInput(context.Background(), player.ID, &roompb.PlayerInput{ClientTick: -9999, MoveX: 1, Yaw: 0})

	syncState := room.ensureSyncState(player.ID)
	if _, exists := syncState.inputs[1]; !exists {
		t.Fatal("expected first received input queued for next server tick")
	}
	if _, exists := syncState.inputs[2]; !exists {
		t.Fatal("expected second received input queued for following server tick")
	}
	if syncState.inputs[1].ClientTick != 1 || syncState.inputs[2].ClientTick != 2 {
		t.Fatalf("expected inputs rewritten to server execute ticks, got %d and %d", syncState.inputs[1].ClientTick, syncState.inputs[2].ClientTick)
	}
}

// TestServerAuthoritativeInputDrivesMovement 验证无本地状态上报时输入仍驱动服务端权威移动
func TestServerAuthoritativeInputDrivesMovement(t *testing.T) {
	room := NewRoomWithOptions("room-test", 2, 20, 10, nil, NewSimplePhysicsWorld(), SyncConfig{}, "", "", defaultGameDuration, nil)
	player := &Player{ID: 1, RoomID: room.ID(), Session: noopSession{}, Alive: true, Grounded: true, HP: defaultPlayerHP}
	spawnPoint := room.physics.SpawnPoints()[0]
	player.X = spawnPoint.Position.X
	player.Y = spawnPoint.Position.Y
	player.Z = spawnPoint.Position.Z
	if err := room.physics.AddPlayer(player.ID, spawnPoint.Position); err != nil {
		t.Fatalf("add player: %v", err)
	}
	room.players[player.ID] = player
	room.syncStates[player.ID] = newPlayerSyncState()

	room.handleInput(context.Background(), player.ID, &roompb.PlayerInput{MoveZ: 1, Yaw: 0})
	room.update(context.Background())

	if player.Z <= spawnPoint.Position.Z {
		t.Fatalf("expected server authoritative movement on z axis, got %.4f from %.4f", player.Z, spawnPoint.Position.Z)
	}
}
