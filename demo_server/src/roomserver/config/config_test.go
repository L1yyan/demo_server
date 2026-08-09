package config

import (
	"testing"
	"time"
)

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
	if cfg.DefaultMapID != "mfps_arena" {
		t.Fatalf("expected default map id mfps_arena, got %s", cfg.DefaultMapID)
	}
	if cfg.MapCollisionPath != "config/maps/mfps_arena/collision.json" {
		t.Fatalf("unexpected map collision path: %s", cfg.MapCollisionPath)
	}
}

// TestDefaultConfigPhysXPVD 验证默认 PhysX PVD 配置只保留连接参数不主动启用
func TestDefaultConfigPhysXPVD(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.PhysXPVDEnabled {
		t.Fatal("expected physx pvd disabled by default")
	}
	if cfg.PhysXPVDHost != DefaultPhysXPVDHost || cfg.PhysXPVDPort != DefaultPhysXPVDPort || cfg.PhysXPVDTimeoutMS != DefaultPhysXPVDTimeoutMS {
		t.Fatalf("unexpected physx pvd config: host=%s port=%d timeout=%d", cfg.PhysXPVDHost, cfg.PhysXPVDPort, cfg.PhysXPVDTimeoutMS)
	}
}

// TestNormalizePhysXPVD 验证非法 PhysX PVD 连接参数会回退默认值
func TestNormalizePhysXPVD(t *testing.T) {
	cfg := Config{PhysXPVDEnabled: true, PhysXPVDHost: "  ", PhysXPVDPort: -1, PhysXPVDTimeoutMS: 0}.Normalize()
	if !cfg.PhysXPVDEnabled {
		t.Fatal("expected physx pvd enabled flag preserved")
	}
	if cfg.PhysXPVDHost != DefaultPhysXPVDHost || cfg.PhysXPVDPort != DefaultPhysXPVDPort || cfg.PhysXPVDTimeoutMS != DefaultPhysXPVDTimeoutMS {
		t.Fatalf("unexpected normalized physx pvd config: host=%s port=%d timeout=%d", cfg.PhysXPVDHost, cfg.PhysXPVDPort, cfg.PhysXPVDTimeoutMS)
	}
}

// TestDefaultConfigMaxInputHoldTicks 验证默认弱网输入沿用窗口
func TestDefaultConfigMaxInputHoldTicks(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxInputHoldTicks != 8 {
		t.Fatalf("expected default max input hold ticks 8, got %d", cfg.MaxInputHoldTicks)
	}
}

// TestDefaultConfigGameDuration 验证默认对局时长为3分钟
func TestDefaultConfigGameDuration(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.GameDuration != 3*time.Minute {
		t.Fatalf("expected default game duration 3m, got %s", cfg.GameDuration)
	}
}

// TestNormalizeGameDuration 验证非法对局时长会回退默认值
func TestNormalizeGameDuration(t *testing.T) {
	cfg := Config{GameDuration: 0}.Normalize()
	if cfg.GameDuration != 3*time.Minute {
		t.Fatalf("expected normalized game duration 3m, got %s", cfg.GameDuration)
	}
}
