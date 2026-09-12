// Package client is the LocalOps Go SDK.
//
// It lets any local Go service report tasks (discrete, has-a-lifecycle work)
// and watcher check-ins (standing up/down conditions) to a locally running
// LocalOps server, without ever risking the host application's correctness:
// when LocalOps isn't configured or isn't reachable, every call is a cheap,
// silent no-op.
//
// # Configuration
//
// Config is read from the process environment, optionally seeded from a
// ".env"-style file first (see LoadEnvFile). Recognized variables:
//
//	PROFILE            host app's existing profile/environment name.
//	                   "staging" enables the real client; anything else
//	                   (including unset) keeps LocalOps a no-op.
//	LOCALOPS_ENABLED   "true"/"1" force-enables the real client regardless
//	                   of PROFILE. Lets integrators opt in without adopting
//	                   a "PROFILE" concept at all.
//	LOCALOPS_URL       base URL of the LocalOps server, e.g.
//	                   "http://localhost:7717". Required for the real
//	                   client; if unset, the SDK stays a no-op even if
//	                   PROFILE=staging or LOCALOPS_ENABLED=true.
//	LOCALOPS_SERVER    the "server" label attached to every task/watcher
//	                   this process reports, e.g. "wvs-server".
//	LOCALOPS_ENV_FILE  optional path to a .env file to load before reading
//	                   the variables above (default: ".env" in the working
//	                   directory, loaded only if present).
package client

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config controls how the SDK talks (or doesn't talk) to LocalOps.
type Config struct {
	// Enabled switches on the real HTTP client. When false, every method
	// on every Task/Watcher handle is a no-op that returns immediately.
	Enabled bool

	// BaseURL of the LocalOps server, e.g. "http://localhost:7717".
	// Ignored when Enabled is false.
	BaseURL string

	// Server is the default "server" label attached to tasks/watcher
	// check-ins that don't specify one explicitly.
	Server string

	// Logger receives non-fatal problems (failed requests, bad responses).
	// Never receives anything from application business logic. Defaults
	// to a logger that writes to os.Stderr if nil.
	Logger Logger
}

// Logger is the minimal logging surface the SDK needs. Most host apps can
// adapt their existing logger to this with a one-line wrapper.
type Logger interface {
	Printf(format string, args ...any)
}

// ConfigFromEnv builds a Config from environment variables, per the rules
// documented on the package. It does not load any .env file itself; call
// LoadEnvFile first if you want file-based configuration merged into the
// process environment.
func ConfigFromEnv() Config {
	profile := os.Getenv("PROFILE")
	explicit, _ := strconv.ParseBool(os.Getenv("LOCALOPS_ENABLED"))
	url := os.Getenv("LOCALOPS_URL")

	enabled := (profile == "staging" || explicit) && url != ""

	return Config{
		Enabled: enabled,
		BaseURL: url,
		Server:  os.Getenv("LOCALOPS_SERVER"),
	}
}

// LoadEnvFile reads a simple KEY=VALUE file (one assignment per line, "#"
// comments, optional "export " prefix, optional surrounding quotes) and
// applies each entry to the process environment via os.Setenv, without
// overwriting a variable that is already set in the environment - real
// environment variables always win over the file.
//
// path may be empty, in which case ".env" is used. A missing file is not
// an error: it simply means there is nothing to load. This lets a service
// call:
//
//	client.LoadEnvFile("")
//	cfg := client.ConfigFromEnv()
//	client.Init(cfg)
//
// unconditionally, in both dev (.env present) and prod (no .env, real
// environment variables set some other way, or unset entirely so the
// SDK stays a no-op).
func LoadEnvFile(path string) error {
	if path == "" {
		path = os.Getenv("LOCALOPS_ENV_FILE")
	}
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
		value = strings.TrimSpace(value)
		value = unquote(value)

		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue // real env always wins over the file
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

type stderrLogger struct{}

func (stderrLogger) Printf(format string, args ...any) {
	// Deliberately minimal - avoids pulling in log/slog opinions.
	// Swap Config.Logger for anything richer.
	os.Stderr.WriteString("[localops] " + fmt.Sprintf(format, args...) + "\n")
}
