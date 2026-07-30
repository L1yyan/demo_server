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

// TestRoomFireKillRespawnsTarget 验证血量归零后目标复活到出生点并进入无敌状态
func TestRoomFireKillRespawnsTarget(t *testing.T) {
	room, shooter, target := newShootingTestRoom(t)

	fireShootingTestFrames(room, shooter, 1, 5)

	if target.HP != defaultPlayerHP {
		t.Fatalf("expected target hp reset to %d, got %d", defaultPlayerHP, target.HP)
	}
	if !target.Alive {
		t.Fatal("expected target alive after respawn")
	}
	if shooter.KillCount != 1 || shooter.DeathCount != 0 {
		t.Fatalf("unexpected shooter stats: kills %d deaths %d", shooter.KillCount, shooter.DeathCount)
	}
	if target.KillCount != 0 || target.DeathCount != 1 {
		t.Fatalf("unexpected target stats: kills %d deaths %d", target.KillCount, target.DeathCount)
	}
	if target.InvincibleUntilTick != room.tick+durationToTicks(defaultRespawnInvincibleDuration, room.tickRate) {
		t.Fatalf("unexpected invincible tick: got %d at server tick %d", target.InvincibleUntilTick, room.tick)
	}
	position, err := room.physics.GetPlayerPosition(target.ID)
	if err != nil {
		t.Fatalf("expected respawned target in physics world: %v", err)
	}
	if position.X != 4 || position.Y != 0.1 || position.Z != 0 {
		t.Fatalf("unexpected respawn physics position: %+v", position)
	}
}

// TestRoomFireIgnoresDamageDuringRespawnInvincibility 验证复活无敌期内命中不扣血不重复计数
func TestRoomFireIgnoresDamageDuringRespawnInvincibility(t *testing.T) {
	room, shooter, target := newShootingTestRoom(t)

	fireShootingTestFrames(room, shooter, 1, 5)
	setShootingTestPlayerPosition(t, room, target, Vector3{X: 0, Y: 0.1, Z: 5}, 180)
	fireShootingTestFrames(room, shooter, 6, 1)

	if target.HP != defaultPlayerHP {
		t.Fatalf("expected target hp unchanged during invincibility, got %d", target.HP)
	}
	if shooter.KillCount != 1 || target.DeathCount != 1 {
		t.Fatalf("unexpected stats during invincibility: shooter kills %d target deaths %d", shooter.KillCount, target.DeathCount)
	}
}

// TestRoomFireDamagesTargetAfterRespawnInvincibility 验证复活无敌期结束后可以再次受伤
func TestRoomFireDamagesTargetAfterRespawnInvincibility(t *testing.T) {
	room, shooter, target := newShootingTestRoom(t)

	fireShootingTestFrames(room, shooter, 1, 5)
	for room.tick < target.InvincibleUntilTick {
		room.update(context.Background())
	}
	setShootingTestPlayerPosition(t, room, target, Vector3{X: 0, Y: 0.1, Z: 5}, 180)
	fireShootingTestFrames(room, shooter, room.tick+1, 1)

	if target.HP != defaultPlayerHP-defaultFireDamage {
		t.Fatalf("expected target damaged after invincibility expired, got %d", target.HP)
	}
	if target.InvincibleUntilTick != 0 {
		t.Fatalf("expected expired invincibility cleared, got %d", target.InvincibleUntilTick)
	}
}

// TestRoomPlayerStatsQueryReturnsKillsAndDeaths 验证房间战绩查询返回击杀和死亡数量
func TestRoomPlayerStatsQueryReturnsKillsAndDeaths(t *testing.T) {
	room, shooter, target := newShootingTestRoom(t)

	fireShootingTestFrames(room, shooter, 1, 5)

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

// fireShootingTestFrames 推进指定数量的开火输入帧
func fireShootingTestFrames(room *Room, shooter *Player, startTick int64, count int) {
	frames := make([]protocol.PlayerInputFrame, 0, count)
	for offset := 0; offset < count; offset++ {
		frames = append(frames, protocol.PlayerInputFrame{ClientTick: startTick + int64(offset), Yaw: 0, Pitch: 0, Fire: true})
	}
	room.handleInputBatch(context.Background(), shooter.ID, protocol.PlayerInputBatch{Frames: frames})
	for tick := 0; tick < len(frames); tick++ {
		room.update(context.Background())
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
