package logic

import (
	"context"
	"testing"

	"demo_server/src/roomserver/protocol"
)

// TestRoomFireHitReducesTargetHP 验证开火命中一次扣除目标20点血量
func TestRoomFireHitReducesTargetHP(t *testing.T) {
	room, shooter, target := newShootingTestRoom(t)

	room.handleInputBatch(context.Background(), shooter.ID, protocol.PlayerInputBatch{Frames: []protocol.PlayerInputFrame{
		{ClientTick: 1, Yaw: 0, Pitch: 0, Fire: true},
	}})
	room.update(context.Background())

	if target.HP != 80 {
		t.Fatalf("expected target hp 80 after one hit, got %d", target.HP)
	}
	if !target.Alive {
		t.Fatal("expected target alive after one hit")
	}
}

// TestRoomFireKillMarksTargetDead 验证血量归零后目标进入死亡状态
func TestRoomFireKillMarksTargetDead(t *testing.T) {
	room, shooter, target := newShootingTestRoom(t)

	frames := make([]protocol.PlayerInputFrame, 0, 6)
	for tick := int64(1); tick <= 6; tick++ {
		frames = append(frames, protocol.PlayerInputFrame{ClientTick: tick, Yaw: 0, Pitch: 0, Fire: true})
	}
	room.handleInputBatch(context.Background(), shooter.ID, protocol.PlayerInputBatch{Frames: frames})
	for tick := 0; tick < len(frames); tick++ {
		room.update(context.Background())
	}

	if target.HP != 0 {
		t.Fatalf("expected target hp clamped to 0, got %d", target.HP)
	}
	if target.Alive {
		t.Fatal("expected target dead after lethal hits")
	}
	if shooter.KillCount != 1 || shooter.DeathCount != 0 {
		t.Fatalf("unexpected shooter stats: kills %d deaths %d", shooter.KillCount, shooter.DeathCount)
	}
	if target.KillCount != 0 || target.DeathCount != 1 {
		t.Fatalf("unexpected target stats: kills %d deaths %d", target.KillCount, target.DeathCount)
	}
	if _, err := room.physics.GetPlayerPosition(target.ID); err != ErrPhysicsPlayerNotFound {
		t.Fatalf("expected dead target removed from physics, got %v", err)
	}
}

// TestRoomPlayerStatsQueryReturnsKillsAndDeaths 验证房间战绩查询返回击杀和死亡数量
func TestRoomPlayerStatsQueryReturnsKillsAndDeaths(t *testing.T) {
	room, shooter, target := newShootingTestRoom(t)

	frames := make([]protocol.PlayerInputFrame, 0, 5)
	for tick := int64(1); tick <= 5; tick++ {
		frames = append(frames, protocol.PlayerInputFrame{ClientTick: tick, Yaw: 0, Pitch: 0, Fire: true})
	}
	room.handleInputBatch(context.Background(), shooter.ID, protocol.PlayerInputBatch{Frames: frames})
	for tick := 0; tick < len(frames); tick++ {
		room.update(context.Background())
	}

	shooterStats, err := room.lookupPlayerStats(shooter.ID, shooter.ID)
	if err != nil {
		t.Fatalf("query shooter stats: %v", err)
	}
	if shooterStats.Stats.KillCount != 1 || shooterStats.Stats.DeathCount != 0 {
		t.Fatalf("unexpected shooter stats: %+v", shooterStats.Stats)
	}
	targetStats, err := room.lookupPlayerStats(shooter.ID, target.ID)
	if err != nil {
		t.Fatalf("query target stats: %v", err)
	}
	if targetStats.Stats.KillCount != 0 || targetStats.Stats.DeathCount != 1 {
		t.Fatalf("unexpected target stats: %+v", targetStats.Stats)
	}
}

// newShootingTestRoom 创建射击命中测试房间
func newShootingTestRoom(t *testing.T) (*Room, *Player, *Player) {
	t.Helper()
	physics := NewSimplePhysicsWorld()
	room := NewRoom("room-shooting-test", 2, 20, 10, nil, physics)
	shooter := &Player{ID: 1, Session: &testSession{id: "session-shooter"}}
	target := &Player{ID: 2, Session: &testSession{id: "session-target"}}

	room.handleJoin(context.Background(), shooter)
	room.handleJoin(context.Background(), target)
	setShootingTestPlayerPosition(t, room, shooter, Vector3{X: 0, Y: 0.1, Z: 0}, 0)
	setShootingTestPlayerPosition(t, room, target, Vector3{X: 0, Y: 0.1, Z: 5}, 180)
	return room, shooter, target
}

// setShootingTestPlayerPosition 同步测试玩家逻辑位置和物理位置
func setShootingTestPlayerPosition(t *testing.T, room *Room, player *Player, position Vector3, yaw float64) {
	t.Helper()
	player.X = position.X
	player.Y = position.Y
	player.Z = position.Z
	player.Yaw = yaw
	player.Pitch = 0
	if err := room.physics.SetPlayerPosition(player.ID, position); err != nil {
		t.Fatalf("set physics position: %v", err)
	}
}
