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

// TestDefaultConfigMapCollisionPath 验证默认地图碰撞文件路径已配置
func TestDefaultConfigMapCollisionPath(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.DefaultMapID != "map_001" {
		t.Fatalf("expected default map id map_001, got %s", cfg.DefaultMapID)
	}
	if cfg.MapCollisionPath != "configs/maps/map_001/collision.json" {
		t.Fatalf("unexpected map collision path: %s", cfg.MapCollisionPath)
	}
}

// TestDefaultConfigMaxInputHoldTicks 验证默认弱网输入沿用窗口
func TestDefaultConfigMaxInputHoldTicks(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxInputHoldTicks != 8 {
		t.Fatalf("expected default max input hold ticks 8, got %d", cfg.MaxInputHoldTicks)
	}
}
