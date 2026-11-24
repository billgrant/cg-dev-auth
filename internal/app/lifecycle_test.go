package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billgrant/cg-dev-auth/internal/dockercfg"
	"github.com/billgrant/cg-dev-auth/internal/launchd"
)

// fullEnv sets up an Env managing all three ecosystems with fakes for every
// external command, plus pre-existing user config files.
func fullEnv(t *testing.T) (*Env, *fakeRunner, map[string]string, *[]Cmd) {
	t.Helper()
	f := &fakeRunner{token: jwt(time.Now().Add(time.Hour))}
	e, _ := newTestEnv(t, f)
	e.Config.Ecosystems = []string{"npm", "python", "docker"}
	cfgHome := filepath.Join(e.Home, ".config")
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("DOCKER_CONFIG", filepath.Join(e.Home, ".docker"))

	originals := map[string]string{
		filepath.Join(e.Home, ".npmrc"):                 "save-exact=true\n",
		filepath.Join(cfgHome, "pip", "pip.conf"):       "[global]\nindex-url = https://pypi.org/simple\ntimeout = 30\n",
		filepath.Join(e.Home, ".docker", "config.json"): "{\n\t\"credsStore\": \"osxkeychain\"\n}\n",
	}
	for p, c := range originals {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(c), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	dockerPath := filepath.Join(e.Home, ".docker", "config.json")
	f.onRun = func(args []string) {
		if strings.Join(args, " ") == "auth configure-docker" {
			data, _ := os.ReadFile(dockerPath)
			cgr := "cgr"
			data, _ = dockercfg.SetCredHelpers(data, map[string]*string{"cgr.dev": &cgr, "uploads.cgr.dev": &cgr})
			os.WriteFile(dockerPath, data, 0o644)
		}
	}
	keyringInstalled := false
	var cmds []Cmd
	e.LookPath = func(name string) (string, error) {
		if name == "uv" || name == "chainctl" {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	e.Exec = func(_ context.Context, c Cmd) ([]byte, error) {
		cmds = append(cmds, c)
		switch c.Name {
		case "keyring":
			if keyringInstalled {
				return []byte("keyrings.chainctl_auth.ChainctlAuth (priority: 9)\n"), nil
			}
			return nil, errors.New("not found")
		case "uv":
			keyringInstalled = c.Args[1] == "install"
			return nil, nil
		}
		return nil, nil
	}
	return e, f, originals, &cmds
}

func TestOffOnUninstallRestoresEverything(t *testing.T) {
	e, _, originals, cmds := fullEnv(t)
	ctx := context.Background()
	if err := Install(ctx, e, InstallOptions{NoAgent: true}); err != nil {
		t.Fatal(err)
	}
	created := []string{
		filepath.Join(e.Home, ".yarnrc.yml"),
		filepath.Join(e.Home, ".config", "uv", "uv.toml"),
	}
	for _, p := range created {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s not created: %v", p, err)
		}
	}

	// off: everything back to the original, files we created gone.
	if err := Off(ctx, e, nil); err != nil {
		t.Fatal(err)
	}
	assertOriginals(t, originals, created)
	if got := e.Store.State().Disabled; len(got) != 3 {
		t.Fatalf("disabled = %v", got)
	}
	// Refresh while off must not touch anything.
	if err := Refresh(ctx, e); err != nil {
		t.Fatal(err)
	}
	assertOriginals(t, originals, created)

	// on (just npm via its yarn alias): npm comes back, others stay off.
	if err := On(ctx, e, []string{"yarn"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, filepath.Join(e.Home, ".npmrc")), "_authToken=") {
		t.Fatal("npm not switched back on")
	}
	if got := e.Store.State().Disabled; len(got) != 2 {
		t.Fatalf("disabled = %v", got)
	}
	if err := On(ctx, e, nil); err != nil {
		t.Fatal(err)
	}

	if err := Uninstall(ctx, e); err != nil {
		t.Fatal(err)
	}
	assertOriginals(t, originals, created)
	if _, err := os.Stat(e.Store.Path()); !os.IsNotExist(err) {
		t.Fatal("state.json not removed")
	}
	var uninstalled bool
	for _, c := range *cmds {
		if c.String() == "uv tool uninstall keyring" {
			uninstalled = true
		}
	}
	if !uninstalled {
		t.Fatal("keyring tool installed by cg-dev-auth was not uninstalled")
	}
}

func assertOriginals(t *testing.T, originals map[string]string, created []string) {
	t.Helper()
	for p, want := range originals {
		if got := read(t, p); got != want {
			t.Errorf("%s not restored:\n%s\nwant:\n%s", p, got, want)
		}
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
			t.Errorf("%s mode %v, want 0640", p, fi.Mode().Perm())
		}
	}
	for _, p := range created {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should have been deleted", p)
		}
	}
}

