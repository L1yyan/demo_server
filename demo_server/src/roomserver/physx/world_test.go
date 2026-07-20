//go:build physx

package physx

import (
	"math"
	"testing"

	"demo_server/src/roomserver/logic"
)

const testFloatTolerance = 0.05 // 浮点结果允许误差

// TestWorldMoveAndRaycast 验证 PhysX world 能创建玩家、推进位置并被射线命中
func TestWorldMoveAndRaycast(t *testing.T) {
	world := newTestWorld(t)
	defer closeTestWorld(t, world)

	if err := world.AddPlayer(1, logic.Vector3{X: 0, Y: 0, Z: 0}); err != nil {
		t.Fatalf("add player: %v", err)
	}

	moveResult, err := world.MovePlayer(logic.MovePlayerRequest{
		PlayerID:  1,
		Direction: logic.Vector3{Z: 1},
		Distance:  1,
	})
	if err != nil {
		t.Fatalf("move player: %v", err)
	}
	if math.Abs(moveResult.Position.X) > testFloatTolerance {
		t.Fatalf("unexpected x after move: %.4f", moveResult.Position.X)
	}
	if math.Abs(moveResult.Position.Y) > testFloatTolerance {
		t.Fatalf("unexpected y after move: %.4f", moveResult.Position.Y)
	}
	if math.Abs(moveResult.Position.Z-1) > testFloatTolerance {
		t.Fatalf("unexpected z after move: %.4f", moveResult.Position.Z)
	}

	// 从玩家前方水平发射射线，验证 capsule actor 能被 PhysX 查询命中
	hit, err := world.Raycast(logic.RaycastRequest{
		Origin:      logic.Vector3{X: 0, Y: 0.9, Z: -5},
		Direction:   logic.Vector3{Z: 1},
		MaxDistance: 10,
	})
	if err != nil {
		t.Fatalf("raycast player: %v", err)
	}
	if !hit.Hit {
		t.Fatal("expected raycast hit player")
	}
	if hit.TargetID != 1 {
		t.Fatalf("unexpected target id: %d", hit.TargetID)
	}
	if hit.Distance <= 0 || hit.Distance > 10 {
		t.Fatalf("unexpected hit distance: %.4f", hit.Distance)
	}
}

// TestWorldRemovePlayer 验证移除玩家后 actor 不再被 raycast 命中
func TestWorldRemovePlayer(t *testing.T) {
	world := newTestWorld(t)
	defer closeTestWorld(t, world)

	if err := world.AddPlayer(1, logic.Vector3{X: 0, Y: 0, Z: 0}); err != nil {
		t.Fatalf("add player: %v", err)
	}
	if err := world.RemovePlayer(1); err != nil {
		t.Fatalf("remove player: %v", err)
	}

	hit, err := world.Raycast(logic.RaycastRequest{
		Origin:      logic.Vector3{X: 0, Y: 0.9, Z: -5},
		Direction:   logic.Vector3{Z: 1},
		MaxDistance: 10,
	})
	if err != nil {
		t.Fatalf("raycast after remove: %v", err)
	}
	if hit.Hit {
		t.Fatalf("expected no player hit after remove, got target %d", hit.TargetID)
	}
}

// TestWorldInvalidRequests 验证 PhysX 后端会拒绝非法请求
func TestWorldInvalidRequests(t *testing.T) {
	world := newTestWorld(t)
	defer closeTestWorld(t, world)

	if err := world.AddPlayer(1, logic.Vector3{X: 0, Y: 0, Z: 0}); err != nil {
		t.Fatalf("add player: %v", err)
	}
	if _, err := world.MovePlayer(logic.MovePlayerRequest{PlayerID: 1, Direction: logic.Vector3{X: math.NaN()}, Distance: 1}); err == nil {
		t.Fatal("expected invalid move request error")
	}
	if _, err := world.Raycast(logic.RaycastRequest{Origin: logic.Vector3{}, Direction: logic.Vector3{}, MaxDistance: 10}); err == nil {
		t.Fatal("expected invalid raycast request error")
	}
}

// newTestWorld 创建测试用 PhysX world
func newTestWorld(t *testing.T) logic.PhysicsWorld {
	t.Helper()
	factory := NewFactory(Config{PlayerCapsuleRadius: 0.35, PlayerCapsuleHeight: 1.8, CreateGroundPlane: true})
	world, err := factory.NewWorld("physx-test")
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	return world
}

// closeTestWorld 关闭测试用 PhysX world
func closeTestWorld(t *testing.T, world logic.PhysicsWorld) {
	t.Helper()
	if err := world.Close(); err != nil {
		t.Fatalf("close world: %v", err)
	}
}
