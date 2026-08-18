package logic

import "testing"

// TestNormalizeLoginEmail 验证登录注册邮箱会统一去空格和小写
func TestNormalizeLoginEmail(t *testing.T) {
	email := normalizeLoginEmail("  Player@Example.COM  ")
	if email != "player@example.com" {
		t.Fatalf("expected normalized email, got %q", email)
	}
}

// TestIsValidPassword 验证密码长度符合 bcrypt 限制
func TestIsValidPassword(t *testing.T) {
	if !isValidPassword("secret") {
		t.Fatalf("expected normal password to be valid")
	}
	if isValidPassword("") {
		t.Fatalf("expected empty password to be invalid")
	}
	if isValidPassword("1234567890123456789012345678901234567890123456789012345678901234567890123") {
		t.Fatalf("expected password longer than bcrypt limit to be invalid")
	}
}
