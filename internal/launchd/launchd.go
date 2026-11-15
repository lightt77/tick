// Package launchd installs tick's daemon as a per-user LaunchAgent, so it starts
// at login and is restarted if it crashes (but not after "Quit tick").
package launchd

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lightt77/tick/internal/paths"
)

// Label names the agent.
const Label = "local.tick.daemon"

// PlistPath is where the agent definition lives.
func PlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
}

func domain() string        { return fmt.Sprintf("gui/%d", os.Getuid()) }
func serviceTarget() string { return domain() + "/" + Label }

// Plist renders the agent for the binary at exe.
func Plist(exe, logFile string) string {
	esc := html.EscapeString
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + esc(exe) + `</string>
		<string>daemon</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	<string>` + esc(logFile) + `</string>
	<key>StandardErrorPath</key>
	<string>` + esc(logFile) + `</string>
</dict>
</plist>
`
}

// Executable is the path launchd should run: this binary, symlinks resolved.
// A `go run` binary lives in a temp build dir and would vanish, so it is refused.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	if strings.Contains(exe, "go-build") || strings.HasPrefix(exe, os.TempDir()) {
		return "", fmt.Errorf("%s is a temporary build; install tick first (make install) and run install from there", exe)
	}
	return exe, nil
}

func launchctl(args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command("launchctl", args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// Install writes the agent and (re)loads it, which starts the daemon.
func Install(exe string) error {
	if err := paths.Ensure(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(PlistPath()), 0o755); err != nil {
		return err
	}
	logFile := filepath.Join(paths.Logs(), "daemon.log")
	if err := os.WriteFile(PlistPath(), []byte(Plist(exe, logFile)), 0o644); err != nil {
		return err
	}
	launchctl("bootout", serviceTarget()) // fine if it was not loaded
	if out, err := launchctl("bootstrap", domain(), PlistPath()); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v %s", err, out)
	}
	return nil
}

// Uninstall stops the daemon and removes the agent.
func Uninstall() error {
	launchctl("bootout", serviceTarget())
	if err := os.Remove(PlistPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Status says whether the agent file exists and whether launchd has it loaded.
func Status() (installed, loaded bool) {
	_, err := os.Stat(PlistPath())
	installed = err == nil
	_, err = launchctl("print", serviceTarget())
	return installed, err == nil
}

// Kickstart starts (or restarts) a loaded agent, e.g. after "Quit tick".
func Kickstart() error {
	if out, err := launchctl("kickstart", "-k", serviceTarget()); err != nil {
		return fmt.Errorf("launchctl kickstart: %v %s", err, out)
	}
	return nil
}
