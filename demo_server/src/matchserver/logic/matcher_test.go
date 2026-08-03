package logic

import (
	"context"
	"errors"
	"testing"
	"time"

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

// TestMatcherDuplicatePlayerReusesReservation 验证同玩家重复匹配不会重复占用房间名额
func TestMatcherDuplicatePlayerReusesReservation(t *testing.T) {
	matcher, err := NewMatcher(conf.MatchServerConfig{
		TokenSecret: "test-room-token-secret",
		TokenExpire: time.Minute,
		RoomServers: []conf.RoomServerNodeConfig{
			{ServerID: "room-01", ServerAddr: "127.0.0.1:9001", MaxRooms: 1},
		},
	})
	if err != nil {
		t.Fatalf("new matcher: %v", err)
	}
	matcher.now = func() time.Time { return time.Unix(100, 0) }

	first, err := matcher.AllocateRoom(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("allocate first player: %v", err)
	}
	retry, err := matcher.AllocateRoom(context.Background(), 1, "   ")
	if err != nil {
		t.Fatalf("retry first player: %v", err)
	}
	second, err := matcher.AllocateRoom(context.Background(), 2, "")
	if err != nil {
		t.Fatalf("allocate second player: %v", err)
	}

	if retry.RoomID != first.RoomID || retry.MatchID != first.MatchID || retry.RoomToken != first.RoomToken || retry.ExpireAt != first.ExpireAt {
		t.Fatalf("expected retry to reuse reservation, first=%+v retry=%+v", first, retry)
	}
	if second.RoomID != first.RoomID {
		t.Fatalf("expected duplicate reservation not to consume a slot, got second room %q first room %q", second.RoomID, first.RoomID)
	}
	if _, err := matcher.AllocateRoom(context.Background(), 3, ""); !errors.Is(err, ErrRoomServerFull) {
		t.Fatalf("expected room full after two distinct players, got %v", err)
	}
}

// TestMatcherExpiredReservationReleasesRoomSlot 验证过期占位会释放房间名额
func TestMatcherExpiredReservationReleasesRoomSlot(t *testing.T) {
	now := time.Unix(100, 0)
	matcher, err := NewMatcher(conf.MatchServerConfig{
		TokenSecret: "test-room-token-secret",
		TokenExpire: time.Second,
		RoomServers: []conf.RoomServerNodeConfig{
			{ServerID: "room-01", ServerAddr: "127.0.0.1:9001", MaxRooms: 1},
		},
	})
	if err != nil {
		t.Fatalf("new matcher: %v", err)
	}
	matcher.now = func() time.Time { return now }

	first, err := matcher.AllocateRoom(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("allocate first player: %v", err)
	}
	if _, err := matcher.AllocateRoom(context.Background(), 2, ""); err != nil {
		t.Fatalf("allocate second player: %v", err)
	}
	if _, err := matcher.AllocateRoom(context.Background(), 3, ""); !errors.Is(err, ErrRoomServerFull) {
		t.Fatalf("expected room full before reservations expire, got %v", err)
	}

	now = now.Add(2 * time.Second)
	third, err := matcher.AllocateRoom(context.Background(), 3, "")
	if err != nil {
		t.Fatalf("allocate third player after expiration: %v", err)
	}
	if third.RoomID != first.RoomID {
		t.Fatalf("expected expired slots to be reused in %q, got %q", first.RoomID, third.RoomID)
	}
	if len(matcher.reservations) != 1 {
		t.Fatalf("expected only one active reservation after purge, got %d", len(matcher.reservations))
	}
	if got := matcher.servers[0].rooms[0].reservationCount(); got != 1 {
		t.Fatalf("expected one room reservation after purge, got %d", got)
	}
}
