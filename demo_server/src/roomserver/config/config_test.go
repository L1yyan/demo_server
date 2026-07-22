package config

import "testing"

// TestDefaultConfigMaxPlayersPerRoom 验证 roomserver 默认使用 2 人房间
func TestDefaultConfigMaxPlayersPerRoom(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxPlayersPerRoom != 2 {
		t.Fatalf("expected default max players per room 2, got %d", cfg.MaxPlayersPerRoom)
	}
}

// TestNormalizeMaxPlayersPerRoom 验证非法人数配置会回退到 2 人房间
func TestNormalizeMaxPlayersPerRoom(t *testing.T) {
	cfg := Config{MaxPlayersPerRoom: 0}.Normalize()
	if cfg.MaxPlayersPerRoom != 2 {
		t.Fatalf("expected normalized max players per room 2, got %d", cfg.MaxPlayersPerRoom)
	}
}
