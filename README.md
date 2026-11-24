# cg-dev-auth

Point a developer machine's package tools at Chainguard, and keep their
credentials fresh, without an artifact manager (Nexus, Artifactory) in between.

| Tool | Pulls from | Credentials |
|---|---|---|
| npm, Yarn Classic | `libraries.cgr.dev/javascript/` | short-lived token in `~/.npmrc`, refreshed in the background |
| Yarn Berry (2+) | `libraries.cgr.dev/javascript` | short-lived token in `~/.yarnrc.yml`, refreshed in the background |
| pip | `libraries.cgr.dev/python/simple/` | [keyrings-chainguard-libraries](https://github.com/chainguard-dev/keyrings-chainguard-libraries): token fetched from chainctl on demand, never stored |
| uv | `libraries.cgr.dev/python/simple/` | same keyring backend |
| Docker | `cgr.dev/ORG/...` | chainctl's credential helper (`docker-credential-cgr`), on demand |

Every credential comes from your `chainctl` login. chainctl renews tokens
from its cached refresh token without opening a browser, so after one
`chainctl auth login` everything keeps working until that refresh token
itself expires. When it does, you get a macOS notification instead of
surprise 401s.

## Install

```sh
brew install billgrant/tap/cg-dev-auth   # or: go install github.com/billgrant/cg-dev-auth/cmd/cg-dev-auth@latest
```

Prerequisites: [`chainctl`](https://edu.chainguard.dev/chainguard/chainctl-usage/how-to-install-chainctl/)
(logged in), and `uv` or `pipx` for the Python keyring backend.

## Quick start

```sh
cg-dev-auth init --org your-org.example   # writes ~/.config/cg-dev-auth/config.yaml
cg-dev-auth install                        # configures every tool, starts background refresh
cg-dev-auth status                         # checks config, token lifetimes, live registry access
```

See [TESTING.md](TESTING.md) for recipes that check each tool really pulls
from Chainguard (using `chainctl libraries verify`), and for comparing
upstream with Chainguard.

To use this on another machine with another org, only the `--org` changes.
The Libraries URLs are the same for every org (your login picks the org);
only Docker image paths (`cgr.dev/ORG/...`) and named repositories use it.

## Commands

| Command | |
|---|---|
| `init --org ORG [--ecosystems npm,python,docker] [--python-index python-remediated] [--repo NAME]` | Write the config |
| `install [--no-agent]` | Configure every enabled tool; on macOS also install the LaunchAgent |
| `status` (`doctor`) `[--offline]` | Show what is configured and whether it works |
| `refresh` | Write a fresh token into the npm/yarn config now (never opens a browser) |
| `off [npm\|yarn\|python\|pip\|uv\|docker]...` | Switch tools back to your previous registries, e.g. to compare upstream and Chainguard |
| `on [...]` | Switch them back to Chainguard |
| `login` | `chainctl auth login`, then refresh |
| `uninstall` | Undo everything (backups and config are kept) |

## How it works

**npm / Yarn Classic.** A managed block goes at the end of `~/.npmrc` (npm
lets the last occurrence of a key win):

```ini
# >>> cg-dev-auth >>> (managed block: edit with cg-dev-auth, not by hand)
registry=https://libraries.cgr.dev/javascript/
//libraries.cgr.dev/javascript/:_authToken=<token from chainctl auth token --audience=libraries.cgr.dev>
# <<< cg-dev-auth <<<
```

`always-auth=true` is added only when Yarn 1.x is installed (Yarn Classic
needs it; npm 11+ warns about it). Nothing outside the block is touched.

**Yarn Berry.** `npmRegistryServer` plus an `npmRegistries` entry with
`npmAuthToken` in `~/.yarnrc.yml`. The YAML is edited as a tree, so your
comments and other keys stay.

**Background refresh (macOS).** The LaunchAgent `dev.cg-dev-auth.refresh`
runs `cg-dev-auth agent` at login and every `refresh_interval` (5m by
default). It writes only when chainctl hands out a new token. Log:
`~/Library/Logs/cg-dev-auth.log`. On Linux, schedule `cg-dev-auth refresh`
yourself (cron `*/5`).

**pip / uv.** `install` installs `keyring` with Chainguard's backend as a uv
(or pipx) tool, pulling the packages from Chainguard Libraries itself, then
sets:

```ini
# pip.conf (~/.config/pip/pip.conf, or ~/Library/Application Support/pip/pip.conf if that directory exists)
[global]
index-url = https://_token@libraries.cgr.dev/python/simple/
keyring-provider = subprocess
```

```toml
# ~/.config/uv/uv.toml (keyring-provider is in a managed block at the top, the index at the end)
keyring-provider = "subprocess"

[[index]]
name = "chainguard"
url = "https://_token@libraries.cgr.dev/python/simple/"
default = true
authenticate = "always"
```

`_token` is the username the backend answers for. The token comes from
`chainctl` each time pip or uv asks, so nothing needs refreshing.

**Docker.** `chainctl auth configure-docker` sets `credHelpers` for
`cgr.dev` (and its sibling hosts) to `cgr`; Docker Desktop's
`credsStore: osxkeychain` still handles every other registry. Your
`FROM`/`image:` lines are untouched: pull `cgr.dev/ORG/IMAGE:TAG` explicitly.

**Undo.** Each file is copied to `~/.config/cg-dev-auth/backups/` before its
first edit. `state.json` records any value that was replaced, so `off` and
`uninstall` put back what was there and leave your later edits alone.

## Caveats

- **Project config wins.** A project `.npmrc`/`.yarnrc.yml`, `[[tool.uv.index]]`
  in `pyproject.toml`, or `--index-url` in a requirements file overrides the
  user-level settings. `status` also flags environment variables
  (`PIP_INDEX_URL`, `UV_INDEX_URL`, ...) that override them.
- **GUI apps and PATH.** pip and uv call `keyring`, which calls `chainctl`;
  both have to be on the PATH of whatever launches pip or uv (IDEs started
  from the Dock may not see `~/.local/bin`). Docker Desktop needs
  `docker-credential-cgr` on its PATH (Homebrew's `/opt/homebrew/bin` is).
- **Lockfiles.** Existing lockfiles still point at the registries they were
  made with; see `chainctl libraries update-hashes`.
- **Upstream fallback** is a server-side setting on your Libraries repository.
  cg-dev-auth adds no client-side fallback to npmjs or PyPI.

## Development

```sh
go test ./...
go build -o cg-dev-auth ./cmd/cg-dev-auth
```

Releases: push a `v*` tag. GoReleaser builds darwin/linux binaries for arm64
and amd64 and updates the cask in `billgrant/homebrew-tap`. That needs a
`HOMEBREW_TAP_GITHUB_TOKEN` repository secret with contents:write on the tap.
