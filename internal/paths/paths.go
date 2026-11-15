// Package paths says where tick keeps its files. TICK_HOME moves everything (tests,
// trial runs); otherwise data lives in Application Support and logs in ~/Library/Logs.
package paths

import (
	"os"
	"path/filepath"
)

// Data is the directory for state.json, runs.jsonl, config.toml and the locks.
func Data() string {
	if h := os.Getenv("TICK_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "tick")
}

// Logs is the directory for the daemon log.
func Logs() string {
	if h := os.Getenv("TICK_HOME"); h != "" {
		return filepath.Join(h, "logs")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "tick")
}

func State() string     { return filepath.Join(Data(), "state.json") }
func StateLock() string { return filepath.Join(Data(), "state.lock") }
func DaemonLock() string {
	return filepath.Join(Data(), "daemon.lock")
}
func RunLog() string { return filepath.Join(Data(), "runs.jsonl") }
func Config() string { return filepath.Join(Data(), "config.toml") }

// Dashboard is the static page; DashboardData is the .js file the daemon keeps
// fresh next to it (a file:// page can load a script but cannot fetch a JSON file).
func Dashboard() string     { return filepath.Join(Data(), "dashboard.html") }
func DashboardData() string { return filepath.Join(Data(), "dashboard-data.js") }

// Ensure creates the data and log directories, readable only by the user.
func Ensure() error {
	if err := os.MkdirAll(Data(), 0o700); err != nil {
		return err
	}
	return os.MkdirAll(Logs(), 0o700)
}
