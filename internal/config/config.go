package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all runtime configuration for CimplrAdmin.
// Loaded entirely from environment variables - no .env library used.
//
// Required env vars:
//   DB_USER, DB_PASSWORD, DB_HOST, DB_PORT, DB_NAME  (or DATABASE_URL)
//   MASTER_KEY
//   PORT
//   SEND_ENDPOINT_URL, SEND_ENDPOINT_API_KEY
//   OUTBOX_WORKER_ENABLED, OUTBOX_WORKER_POLL_SECS, OUTBOX_WORKER_BATCH_SIZE, OUTBOX_WORKER_TIMEOUT_SECS
//   LICENCE_CHECKER_ENABLED, LICENCE_CHECKER_POLL_HOURS
type Config struct {
	// Database
	DBUser      string
	DBPassword  string
	DBHost      string
	DBPort      string
	DBName      string
	DatabaseURL string // overrides individual DB_* vars

	// HTTP
	Port string

	// Emergency master access key
	MasterKey string

	// Notification outbox
	SendEndpointURL    string
	SendEndpointAPIKey string

	OutboxWorkerEnabled bool
	OutboxPollSecs      int
	OutboxBatchSize     int
	OutboxTimeoutSecs   int

	// Licence checker
	LicenceCheckerEnabled   bool
	LicenceCheckerPollHours int
}

// Load reads all configuration from environment variables.
func Load() *Config {
	return &Config{
		DBUser:      os.Getenv("DB_USER"),
		DBPassword:  os.Getenv("DB_PASSWORD"),
		DBHost:      os.Getenv("DB_HOST"),
		DBPort:      envOrDefault("DB_PORT", "5432"),
		DBName:      os.Getenv("DB_NAME"),
		DatabaseURL: os.Getenv("DATABASE_URL"),

		Port:      envOrDefault("PORT", "8080"),
		MasterKey: os.Getenv("MASTER_KEY"),

		SendEndpointURL:    os.Getenv("SEND_ENDPOINT_URL"),
		SendEndpointAPIKey: os.Getenv("SEND_ENDPOINT_API_KEY"),

		OutboxWorkerEnabled: envBool("OUTBOX_WORKER_ENABLED", true),
		OutboxPollSecs:      envInt("OUTBOX_WORKER_POLL_SECS", 10),
		OutboxBatchSize:     envInt("OUTBOX_WORKER_BATCH_SIZE", 50),
		OutboxTimeoutSecs:   envInt("OUTBOX_WORKER_TIMEOUT_SECS", 15),

		LicenceCheckerEnabled:   envBool("LICENCE_CHECKER_ENABLED", true),
		LicenceCheckerPollHours: envInt("LICENCE_CHECKER_POLL_HOURS", 24),
	}
}

// DSN returns the PostgreSQL connection string. DATABASE_URL takes precedence.
func (c *Config) DSN() string {
	if c.DatabaseURL != "" {
		return c.DatabaseURL
	}
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=require",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName,
	)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
