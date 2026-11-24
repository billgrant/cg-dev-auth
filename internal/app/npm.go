package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/billgrant/cg-dev-auth/internal/fsutil"
	"github.com/billgrant/cg-dev-auth/internal/npmrc"
	"github.com/billgrant/cg-dev-auth/internal/yarnrc"
)

// NPM configures npm and Yarn Classic (~/.npmrc) and Yarn Berry
// (~/.yarnrc.yml) to use Chainguard Libraries for JavaScript with a
// short-lived bearer token from chainctl.
type NPM struct{}

const prevYarnServer = "npm.yarn.npmRegistryServer"

func (*NPM) Name() string    { return "npm" }
func (*NPM) UsesToken() bool { return true }

func npmrcPath(e *Env) string  { return filepath.Join(e.Home, ".npmrc") }
func yarnrcPath(e *Env) string { return filepath.Join(e.Home, ".yarnrc.yml") }

func (n *NPM) Apply(ctx context.Context, e *Env, token string, create bool) (bool, error) {
	a, err := n.applyNpmrc(e, token, create)
	if err != nil {
		return a, err
	}
	b, err := n.applyYarnrc(e, token, create)
	return a || b, err
}

func (*NPM) applyNpmrc(e *Env, token string, create bool) (bool, error) {
	path := npmrcPath(e)
	data, _, err := fsutil.ReadFileIfExists(path)
	if err != nil {
		return false, err
	}
	cur, managed, err := npmrc.Read(string(data))
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if !managed && !create {
		return false, nil
	}
	// Decide always-auth at install time; a refresh (possibly from launchd,
	// with a minimal PATH and no yarn) keeps what install chose.
	alwaysAuth := cur.AlwaysAuth
	if create {
		alwaysAuth = e.yarnClassic()
	}
	out, err := npmrc.Apply(string(data), npmrc.Settings{Registry: e.Config.JavaScriptRegistry(), Token: token, AlwaysAuth: alwaysAuth})
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if out == string(data) {
		return true, nil
	}
	if err := e.Store.EnsureBackup(path); err != nil {
		return false, err
	}
	// The file holds a bearer token, so keep it private to the user.
	return true, fsutil.WriteFileAtomic(path, []byte(out), 0o600)
}

func (*NPM) applyYarnrc(e *Env, token string, create bool) (bool, error) {
	path := yarnrcPath(e)
	reg := e.Config.JavaScriptRegistry()
	data, _, err := fsutil.ReadFileIfExists(path)
	if err != nil {
		return false, err
	}
	_, cur, err := yarnrc.Read(data, reg)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if cur == "" && !create {
		return false, nil
	}
	out, prev, err := yarnrc.Apply(data, yarnrc.Settings{Registry: reg, Token: token})
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if string(out) == string(data) {
		return true, nil
	}
	if prev == nil || *prev != yarnrcServer(reg) {
		e.Store.RememberPrev(prevYarnServer, prev)
	}
	if err := e.Store.EnsureBackup(path); err != nil {
		return false, err
	}
	return true, fsutil.WriteFileAtomic(path, out, 0o600)
}

func yarnrcServer(reg string) string {
	if len(reg) > 0 && reg[len(reg)-1] == '/' {
		return reg[:len(reg)-1]
	}
	return reg
}

func (*NPM) Status(ctx context.Context, e *Env, online bool) []Check {
	reg := e.Config.JavaScriptRegistry()
	var checks []Check

	path := npmrcPath(e)
	data, _, err := fsutil.ReadFileIfExists(path)
	var token string
	switch s, managed, perr := npmrc.Read(string(data)); {
	case err != nil:
		checks = append(checks, Check{Fail, ".npmrc", err.Error()})
	case perr != nil:
		checks = append(checks, Check{Fail, ".npmrc", perr.Error()})
	case !managed:
		checks = append(checks, Check{Fail, ".npmrc", "not configured (run: cg-dev-auth install)"})
	case s.Registry != reg:
		checks = append(checks, Check{Warn, ".npmrc", fmt.Sprintf("registry is %s, config says %s (run: cg-dev-auth install)", s.Registry, reg)})
	default:
		token = s.Token
		checks = append(checks, Check{OK, ".npmrc", "registry " + reg})
		checks = append(checks, tokenCheck(e, "", s.Token))
		for _, k := range npmrc.Conflicts(string(data), reg) {
			checks = append(checks, Check{Warn, "", fmt.Sprintf("%s also sets %q outside the managed block (the block wins)", path, k)})
		}
	}

	ypath := yarnrcPath(e)
	ydata, _, err := fsutil.ReadFileIfExists(ypath)
	if err != nil {
		checks = append(checks, Check{Fail, ".yarnrc.yml", err.Error()})
	} else if server, ytok, err := yarnrc.Read(ydata, reg); err != nil {
		checks = append(checks, Check{Fail, ".yarnrc.yml", err.Error()})
	} else if ytok == "" {
		checks = append(checks, Check{Warn, ".yarnrc.yml", "not configured (Yarn Berry will not use Chainguard; run: cg-dev-auth install)"})
	} else {
		if server != yarnrcServer(reg) {
			checks = append(checks, Check{Warn, ".yarnrc.yml", fmt.Sprintf("npmRegistryServer is %s (run: cg-dev-auth install)", server)})
		} else {
			checks = append(checks, Check{OK, ".yarnrc.yml", "registry " + server})
		}
		checks = append(checks, tokenCheck(e, "", ytok))
		if token == "" {
			token = ytok
		}
	}

	if online && token != "" {
		checks = append(checks, probe(ctx, e, "registry", reg+"lodash", "Bearer "+token))
	}
	return checks
}
