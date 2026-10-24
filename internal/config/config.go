package config

import (
	"fmt"
	"os"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
)

// Config is the process configuration loaded from the environment.
type Config struct {
	AppEnv                  string
	Addr                    string
	PlatformDatabaseURL     string
	SecretKey               []byte
	SessionIdleTimeout      time.Duration
	SessionAbsoluteTimeout  time.Duration
	SessionRotationInterval time.Duration
	SessionGracePeriod      time.Duration
	BootstrapAdminUsername  string
	BootstrapAdminPassword  string
	TestDatabaseURL         string
}

// Load reads environment variables (after an optional .env file).
func Load() (Config, error) {
	LoadDotEnv()

	cfg := Config{
		AppEnv:                  getenv("APP_ENV", "development"),
		Addr:                    getenv("APP_ADDR", ":8080"),
		PlatformDatabaseURL:     os.Getenv("PLATFORM_DATABASE_URL"),
		SessionIdleTimeout:      30 * time.Minute,
		SessionAbsoluteTimeout:  8 * time.Hour,
		SessionRotationInterval: 15 * time.Minute,
		SessionGracePeriod:      30 * time.Second,
		BootstrapAdminUsername:  getenv("BOOTSTRAP_ADMIN_USERNAME", "admin"),
		BootstrapAdminPassword:  os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
		TestDatabaseURL:         os.Getenv("TEST_DATABASE_URL"),
	}
	if cfg.PlatformDatabaseURL == "" {
		return Config{}, fmt.Errorf("config: PLATFORM_DATABASE_URL wajib diisi")
	}

	var err error
	if cfg.SessionIdleTimeout, err = durationEnv("SESSION_IDLE_TIMEOUT", cfg.SessionIdleTimeout); err != nil {
		return Config{}, err
	}
	if cfg.SessionAbsoluteTimeout, err = durationEnv("SESSION_ABSOLUTE_TIMEOUT", cfg.SessionAbsoluteTimeout); err != nil {
		return Config{}, err
	}
	if cfg.SessionRotationInterval, err = durationEnv("SESSION_ROTATION_INTERVAL", cfg.SessionRotationInterval); err != nil {
		return Config{}, err
	}
	if cfg.SessionGracePeriod, err = durationEnv("SESSION_GRACE_PERIOD", cfg.SessionGracePeriod); err != nil {
		return Config{}, err
	}

	secret, err := dbconn.ParseSecretKey(os.Getenv("APP_SECRET_KEY"))
	if err != nil {
		return Config{}, fmt.Errorf("config: APP_SECRET_KEY: %w", err)
	}
	cfg.SecretKey = secret
	return cfg, nil
}

// MustLoad is for process startup. It panics when configuration is invalid.
func MustLoad() Config {
	cfg, err := Load()
	if err != nil {
		panic(err)
	}
	return cfg
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s: %w", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("config: %s harus lebih besar dari 0", key)
	}
	return d, nil
}
