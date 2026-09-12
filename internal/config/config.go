// Package config loads LocalOps' own server-side configuration - not to
// be confused with the client SDK's config (client.Config), which
// configures how an *external* app talks to this server.
package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Config holds every knob the LocalOps server itself needs.
type Config struct {
	Port                 string // e.g. "7717"
	DBPath               string // SQLite file path
	SlackWebhookURL      string // empty = alerting disabled
	SlackSigningSecret   string // empty = inbound Slack slash commands rejected (POST /slack/commands)
	WatcherFailThreshold int    // consecutive fails before the first down alert
	StuckMultiplier      int    // N x expected_heartbeat_s before a running task is "stuck"
	SweepIntervalS       int    // how often the stuck-task/overdue-watcher sweep runs
}

// Load reads LOCALOPS_* environment variables (optionally seeded from a
// .env file first via LoadEnvFile), applying sensible defaults so the
// server runs out of the box with nothing configured beyond a port.
func Load() Config {
	return Config{
		Port:                 getEnv("LOCALOPS_PORT", "7717"),
		DBPath:               getEnv("LOCALOPS_DB_PATH", "localops.db"),
		SlackWebhookURL:      os.Getenv("LOCALOPS_SLACK_WEBHOOK_URL"),
		SlackSigningSecret:   os.Getenv("LOCALOPS_SLACK_SIGNING_SECRET"),
		WatcherFailThreshold: getEnvInt("LOCALOPS_WATCHER_FAIL_THRESHOLD", 2),
		StuckMultiplier:      getEnvInt("LOCALOPS_STUCK_MULTIPLIER", 3),
		SweepIntervalS:       getEnvInt("LOCALOPS_SWEEP_INTERVAL_S", 30),
	}
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// LoadEnvFile reads a simple KEY=VALUE file (as produced by configs/*.env)
// into the process environment, without overwriting variables already
// set. A missing file at path is not an error. Mirrors client.LoadEnvFile
// so both halves of LocalOps configure the same way.
func LoadEnvFile(path string) error {
	if path == "" {
		path = ".env"
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = unquote(strings.TrimSpace(value))
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		os.Setenv(key, value)
	}
	return scanner.Err()
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
