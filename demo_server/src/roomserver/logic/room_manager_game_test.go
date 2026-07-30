package logic

import (
	"testing"
	"time"
)

// TestRoomManagerCleansRoomAfterGameOver 验证对局结束后管理器清理房间索引
func TestRoomManagerCleansRoomAfterGameOver(t *testing.T) {
	manager := NewRoomManagerWithOptions(nil, 10, 2, 20, 10, SyncConfig{}, "", "", 50*time.Millisecond, nil, NewSimplePhysicsWorldFactory())
	room := NewRoomWithOptions("room-test", 2, 20, 10, nil, NewSimplePhysicsWorld(), SyncConfig{}, "", "", 50*time.Millisecond, manager.finishRoom)
	manager.rooms[room.ID()] = room
	manager.playerRooms[1] = room.ID()
	manager.playerRooms[2] = room.ID()

	room.finishGame(nil)

	if _, exists := manager.rooms[room.ID()]; exists {
		t.Fatal("expected room index cleaned after game over")
	}
	if _, exists := manager.playerRooms[1]; exists {
		t.Fatal("expected player A room index cleaned after game over")
	}
	if _, exists := manager.playerRooms[2]; exists {
		t.Fatal("expected player B room index cleaned after game over")
	}
}
