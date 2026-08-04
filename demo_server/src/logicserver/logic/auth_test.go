package logic

import "testing"

// TestParseGuestLogin 验证 Multiplayer 临时玩家登录参数解析
func TestParseGuestLogin(t *testing.T) {
	displayName, ok := parseGuestLogin("guest:Player One", guestLoginPassword)
	if !ok {
		t.Fatalf("expected guest login to parse")
	}
	if displayName != "Player One" {
		t.Fatalf("expected display name to be trimmed, got %q", displayName)
	}

	if _, ok := parseGuestLogin("guest:Player One", "wrong-password"); ok {
		t.Fatalf("expected wrong guest password to fail")
	}
	if _, ok := parseGuestLogin("player@example.com", guestLoginPassword); ok {
		t.Fatalf("expected normal email to skip guest parser")
	}
}

// TestGuestPlayerIDStable 验证同一昵称生成稳定且非零的临时玩家ID
func TestGuestPlayerIDStable(t *testing.T) {
	first := guestPlayerID("Player One")
	second := guestPlayerID("Player One")
	other := guestPlayerID("Player Two")

	if first == 0 {
		t.Fatalf("expected non-zero guest player id")
	}
	if first != second {
		t.Fatalf("expected stable guest player id, got %d and %d", first, second)
	}
	if first == other {
		t.Fatalf("expected different names to produce different ids")
	}
}
