package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PLATFORM_DATABASE_URL", "")
	t.Setenv("APP_SECRET_KEY", strings.Repeat("a", 32))
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "PLATFORM_DATABASE_URL") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoad_RejectsShortSecret(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PLATFORM_DATABASE_URL", "postgres://platform:platform@localhost:5432/platform?sslmode=disable")
	t.Setenv("APP_SECRET_KEY", "too-short")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "APP_SECRET_KEY") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoad_RejectsBadDuration(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PLATFORM_DATABASE_URL", "postgres://platform:platform@localhost:5432/platform?sslmode=disable")
	t.Setenv("APP_SECRET_KEY", strings.Repeat("b", 32))
	t.Setenv("SESSION_IDLE_TIMEOUT", "nope")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "SESSION_IDLE_TIMEOUT") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoad_UsesEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PLATFORM_DATABASE_URL", "postgres://platform:platform@localhost:5432/platform?sslmode=disable")
	t.Setenv("APP_SECRET_KEY", strings.Repeat("c", 32))
	t.Setenv("APP_ENV", "test")
	t.Setenv("APP_ADDR", ":9090")
	t.Setenv("SESSION_IDLE_TIMEOUT", "1m")
	t.Setenv("SESSION_ABSOLUTE_TIMEOUT", "2h")
	t.Setenv("SESSION_ROTATION_INTERVAL", "5m")
	t.Setenv("SESSION_GRACE_PERIOD", "10s")
	t.Setenv("BOOTSTRAP_ADMIN_USERNAME", "root")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "secret-pass")
	t.Setenv("TEST_DATABASE_URL", "postgres://test:test@localhost:5440/postgres?sslmode=disable")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppEnv != "test" || cfg.Addr != ":9090" || cfg.BootstrapAdminUsername != "root" || cfg.BootstrapAdminPassword != "secret-pass" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.SessionIdleTimeout != time.Minute || cfg.SessionAbsoluteTimeout != 2*time.Hour || cfg.SessionRotationInterval != 5*time.Minute || cfg.SessionGracePeriod != 10*time.Second {
		t.Fatalf("durations = %s %s %s %s", cfg.SessionIdleTimeout, cfg.SessionAbsoluteTimeout, cfg.SessionRotationInterval, cfg.SessionGracePeriod)
	}
	if string(cfg.SecretKey) != strings.Repeat("c", 32) {
		t.Fatalf("secret len = %d", len(cfg.SecretKey))
	}
	if cfg.TestDatabaseURL == "" || cfg.PlatformDatabaseURL == "" {
		t.Fatalf("urls were cleared")
	}
}

func TestLoadDotEnv_DoesNotOverride(t *testing.T) {
	dir := t.TempDir()
	body := "KEEP_ME=from-file\nNEW_VAR=\"hello world\"\n# comment\n\nNOT_A_PAIR\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("KEEP_ME", "from-env")
	t.Setenv("NEW_VAR", "")

	LoadDotEnv()

	if os.Getenv("KEEP_ME") != "from-env" {
		t.Fatalf("KEEP_ME = %q", os.Getenv("KEEP_ME"))
	}
	if os.Getenv("NEW_VAR") != "hello world" {
		t.Fatalf("NEW_VAR = %q", os.Getenv("NEW_VAR"))
	}
}
