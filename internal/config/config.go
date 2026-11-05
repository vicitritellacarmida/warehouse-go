// Package config loads service settings from environment variables and an
// optional .env file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// PlaceholderPassword is the marker used in example DSNs. The service
// refuses to start while the DSN still contains it.
const PlaceholderPassword = "YOUR_PASSWORD_HERE"

// DefaultDSN is the DSN used when DATABASE_DSN is not set. It contains a
// placeholder password on purpose, so it must be overridden.
const DefaultDSN = "root:" + PlaceholderPassword + "@tcp(127.0.0.1:3306)/warehouse?parseTime=true&multiStatements=true"

// Config holds all runtime settings.
type Config struct {
	DatabaseDSN        string
	HTTPAddr           string
	LogLevel           slog.Level
	AutoMigrate        bool
	ReceiptToleranceBP int64
	ShutdownTimeout    time.Duration
}

// Load reads envFile (if it exists) into the process environment without
// overriding variables that are already set, and then builds the Config.
func Load(envFile string) (Config, error) {
	if envFile != "" {
		if err := godotenv.Load(envFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Config{}, fmt.Errorf("load %s: %w", envFile, err)
		}
	}
	return FromEnv(os.Getenv)
}

// FromEnv builds a Config from a lookup function such as os.Getenv.
func FromEnv(getenv func(string) string) (Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
	cfg := Config{
		DatabaseDSN: get("DATABASE_DSN", DefaultDSN),
		HTTPAddr:    get("HTTP_ADDR", ":8080"),
	}
	if strings.Contains(cfg.DatabaseDSN, PlaceholderPassword) {
		return Config{}, fmt.Errorf("DATABASE_DSN still contains the placeholder %s: "+
			"set DATABASE_DSN in the environment or in .env (see .env.example)", PlaceholderPassword)
	}
	if err := cfg.LogLevel.UnmarshalText([]byte(get("LOG_LEVEL", "info"))); err != nil {
		return Config{}, fmt.Errorf("LOG_LEVEL: %w", err)
	}
	var err error
	if cfg.AutoMigrate, err = strconv.ParseBool(get("AUTO_MIGRATE", "true")); err != nil {
		return Config{}, fmt.Errorf("AUTO_MIGRATE: %w", err)
	}
	if cfg.ReceiptToleranceBP, err = strconv.ParseInt(get("RECEIPT_TOLERANCE_BP", "500"), 10, 64); err != nil ||
		cfg.ReceiptToleranceBP < 0 || cfg.ReceiptToleranceBP > 10000 {
		return Config{}, fmt.Errorf("RECEIPT_TOLERANCE_BP must be an integer between 0 and 10000")
	}
	if cfg.ShutdownTimeout, err = time.ParseDuration(get("SHUTDOWN_TIMEOUT", "15s")); err != nil {
		return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT: %w", err)
	}
	return cfg, nil
}
