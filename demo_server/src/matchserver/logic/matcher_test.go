package logic

import (
	"context"
	"errors"
	"testing"

	conf "demo_server/config"
)

// TestMatcherDefaultTwoPlayersPerRoom 验证匹配默认按 2 人房间分配
func TestMatcherDefaultTwoPlayersPerRoom(t *testing.T) {
	matcher, err := NewMatcher(conf.MatchServerConfig{
		TokenSecret: "test-room-token-secret",
		RoomServers: []conf.RoomServerNodeConfig{
			{ServerID: "room-01", ServerAddr: "127.0.0.1:9001", MaxRooms: 2},
		},
	})
	if err != nil {
		t.Fatalf("new matcher: %v", err)
	}

	first, err := matcher.AllocateRoom(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("allocate first player: %v", err)
	}
	second, err := matcher.AllocateRoom(context.Background(), 2, "")
	if err != nil {
		t.Fatalf("allocate second player: %v", err)
	}
	third, err := matcher.AllocateRoom(context.Background(), 3, "")
	if err != nil {
		t.Fatalf("allocate third player: %v", err)
	}

	if first.RoomID != second.RoomID {
		t.Fatalf("expected first two players in same room, got %q and %q", first.RoomID, second.RoomID)
	}
	if third.RoomID == first.RoomID {
		t.Fatalf("expected third player in new room, got %q", third.RoomID)
	}
}

// TestMatcherDefaultTwoPlayersRoomFull 验证单房间默认第 3 人会触发满房
func TestMatcherDefaultTwoPlayersRoomFull(t *testing.T) {
	matcher, err := NewMatcher(conf.MatchServerConfig{
		TokenSecret: "test-room-token-secret",
		RoomServers: []conf.RoomServerNodeConfig{
			{ServerID: "room-01", ServerAddr: "127.0.0.1:9001", MaxRooms: 1},
		},
	})
	if err != nil {
		t.Fatalf("new matcher: %v", err)
	}

	for playerID := uint64(1); playerID <= 2; playerID++ {
		if _, err := matcher.AllocateRoom(context.Background(), playerID, ""); err != nil {
			t.Fatalf("allocate player %d: %v", playerID, err)
		}
	}
	if _, err := matcher.AllocateRoom(context.Background(), 3, ""); !errors.Is(err, ErrRoomServerFull) {
		t.Fatalf("expected roomserver full, got %v", err)
	}
}
