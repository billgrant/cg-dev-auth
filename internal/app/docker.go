package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/billgrant/cg-dev-auth/internal/dockercfg"
	"github.com/billgrant/cg-dev-auth/internal/fsutil"
)

// Docker sets up chainctl's Docker credential helper (docker-credential-cgr)
// for cgr.dev. The helper mints registry tokens from the chainctl session on
// demand, so nothing is stored and nothing needs refreshing. Image references
// (cgr.dev/ORG/IMAGE) are left to the user.
type Docker struct{}

const (
	dockerHost       = "cgr.dev"
	dockerHelper     = "cgr"
	dockerHelperBin  = "docker-credential-cgr"
	prevDockerPrefix = "docker.credHelpers."
)

func (*Docker) Name() string    { return "docker" }
func (*Docker) UsesToken() bool { return false }

func (*Docker) Apply(ctx context.Context, e *Env, _ string, create bool) (bool, error) {
	if !create {
		return false, nil
	}
	path := dockercfg.Path(e.Home)
	before, _, err := fsutil.ReadFileIfExists(path)
	if err != nil {
		return false, err
	}
	beforeHelpers, err := dockercfg.CredHelpers(before)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if beforeHelpers[dockerHost] == dockerHelper {
		return true, nil
	}
	if err := e.Store.EnsureBackup(path); err != nil {
		return false, err
	}
	// chainctl knows which hosts its helper serves; let it write the entries.
	_, runErr := e.Chainctl.Runner.Run(ctx, "auth", "configure-docker")

	after, _, err := fsutil.ReadFileIfExists(path)
	if err != nil {
		return false, err
	}
	afterHelpers, err := dockercfg.CredHelpers(after)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if afterHelpers[dockerHost] != dockerHelper {
		if runErr != nil {
			return false, runErr
		}
		return false, fmt.Errorf("chainctl auth configure-docker did not set credHelpers[%q] in %s", dockerHost, path)
	}
	// Remember what each changed host pointed at, so uninstall can undo it.
	for host, v := range afterHelpers {
		if old, ok := beforeHelpers[host]; !ok {
			e.Store.RememberPrev(prevDockerPrefix+host, nil)
		} else if old != v {
			o := old
			e.Store.RememberPrev(prevDockerPrefix+host, &o)
		}
	}
	return true, nil
}

func (*Docker) Status(ctx context.Context, e *Env, online bool) []Check {
	var checks []Check
	path := dockercfg.Path(e.Home)
	data, _, err := fsutil.ReadFileIfExists(path)
	if err != nil {
		return []Check{{Fail, "config.json", err.Error()}}
	}
	helpers, err := dockercfg.CredHelpers(data)
	switch {
	case err != nil:
		checks = append(checks, Check{Fail, "config.json", err.Error()})
	case helpers[dockerHost] != dockerHelper:
		checks = append(checks, Check{Fail, "config.json", fmt.Sprintf("credHelpers[%q] not set to %q (run: cg-dev-auth install)", dockerHost, dockerHelper)})
	default:
		var hosts []string
		for h, v := range helpers {
			if v == dockerHelper {
				hosts = append(hosts, h)
			}
		}
		slices.Sort(hosts)
		checks = append(checks, Check{OK, "config.json", "credential helper for " + strings.Join(hosts, ", ")})
	}

	if _, err := e.lookPath(dockerHelperBin); err != nil {
		checks = append(checks, Check{Fail, "helper", dockerHelperBin + " not on PATH (it ships with chainctl; reinstall chainctl)"})
	} else if online {
		out, err := e.exec(ctx, Cmd{Name: dockerHelperBin, Args: []string{"get"}, Stdin: dockerHost})
		var cred struct{ Secret string }
		if err != nil || json.Unmarshal(out, &cred) != nil || cred.Secret == "" {
			msg := "helper returned no credential for " + dockerHost
			if err != nil {
				msg += ": " + firstLine(err.Error())
			}
			checks = append(checks, Check{Fail, "helper", msg})
		} else {
			checks = append(checks, Check{OK, "helper", "returns a credential for " + dockerHost})
		}
	}
	checks = append(checks, Check{Info, "", fmt.Sprintf("pull with: docker pull %s/%s/IMAGE:TAG", dockerHost, e.Config.Org)})
	return checks
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
