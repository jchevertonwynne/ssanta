package config

import (
	"fmt"
	"os"
)

type Config struct {
	HTTPAddr      string
	DatabaseURL   string
	MigrationsDir string
	SessionSecret string
}

func Load() (Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	secret := os.Getenv("SESSION_SECRET")
	if secret == "" {
		return Config{}, fmt.Errorf("SESSION_SECRET is required")
	}

	return Config{
		HTTPAddr:      getEnv("HTTP_ADDR", ":8080"),
		DatabaseURL:   dbURL,
		MigrationsDir: getEnv("MIGRATIONS_DIR", "migrations"),
		SessionSecret: secret,
	}, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
