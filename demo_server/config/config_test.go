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

// TestLoadRoomServerConfig 验证全局配置能读取 roomserver 配置
func TestLoadRoomServerConfig(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.RoomServer01.ServerID != "room-01" {
		t.Fatalf("unexpected roomserver id: %s", cfg.RoomServer01.ServerID)
	}
	if cfg.RoomServer01.DefaultMapID != "mfps_arena" {
		t.Fatalf("unexpected roomserver map id: %s", cfg.RoomServer01.DefaultMapID)
	}
	if cfg.RoomServer01.MapCollisionPath != "config/maps/mfps_arena/collision.json" {
		t.Fatalf("unexpected roomserver collision path: %s", cfg.RoomServer01.MapCollisionPath)
	}
	if cfg.RoomServer01.MaxInputHoldTicks != 3 {
		t.Fatalf("expected yaml max input hold ticks 3, got %d", cfg.RoomServer01.MaxInputHoldTicks)
	}
}
