package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/billgrant/cg-dev-auth/internal/chainctl"
	"github.com/billgrant/cg-dev-auth/internal/fsutil"
	"github.com/billgrant/cg-dev-auth/internal/launchd"
)

const (
	maxLogBytes    = 1 << 20
	notifyInterval = 2 * time.Hour
)

func launchdDomain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

// agentProgram picks the binary path launchd should run. A Homebrew install
// runs from a versioned Cellar path, so prefer the stable symlink on PATH
// when it points at the same file.
func agentProgram(e *Env) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if p, err := e.lookPath("cg-dev-auth"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			a, errA := os.Stat(abs)
			b, errB := os.Stat(exe)
			if errA == nil && errB == nil && os.SameFile(a, b) {
				return abs, nil
			}
		}
	}
	return exe, nil
}

// agentPath builds the PATH for the job: chainctl's directory first, then the
// usual Homebrew and system directories.
func agentPath(e *Env) string {
	var dirs []string
	add := func(d string) {
		if d != "" && !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	if p, err := e.lookPath("chainctl"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			add(filepath.Dir(abs))
		}
	}
	for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
		add(d)
	}
	return strings.Join(dirs, ":")
}

// InstallAgent installs and (re)loads the LaunchAgent that runs `cg-dev-auth
// agent` every refresh_interval. On other systems it prints how to schedule
// refresh instead.
func InstallAgent(ctx context.Context, e *Env) error {
	if e.goos() != "darwin" {
		e.printf("· background refresh uses launchd and is macOS-only for now; schedule `cg-dev-auth refresh` (e.g. cron */5) to keep npm/yarn tokens fresh\n")
		return nil
	}
	prog, err := agentProgram(e)
	if err != nil {
		return err
	}
	env := map[string]string{"PATH": agentPath(e)}
	// Keep the agent on the same config as this shell.
	for _, k := range []string{"CG_DEV_AUTH_HOME", "XDG_CONFIG_HOME"} {
		if v := os.Getenv(k); v != "" {
			env[k] = v
		}
	}
	agent := launchd.Agent{
		Program:  prog,
		Args:     []string{"agent"},
		Interval: e.Config.RefreshInterval.D(),
		Env:      env,
		Log:      launchd.LogPath(e.Home),
	}
	if err := os.MkdirAll(filepath.Dir(agent.Log), 0o755); err != nil {
		return err
	}
	path := launchd.PlistPath(e.Home)
	if err := fsutil.WriteFileAtomic(path, []byte(agent.Plist()), 0o644); err != nil {
		return err
	}
	// bootout fails when the job is not loaded; that is fine.
	_, _ = e.exec(ctx, Cmd{Name: "launchctl", Args: []string{"bootout", launchdDomain() + "/" + launchd.Label}})
	if _, err := e.exec(ctx, Cmd{Name: "launchctl", Args: []string{"bootstrap", launchdDomain(), path}}); err != nil {
		return fmt.Errorf("load LaunchAgent: %w", err)
	}
	e.printf("✓ background refresh every %s (LaunchAgent %s, log %s)\n", agent.Interval, launchd.Label, agent.Log)
	return nil
}

// UninstallAgent unloads and deletes the LaunchAgent.
func UninstallAgent(ctx context.Context, e *Env) error {
	if e.goos() != "darwin" {
		return nil
	}
	path := launchd.PlistPath(e.Home)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	_, _ = e.exec(ctx, Cmd{Name: "launchctl", Args: []string{"bootout", launchdDomain() + "/" + launchd.Label}})
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	e.printf("✓ background refresh removed\n")
	return nil
}

// AgentStatus reports whether the LaunchAgent is installed and loaded.
func AgentStatus(ctx context.Context, e *Env) []Check {
	if e.goos() != "darwin" {
		return []Check{{Info, "", "background refresh is macOS-only for now (schedule `cg-dev-auth refresh` yourself)"}}
	}
	if _, err := os.Stat(launchd.PlistPath(e.Home)); err != nil {
		return []Check{{Warn, "launchd", "LaunchAgent not installed: npm/yarn tokens will expire (run: cg-dev-auth install)"}}
	}
	if _, err := e.exec(ctx, Cmd{Name: "launchctl", Args: []string{"print", launchdDomain() + "/" + launchd.Label}}); err != nil {
		return []Check{{Fail, "launchd", "LaunchAgent installed but not loaded (run: cg-dev-auth install)"}}
	}
	checks := []Check{{OK, "launchd", fmt.Sprintf("refreshing every %s", e.Config.RefreshInterval.D())}}
	if last := lastLogLine(launchd.LogPath(e.Home)); last != "" {
		checks = append(checks, Check{Info, "", "last run: " + last})
	}
	return checks
}

func lastLogLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// Agent is what the LaunchAgent runs: one refresh, logged with a timestamp.
// When chainctl needs an interactive login it posts a macOS notification (at
// most every notifyInterval) instead of opening a browser.
func Agent(ctx context.Context, e *Env) error {
	trimLog(launchd.LogPath(e.Home))
	var buf strings.Builder
	out := e.Out
	e.Out = &buf
	err := Refresh(ctx, e)
	e.Out = out

	stamp := e.Now().Format(time.RFC3339)
	msg := strings.ReplaceAll(strings.TrimSpace(buf.String()), "\n", "; ")
	if err != nil {
		e.printf("%s error: %s\n", stamp, strings.ReplaceAll(err.Error(), "\n", " "))
		if errors.Is(err, chainctl.ErrNotLoggedIn) {
			e.notify("Chainguard session expired: run `cg-dev-auth login`")
		}
		return err
	}
	if msg == "" {
		msg = "nothing to refresh"
	}
	e.printf("%s %s\n", stamp, msg)
	return nil
}

// trimLog empties the log once it passes maxLogBytes. launchd opens it in
// append mode, so truncating in place is safe.
func trimLog(path string) {
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLogBytes {
		_ = os.Truncate(path, 0)
	}
}

func (e *Env) notify(message string) {
	if e.goos() != "darwin" {
		return
	}
	stampFile := filepath.Join(e.ConfigDir, "last-notify")
	if fi, err := os.Stat(stampFile); err == nil && e.Now().Sub(fi.ModTime()) < notifyInterval {
		return
	}
	script := fmt.Sprintf("display notification %q with title %q", message, "cg-dev-auth")
	if _, err := e.exec(context.Background(), Cmd{Name: "osascript", Args: []string{"-e", script}}); err == nil {
		_ = fsutil.WriteFileAtomic(stampFile, []byte(e.Now().Format(time.RFC3339)+"\n"), 0o644)
	}
}
