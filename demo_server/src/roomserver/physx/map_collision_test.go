package physx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadMapCollision 验证地图碰撞配置可以被加载和校验
func TestLoadMapCollision(t *testing.T) {
	path := writeTestMapCollision(t, "map_test", `
    {
      "id": "wall_test",
      "shape": "box",
      "position": [0, 1, 2],
      "rotation": [0, 0, 0, 1],
      "size": [1, 2, 3],
      "is_trigger": false
    }`)

	collision, err := loadMapCollision(path, "map_test")
	if err != nil {
		t.Fatalf("load map collision: %v", err)
	}
	if collision.MapID != "map_test" {
		t.Fatalf("unexpected map id: %s", collision.MapID)
	}
	if len(collision.Colliders) != 1 {
		t.Fatalf("expected 1 collider, got %d", len(collision.Colliders))
	}
}

// TestMapCollisionSpawnPoints 验证地图出生点会转换为 logic 出生点
func TestMapCollisionSpawnPoints(t *testing.T) {
	path := writeTestMapCollision(t, "map_test", `
    {
      "id": "wall_test",
      "shape": "box",
      "position": [0, 1, 2],
      "rotation": [0, 0, 0, 1],
      "size": [1, 2, 3],
      "is_trigger": false
    }`)

	collision, err := loadMapCollision(path, "map_test")
	if err != nil {
		t.Fatalf("load map collision: %v", err)
	}
	spawnPoints := toLogicSpawnPoints(collision.SpawnPoints)
	if len(spawnPoints) != 2 {
		t.Fatalf("expected 2 spawn points, got %d", len(spawnPoints))
	}
	if spawnPoints[0].ID != "spawn_a" || spawnPoints[0].Position.X != -4 || spawnPoints[0].Yaw != 0 {
		t.Fatalf("unexpected spawn a: %+v", spawnPoints[0])
	}
	if spawnPoints[1].ID != "spawn_b" || spawnPoints[1].Position.X != 4 || spawnPoints[1].Yaw != 180 {
		t.Fatalf("unexpected spawn b: %+v", spawnPoints[1])
	}
}

// TestLoadMapCollisionRejectUnsupportedShape 验证暂不支持的碰撞体类型会显式报错
func TestLoadMapCollisionRejectUnsupportedShape(t *testing.T) {
	path := writeTestMapCollision(t, "map_test", `
    {
      "id": "sphere_test",
      "shape": "sphere",
      "position": [0, 1, 2],
      "rotation": [0, 0, 0, 1],
      "radius": 1,
      "is_trigger": false
    }`)

	_, err := loadMapCollision(path, "map_test")
	if !errors.Is(err, ErrUnsupportedMapColliderShape) {
		t.Fatalf("expected unsupported shape error, got %v", err)
	}
}

// TestLoadMapCollisionMetadata 验证地图元数据读取复用碰撞文件校验
func TestLoadMapCollisionMetadata(t *testing.T) {
	path := writeTestMapCollision(t, "map_test", `
    {
      "id": "wall_test",
      "shape": "box",
      "position": [0, 1, 2],
      "rotation": [0, 0, 0, 1],
      "size": [1, 2, 3],
      "is_trigger": false
    }`)

	metadata, err := LoadMapCollisionMetadata(path, "map_test")
	if err != nil {
		t.Fatalf("load map metadata: %v", err)
	}
	if metadata.MapID != "map_test" || metadata.PhysicsHash != "sha256:test" || metadata.ColliderCount != 1 || metadata.SpawnPointCount != 2 {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
}

// writeTestMapCollision 写入测试用地图碰撞配置
func writeTestMapCollision(t *testing.T, mapID string, colliderJSON string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "collision.json")
	data := `{
  "map_id": "` + mapID + `",
  "map_version": 1,
  "physics_hash": "sha256:test",
  "units": "meter",
  "rotation": "quat_xyzw",
  "colliders": [` + colliderJSON + `],
  "spawn_points": [
    {
      "id": "spawn_a",
      "position": [-4, 0.1, 0],
      "rotation": [0, 0, 0, 1]
    },
    {
      "id": "spawn_b",
      "position": [4, 0.1, 0],
      "rotation": [0, 1, 0, 0]
    }
  ]
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write test map collision: %v", err)
	}
	return path
}
