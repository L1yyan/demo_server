package config

import "testing"

// TestNormalizeMatchServerMaxPlayersPerRoom 验证 matchserver 默认使用 2 人房间
func TestNormalizeMatchServerMaxPlayersPerRoom(t *testing.T) {
	cfg := Config{
		MatchServer01: MatchServerConfig{
			RoomServers: []RoomServerNodeConfig{{ServerID: "room-01", ServerAddr: "127.0.0.1:9001"}},
		},
	}
	cfg.normalize()

	if cfg.MatchServer01.MaxPlayersPerRoom != 2 {
		t.Fatalf("expected default max players per room 2, got %d", cfg.MatchServer01.MaxPlayersPerRoom)
	}
	if cfg.MatchServer01.RoomServers[0].MaxPlayersPerRoom != 2 {
		t.Fatalf("expected roomserver max players per room 2, got %d", cfg.MatchServer01.RoomServers[0].MaxPlayersPerRoom)
	}
}
