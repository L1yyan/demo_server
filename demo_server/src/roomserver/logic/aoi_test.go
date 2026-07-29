package logic

import "testing"

// TestSimpleAOIFilterKeepsAliveOpponentOutsideViewAngle 验证双人对局不因视野角过滤存活对手
func TestSimpleAOIFilterKeepsAliveOpponentOutsideViewAngle(t *testing.T) {
	filter := NewSimpleAOIFilter()
	self := &Player{ID: 1, X: -4, Z: 0, Yaw: 0, Alive: true}
	opponent := &Player{ID: 2, X: 1.9158495664596558, Z: 0.777599036693573, Yaw: 0, Alive: true}

	visible := filter.FilterVisible(self, []*Player{self, opponent})
	if len(visible) != 1 || visible[0].ID != opponent.ID {
		t.Fatalf("expected alive opponent visible outside old view angle, got %+v", visible)
	}
}

// TestSimpleAOIFilterSkipsDeadOpponent 验证死亡玩家不会进入快照可见列表
func TestSimpleAOIFilterSkipsDeadOpponent(t *testing.T) {
	filter := NewSimpleAOIFilter()
	self := &Player{ID: 1, Alive: true}
	opponent := &Player{ID: 2, Alive: false}

	visible := filter.FilterVisible(self, []*Player{self, opponent})
	if len(visible) != 0 {
		t.Fatalf("expected dead opponent hidden, got %+v", visible)
	}
}
