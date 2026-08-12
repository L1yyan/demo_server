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

	// 射击查询需要忽略发起者自身，避免从视角位置直接命中自己
	selfHit, err := world.Raycast(logic.RaycastRequest{
		Origin:         logic.Vector3{X: 0, Y: 0.9, Z: 1},
		Direction:      logic.Vector3{Z: 1},
		MaxDistance:    10,
		IgnorePlayerID: 1,
	})
	if err != nil {
		t.Fatalf("raycast ignore self: %v", err)
	}
	if selfHit.Hit {
		t.Fatalf("expected self ignored by raycast, got target %d", selfHit.TargetID)
	}
}

// TestWorldJump 验证 PhysX 后端支持玩家跳跃位移
func TestWorldJump(t *testing.T) {
	world := newTestWorld(t)
	defer closeTestWorld(t, world)

	if err := world.AddPlayer(1, logic.Vector3{X: 0, Y: 0, Z: 0}); err != nil {
		t.Fatalf("add player: %v", err)
	}
	jumpResult, err := world.MovePlayer(logic.MovePlayerRequest{PlayerID: 1, DeltaTime: 0.05, Jump: true, Grounded: true})
	if err != nil {
		t.Fatalf("jump player: %v", err)
	}
	if jumpResult.Position.Y <= 0 {
		t.Fatalf("expected player above ground after jump, got y %.4f", jumpResult.Position.Y)
	}
	if jumpResult.VerticalVelocity <= 0 || jumpResult.Grounded {
		t.Fatalf("unexpected jump state: velocity %.4f grounded %v", jumpResult.VerticalVelocity, jumpResult.Grounded)
	}
}

