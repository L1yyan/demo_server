package logic

import "testing"

// TestSimplePhysicsWorldMovePlayer 验证 simple 后端会维护玩家位置并按边界裁剪
func TestSimplePhysicsWorldMovePlayer(t *testing.T) {
	world := NewSimplePhysicsWorld()
	defer world.Close()

	if err := world.AddPlayer(1, Vector3{X: 99, Y: 0, Z: 0}); err != nil {
		t.Fatalf("add player: %v", err)
	}
	result, err := world.MovePlayer(MovePlayerRequest{PlayerID: 1, Direction: Vector3{X: 1}, Distance: 5})
	if err != nil {
		t.Fatalf("move player: %v", err)
	}
	if result.Position.X != defaultWorldLimit {
		t.Fatalf("expected x clamped to %.1f, got %.1f", defaultWorldLimit, result.Position.X)
	}
	if !result.Blocked {
		t.Fatal("expected movement to be blocked by world limit")
	}
}

// TestSimplePhysicsWorldInvalidRequest 验证 simple 后端拒绝非法物理请求
func TestSimplePhysicsWorldInvalidRequest(t *testing.T) {
	world := NewSimplePhysicsWorld()
	defer world.Close()

	if _, err := world.MovePlayer(MovePlayerRequest{PlayerID: 1, Direction: Vector3{X: 1}, Distance: 1}); err != ErrPhysicsPlayerNotFound {
		t.Fatalf("expected missing player error, got %v", err)
	}
	if err := world.AddPlayer(1, Vector3{}); err != nil {
		t.Fatalf("add player: %v", err)
	}
	if _, err := world.MovePlayer(MovePlayerRequest{PlayerID: 1, Direction: Vector3{X: 1}, Distance: -1}); err != ErrInvalidPhysicsRequest {
		t.Fatalf("expected invalid request error, got %v", err)
	}
	if _, err := world.Raycast(RaycastRequest{Origin: Vector3{}, Direction: Vector3{}, MaxDistance: 10}); err != ErrInvalidPhysicsRequest {
		t.Fatalf("expected invalid raycast error, got %v", err)
	}
}
