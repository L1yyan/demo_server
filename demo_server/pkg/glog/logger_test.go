package glog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitCreatesSplitLogFiles 校验初始化后会创建分级日志文件
func TestInitCreatesSplitLogFiles(t *testing.T) {
	resetLoggerForTest()

	cfg := DefaultConfig()
	cfg.ServiceName = "logic_server"
	cfg.RootDir = t.TempDir()
	cfg.Level = "debug"
	cfg.Console = false
	cfg.AddCaller = false

	// 初始化日志系统并写入不同级别日志
	if err := Init(cfg); err != nil {
		t.Fatalf("init logger: %v", err)
	}
	ctx := WithRequestID(WithTraceID(context.Background(), "trace-1"), "req-1")
	Info(ctx, "login success", String("user_id", "10001"))
	Warn(ctx, "slow request", Int("cost_ms", 30))
	Error(ctx, "query failed", Err(errors.New("db down")))
	if err := Sync(); err != nil {
		t.Fatalf("sync logger: %v", err)
	}

	infoContent := readOnlyLogFile(t, cfg.infoLogDir())
	errorContent := readOnlyLogFile(t, cfg.errorLogDir())

	// 校验 error 与非 error 日志严格分流
	if !strings.Contains(infoContent, "login success") || !strings.Contains(infoContent, "slow request") {
		t.Fatalf("info log missing non-error content: %s", infoContent)
	}
	if strings.Contains(infoContent, "query failed") {
		t.Fatalf("info log contains error content: %s", infoContent)
	}
	if !strings.Contains(errorContent, "query failed") {
		t.Fatalf("error log missing error content: %s", errorContent)
	}
	if strings.Contains(errorContent, "login success") || strings.Contains(errorContent, "slow request") {
		t.Fatalf("error log contains non-error content: %s", errorContent)
	}

	// 校验 context 字段会自动写入日志
	if !strings.Contains(infoContent, "trace-1") || !strings.Contains(infoContent, "req-1") {
		t.Fatalf("info log missing context fields: %s", infoContent)
	}
}

// TestInitRejectsInvalidConfig 校验非法配置会返回错误
func TestInitRejectsInvalidConfig(t *testing.T) {
	resetLoggerForTest()

	cfg := DefaultConfig()
	cfg.ServiceName = ""
	if err := Init(cfg); err == nil {
		t.Fatal("expected error for empty service name")
	}

	cfg = DefaultConfig()
	cfg.Level = "verbose"
	if err := Init(cfg); err == nil {
		t.Fatal("expected error for unsupported log level")
	}
}

// TestRelativeRootDirUsesProjectRoot 校验相对日志目录会解析到项目根目录
func TestRelativeRootDirUsesProjectRoot(t *testing.T) {
	resetLoggerForTest()

	oldWorkDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get work dir: %v", err)
	}
	defer func() {
		if err := os.Chdir(oldWorkDir); err != nil {
			t.Fatalf("restore work dir: %v", err)
		}
	}()

	projectRoot := t.TempDir()
	goModPath := filepath.Join(projectRoot, "go.mod")
	if err := os.WriteFile(goModPath, []byte("module test_project\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	workDir := filepath.Join(projectRoot, "src", "logicserver", "cmd")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("create work dir: %v", err)
	}

	// 切换到子目录后初始化日志，验证相对路径仍按项目根目录解析
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("change work dir: %v", err)
	}

	cfg := DefaultConfig()
	cfg.ServiceName = "logic_server"
	cfg.RootDir = "./bin/logs"
	cfg.Console = false
	cfg.AddCaller = false
	if err := Init(cfg); err != nil {
		t.Fatalf("init logger: %v", err)
	}
	Info(context.Background(), "relative root")
	if err := Sync(); err != nil {
		t.Fatalf("sync logger: %v", err)
	}

	rootDir := filepath.Join(projectRoot, "bin", "logs", "logic_server", "info")
	infoContent := readOnlyLogFile(t, rootDir)
	if !strings.Contains(infoContent, "relative root") {
		t.Fatalf("info log missing relative root content: %s", infoContent)
	}
}

// TestWithLoggerFields 校验模块 logger 会携带固定字段
func TestWithLoggerFields(t *testing.T) {
	resetLoggerForTest()

	cfg := DefaultConfig()
	cfg.ServiceName = "logic_server"
	cfg.RootDir = t.TempDir()
	cfg.Console = false
	cfg.AddCaller = false
	if err := Init(cfg); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	// 通过模块 logger 写入固定字段
	logger := With(String("module", "auth"))
	logger.Info(context.Background(), "register success", String("user_id", "10002"), Err(nil))
	if err := Sync(); err != nil {
		t.Fatalf("sync logger: %v", err)
	}

	infoContent := readOnlyLogFile(t, cfg.infoLogDir())
	if !strings.Contains(infoContent, "register success") || !strings.Contains(infoContent, "module") || !strings.Contains(infoContent, "auth") {
		t.Fatalf("info log missing logger fields: %s", infoContent)
	}
	if strings.Contains(infoContent, "error") {
		t.Fatalf("nil error should be skipped: %s", infoContent)
	}
}

// readOnlyLogFile 读取目录中唯一的日志文件内容
func readOnlyLogFile(t *testing.T, dir string) string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read log dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 log file in %s, got %d", dir, len(entries))
	}

	content, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	return string(content)
}

// resetLoggerForTest 重置全局 logger，避免测试间互相影响
func resetLoggerForTest() {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalLogger = newDevelopmentLogger()
	globalInitialized = false
}