// TestWorldSetGetPlayerPosition 验证 PhysX 后端支持玩家位置读写
func TestWorldSetGetPlayerPosition(t *testing.T) {
	world := newTestWorld(t)
	defer closeTestWorld(t, world)

	if err := world.AddPlayer(1, logic.Vector3{}); err != nil {
		t.Fatalf("add player: %v", err)
	}
	if err := world.SetPlayerPosition(1, logic.Vector3{X: 2, Y: 0.2, Z: -3}); err != nil {
		t.Fatalf("set player position: %v", err)
	}
	position, err := world.GetPlayerPosition(1)
	if err != nil {
		t.Fatalf("get player position: %v", err)
	}
	if math.Abs(position.X-2) > testFloatTolerance || math.Abs(position.Y-0.2) > testFloatTolerance || math.Abs(position.Z+3) > testFloatTolerance {
		t.Fatalf("unexpected player position: %+v", position)
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

// TestWorldStaticMapCollision 验证地图静态 box 会阻挡玩家移动
func TestWorldStaticMapCollision(t *testing.T) {
	wallMapPath := writeTestMapCollision(t, "map_test", `
    {
      "id": "wall_test",
      "shape": "box",
      "position": [0, 1, 2],
      "rotation": [0, 0, 0, 1],
      "size": [10, 4, 0.5],
      "is_trigger": false
    }`)
	world := newTestWorldWithMap(t, wallMapPath)
	defer closeTestWorld(t, world)

	if err := world.AddPlayer(1, logic.Vector3{X: 0, Y: 0, Z: 0}); err != nil {
		t.Fatalf("add player: %v", err)
	}
	moveResult, err := world.MovePlayer(logic.MovePlayerRequest{
		PlayerID:  1,
		Direction: logic.Vector3{Z: 1},
		Distance:  5,
	})
	if err != nil {
		t.Fatalf("move player: %v", err)
	}
	if !moveResult.Blocked {
		t.Fatal("expected movement to be blocked by map wall")
	}
	if moveResult.Position.Z >= 2 {
		t.Fatalf("expected player to stop before wall, got z %.4f", moveResult.Position.Z)
	}
}

// TestWorldStaticMeshMapCollision 验证地图静态 mesh 会阻挡玩家移动和射线
func TestWorldStaticMeshMapCollision(t *testing.T) {
	meshMapPath := writeTestMapCollision(t, "map_test", `
    {
      "id": "mesh_wall_test",
      "shape": "mesh",
      "position": [0, 0, 0],
      "rotation": [0, 0, 0, 1],
      "vertices": [-5, 0, 2, 5, 0, 2, -5, 3, 2, 5, 3, 2],
      "triangles": [0, 2, 1, 1, 2, 3],
      "is_trigger": false
    }`)
	world := newTestWorldWithMap(t, meshMapPath)
	defer closeTestWorld(t, world)

	if err := world.AddPlayer(1, logic.Vector3{X: 0, Y: 0, Z: 0}); err != nil {
		t.Fatalf("add player: %v", err)
	}
	moveResult, err := world.MovePlayer(logic.MovePlayerRequest{
		PlayerID:  1,
		Direction: logic.Vector3{Z: 1},
		Distance:  5,
	})
	if err != nil {
		t.Fatalf("move player: %v", err)
	}
	if !moveResult.Blocked {
		t.Fatal("expected movement to be blocked by map mesh wall")
	}
	if moveResult.Position.Z >= 2 {
		t.Fatalf("expected player to stop before mesh wall, got z %.4f", moveResult.Position.Z)
	}

	hit, err := world.Raycast(logic.RaycastRequest{
		Origin:         logic.Vector3{X: 0, Y: 1, Z: 0},
		Direction:      logic.Vector3{Z: 1},
		MaxDistance:    5,
		IgnorePlayerID: 1,
	})
	if err != nil {
		t.Fatalf("raycast mesh wall: %v", err)
	}
	if !hit.Hit || math.Abs(hit.Distance-2) > testFloatTolerance {
		t.Fatalf("expected raycast to hit mesh wall near distance 2, got %+v", hit)
	}
}

// TestWorldCreateMultipleRooms 验证多个房间 world 可以共享进程级 PhysX runtime
func TestWorldCreateMultipleRooms(t *testing.T) {
	mapPath := writeTestMapCollision(t, "map_test", `
    {
      "id": "far_box_test",
      "shape": "box",
      "position": [100, 1, 100],
      "rotation": [0, 0, 0, 1],
      "size": [1, 1, 1],
      "is_trigger": false
    }`)
	factory := NewFactory(Config{PlayerCapsuleRadius: 0.35, PlayerCapsuleHeight: 1.8, CreateGroundPlane: true, DefaultMapID: "map_test", MapCollisionPath: mapPath})

	firstWorld, err := factory.NewWorld("room-1")
	if err != nil {
		t.Fatalf("new first world: %v", err)
	}
	defer closeTestWorld(t, firstWorld)

	secondWorld, err := factory.NewWorld("room-2")
	if err != nil {
		t.Fatalf("new second world: %v", err)
	}
	defer closeTestWorld(t, secondWorld)

	if err := firstWorld.AddPlayer(1, logic.Vector3{X: 0, Y: 0, Z: 0}); err != nil {
		t.Fatalf("add first world player: %v", err)
	}
	if err := secondWorld.AddPlayer(2, logic.Vector3{X: 0, Y: 0, Z: 0}); err != nil {
		t.Fatalf("add second world player: %v", err)
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
	emptyMapPath := writeTestMapCollision(t, "map_test", `
    {
      "id": "far_box_test",
      "shape": "box",
      "position": [100, 1, 100],
      "rotation": [0, 0, 0, 1],
      "size": [1, 1, 1],
      "is_trigger": false
    }`)
	return newTestWorldWithMap(t, emptyMapPath)
}

// newTestWorldWithMap 创建加载指定地图碰撞的测试用 PhysX world
func newTestWorldWithMap(t *testing.T, mapCollisionPath string) logic.PhysicsWorld {
	t.Helper()
	factory := NewFactory(Config{PlayerCapsuleRadius: 0.35, PlayerCapsuleHeight: 1.8, CreateGroundPlane: true, DefaultMapID: "map_test", MapCollisionPath: mapCollisionPath})
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