func TestUserEditsSurviveOff(t *testing.T) {
	e, _, _, _ := fullEnv(t)
	ctx := context.Background()
	if err := Install(ctx, e, InstallOptions{NoAgent: true}); err != nil {
		t.Fatal(err)
	}
	// The user adds a setting after install.
	p := filepath.Join(e.Home, ".npmrc")
	data := read(t, p)
	os.WriteFile(p, []byte("fund=false\n"+data), 0o600)
	if err := Off(ctx, e, []string{"npm"}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); got != "fund=false\nsave-exact=true\n" {
		t.Fatalf("got %q", got)
	}
}

func TestAgentOnDarwin(t *testing.T) {
	e, f, _, cmds := fullEnv(t)
	e.Config.Ecosystems = []string{"npm"}
	e.OS = "darwin"
	ctx := context.Background()
	if err := Install(ctx, e, InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	plist := read(t, launchd.PlistPath(e.Home))
	for _, want := range []string{"<string>agent</string>", "<integer>300</integer>", "<key>PATH</key>", "/usr/bin:/opt/homebrew/bin"} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %q:\n%s", want, plist)
		}
	}
	var bootstrapped bool
	for _, c := range *cmds {
		if c.Name == "launchctl" && c.Args[0] == "bootstrap" {
			bootstrapped = true
		}
	}
	if !bootstrapped {
		t.Fatal("launchctl bootstrap not called")
	}

	// A refresh failure that needs a login notifies once, then is throttled.
	f.err = errors.New("no credentials available non-interactively")
	notifications := func() int {
		n := 0
		for _, c := range *cmds {
			if c.Name == "osascript" {
				n++
			}
		}
		return n
	}
	for i := 0; i < 3; i++ {
		if err := Agent(ctx, e); err == nil {
			t.Fatal("expected error")
		}
	}
	if n := notifications(); n != 1 {
		t.Fatalf("%d notifications, want 1", n)
	}

	if err := Uninstall(ctx, e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(launchd.PlistPath(e.Home)); !os.IsNotExist(err) {
		t.Fatal("plist not removed")
	}
}

func TestAgentLogsSuccess(t *testing.T) {
	e, _, _, _ := fullEnv(t)
	e.Config.Ecosystems = []string{"npm"}
	ctx := context.Background()
	if err := Install(ctx, e, InstallOptions{NoAgent: true}); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	e.Out = &buf
	if err := Agent(ctx, e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "npm token refreshed") || strings.Count(buf.String(), "\n") != 1 {
		t.Fatalf("log = %q", buf.String())
	}
}

func TestDockerConfigCreatedByChainctlIsDeleted(t *testing.T) {
	e, _, _, _ := fullEnv(t)
	e.Config.Ecosystems = []string{"docker"}
	path := filepath.Join(e.Home, ".docker", "config.json")
	os.Remove(path)
	ctx := context.Background()
	if err := Install(ctx, e, InstallOptions{NoAgent: true}); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(ctx, e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config.json left behind: %s", read(t, path))
	}
}

func TestDockerUserConfigKeptEvenIfEmptyAfterRestore(t *testing.T) {
	e, _, _, _ := fullEnv(t)
	e.Config.Ecosystems = []string{"docker"}
	path := filepath.Join(e.Home, ".docker", "config.json")
	orig := "{\n\t\"auths\": {}\n}\n"
	os.WriteFile(path, []byte(orig), 0o640)
	ctx := context.Background()
	if err := Install(ctx, e, InstallOptions{NoAgent: true}); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != orig {
		t.Fatalf("got %q", got)
	}
}
