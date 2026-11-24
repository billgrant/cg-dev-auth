package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/billgrant/cg-dev-auth/internal/dockercfg"
	"github.com/billgrant/cg-dev-auth/internal/fsutil"
	"github.com/billgrant/cg-dev-auth/internal/npmrc"
	"github.com/billgrant/cg-dev-auth/internal/pipconf"
	"github.com/billgrant/cg-dev-auth/internal/uvtoml"
	"github.com/billgrant/cg-dev-auth/internal/yarnrc"
)

// writeRestored writes content back to path after an undo. When cg-dev-auth
// created the file and nothing else is left in it, the file is deleted. The
// original permissions are put back when the file existed before.
func writeRestored(e *Env, path string, content []byte) error {
	rec := e.Store.State().Files[path]
	if rec != nil && !rec.Existed && len(strings.TrimSpace(string(content))) == 0 {
		err := os.Remove(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	perm := fs.FileMode(0o644)
	if rec != nil && rec.Existed && rec.Mode != 0 {
		perm = rec.Mode
	} else if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
	}
	return fsutil.WriteFileAtomic(path, content, perm)
}

// restoreFile applies undo to path's contents, if the file exists.
func restoreFile(e *Env, path string, undo func([]byte) ([]byte, error)) error {
	data, exists, err := fsutil.ReadFileIfExists(path)
	if err != nil || !exists {
		return err
	}
	out, err := undo(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if string(out) == string(data) {
		return nil
	}
	return writeRestored(e, path, out)
}

func (*NPM) Remove(_ context.Context, e *Env) error {
	reg := e.Config.JavaScriptRegistry()
	err1 := restoreFile(e, npmrcPath(e), func(b []byte) ([]byte, error) {
		s, err := npmrc.Remove(string(b))
		return []byte(s), err
	})
	prev, _ := e.Store.Prev(prevYarnServer)
	err2 := restoreFile(e, yarnrcPath(e), func(b []byte) ([]byte, error) {
		return yarnrc.Remove(b, reg, prev)
	})
	return errors.Join(err1, err2)
}

func (*Python) Remove(_ context.Context, e *Env) error {
	want := indexURLWithUser(e.Config.PythonIndexURL())
	err1 := restoreFile(e, pipConfPath(e), func(b []byte) ([]byte, error) {
		s := string(b)
		// Only undo values that are still ours; leave user edits alone.
		if v, ok := pipconf.Get(s, "index-url"); ok && v == want {
			prev, _ := e.Store.Prev(prevPipIndex)
			s = pipconf.Restore(s, "index-url", prev)
		}
		if v, ok := pipconf.Get(s, "keyring-provider"); ok && v == "subprocess" {
			prev, _ := e.Store.Prev(prevPipKeyring)
			s = pipconf.Restore(s, "keyring-provider", prev)
		}
		return []byte(s), nil
	})
	err2 := restoreFile(e, uvTomlPath(e), func(b []byte) ([]byte, error) {
		s, err := uvtoml.Remove(string(b))
		return []byte(s), err
	})
	return errors.Join(err1, err2)
}

func (*Docker) Remove(_ context.Context, e *Env) error {
	path := dockercfg.Path(e.Home)
	return restoreFile(e, path, func(b []byte) ([]byte, error) {
		cur, err := dockercfg.CredHelpers(b)
		if err != nil {
			return nil, err
		}
		changes := map[string]*string{}
		for key, prev := range e.Store.State().Prev {
			host, ok := strings.CutPrefix(key, prevDockerPrefix)
			if !ok || cur[host] != dockerHelper {
				continue // not ours, or changed since: leave it
			}
			changes[host] = prev
		}
		if len(changes) == 0 {
			return b, nil
		}
		out, err := dockercfg.SetCredHelpers(b, changes)
		if err != nil {
			return nil, err
		}
		// chainctl may have created the file with an empty "auths"; report it
		// as empty so a file cg-dev-auth caused to exist is deleted.
		if rec := e.Store.State().Files[path]; rec != nil && !rec.Existed && dockercfg.IsEmpty(out) {
			return nil, nil
		}
		return out, nil
	})
}

// selectEcosystems resolves names (empty = all configured) to ecosystems.
func selectEcosystems(e *Env, names []string) ([]Ecosystem, error) {
	if len(names) == 0 {
		names = e.Config.Ecosystems
	}
	var out []Ecosystem
	for _, n := range names {
		if n == "yarn" {
			n = "npm"
		}
		if n == "pip" || n == "uv" {
			n = "python"
		}
		var found Ecosystem
		for _, eco := range Ecosystems() {
			if eco.Name() == n {
				found = eco
			}
		}
		if found == nil {
			return nil, fmt.Errorf("unknown ecosystem %q", n)
		}
		if !slices.ContainsFunc(out, func(x Ecosystem) bool { return x.Name() == n }) {
			out = append(out, found)
		}
	}
	return out, nil
}

// Off switches ecosystems back to their upstream registries (the settings in
// place before cg-dev-auth), remembering that they are off so refresh skips
// them. `On` reverses it.
func Off(ctx context.Context, e *Env, names []string) error {
	ecos, err := selectEcosystems(e, names)
	if err != nil {
		return err
	}
	var errs []error
	st := e.Store.State()
	for _, eco := range ecos {
		if err := eco.Remove(ctx, e); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", eco.Name(), err))
			continue
		}
		if !slices.Contains(st.Disabled, eco.Name()) {
			st.Disabled = append(st.Disabled, eco.Name())
		}
		e.printf("✓ %s switched off (using your previous registry settings)\n", eco.Name())
	}
	if err := e.Store.Save(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// On switches ecosystems back to Chainguard.
func On(ctx context.Context, e *Env, names []string) error {
	ecos, err := selectEcosystems(e, names)
	if err != nil {
		return err
	}
	st := e.Store.State()
	for _, eco := range ecos {
		st.Disabled = slices.DeleteFunc(st.Disabled, func(d string) bool { return d == eco.Name() })
	}
	var token string
	if e.needsToken(ecos) {
		if token, err = e.Chainctl.LibrariesToken(ctx); err != nil {
			return err
		}
	}
	var errs []error
	for _, eco := range ecos {
		if _, err := eco.Apply(ctx, e, token, true); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", eco.Name(), err))
			continue
		}
		e.printf("✓ %s switched on (Chainguard)\n", eco.Name())
	}
	if err := e.Store.Save(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Uninstall undoes everything: tool configuration, the background agent, the
// keyring tool if cg-dev-auth installed it, and the recorded state. Backups
// are kept.
func Uninstall(ctx context.Context, e *Env) error {
	var errs []error
	for _, eco := range Ecosystems() {
		if err := eco.Remove(ctx, e); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", eco.Name(), err))
			continue
		}
		e.printf("✓ %s configuration removed\n", eco.Name())
	}
	if err := UninstallAgent(ctx, e); err != nil {
		errs = append(errs, err)
	}
	if v, ok := e.Store.Prev(prevKeyringInstaller); ok && v != nil {
		var c Cmd
		switch *v {
		case "uv":
			c = Cmd{Name: "uv", Args: []string{"tool", "uninstall", "keyring"}}
		case "pipx":
			c = Cmd{Name: "pipx", Args: []string{"uninstall", "keyring"}}
		}
		if c.Name != "" {
			if _, err := e.exec(ctx, c); err != nil {
				errs = append(errs, err)
			} else {
				e.printf("✓ keyring tool removed (%s)\n", *v)
			}
		}
	}
	if len(errs) > 0 {
		// Keep the state so a second run can finish the job.
		return errors.Join(errs...)
	}
	if err := os.Remove(e.Store.Path()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	e.printf("· backups kept in %s/backups; config kept in %s\n", e.ConfigDir, e.ConfigPath())
	return nil
}
