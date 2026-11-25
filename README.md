# cg-dev-auth

Point your Mac's package tools at Chainguard, and keep their credentials
fresh, without an artifact manager (Nexus, Artifactory) in between.

## Why this exists

Without an artifact manager, using Chainguard from a laptop means setting up
every package tool by hand, and each one does it differently:

- **Point it at Chainguard.** `registry=` in `~/.npmrc`, `npmRegistryServer`
  in `~/.yarnrc.yml`, `index-url` in `pip.conf`, an `[[index]]` in `uv.toml`.
  Each file lives somewhere different, and each has its own format and quirks.
- **Give it credentials.** Usually a pull token pasted into the same files.
  Pull tokens expire (after 30 days, a week or a year depending on the plan),
  and nothing warns you. You find out when `npm install` or `docker pull`
  suddenly fails with a 401, then you go hunting for which file holds the
  stale token. In the middle of a demo that's embarrassing. On a real team it
  can mean a developer is blocked until someone with the right access issues
  a new token, sometimes through a ticket.

cg-dev-auth does both, for every tool, with one command. It writes each
tool's registry settings so they point at Chainguard, and it removes the
long-lived token: each tool gets credentials from your `chainctl` login,
which renews itself in the background, so pulls keep working without anyone
tracking expiry dates. The same command works on any Mac for any
organization (only `--org` changes), and `cg-dev-auth off` switches the
tools back to their previous registries when you want to compare.

## What it configures

| Tool | Pulls from | Credentials |
|---|---|---|
| npm, Yarn Classic | `libraries.cgr.dev/javascript/` | short-lived token in `~/.npmrc`, refreshed in the background |
| Yarn Berry (2+) | `libraries.cgr.dev/javascript` | short-lived token in `~/.yarnrc.yml`, refreshed in the background |
| pip | `libraries.cgr.dev/python/simple/` | [keyrings-chainguard-libraries](https://github.com/chainguard-dev/keyrings-chainguard-libraries): token fetched from chainctl on demand, never stored |
| uv | `libraries.cgr.dev/python/simple/` | same keyring backend |
| Docker | `cgr.dev/ORG/...` | chainctl's credential helper (`docker-credential-cgr`), on demand |

Every credential comes from your `chainctl` login. There are no API keys or
pull tokens to create, store or rotate.

> A personal project, not an official Chainguard product. macOS is the main
> target; Linux works without the background refresh (see [Linux](#linux)).

## Before you start

- **macOS with [Homebrew](https://brew.sh).**
- **[`chainctl`](https://edu.chainguard.dev/chainguard/chainctl-usage/how-to-install-chainctl/), logged in:**
  `brew install chainguard-dev/tap/chainctl && chainctl auth login`.
- **Your organization name:** `chainctl iam organizations list`. It looks
  like `example.com`.
- **Libraries entitlements** for the ecosystems you want:
  `chainctl libraries entitlements list --parent YOUR_ORG`. Without one, that
  ecosystem's installs fail with 401/403.
- **For Python:** `uv` (`brew install uv`) or `pipx`, used to install the
  keyring backend.
- **For Docker:** Docker Desktop (or another Docker that reads
  `~/.docker/config.json`).

> **It changes your user-level config.** If `~/.npmrc`, `~/.yarnrc.yml`,
> `pip.conf` or `uv.toml` already point somewhere else (a company
> Artifactory, say), cg-dev-auth overrides that registry setting. It backs
> each file up first and records what it replaced, and `cg-dev-auth off`
> puts your previous settings back at any time.

## Install

```sh
brew install billgrant/tap/cg-dev-auth
```

Or with Go 1.24+: `go install github.com/billgrant/cg-dev-auth/cmd/cg-dev-auth@latest`.

## Quick start

```sh
cg-dev-auth init --org YOUR_ORG   # writes ~/.config/cg-dev-auth/config.yaml
cg-dev-auth install               # configures every tool, starts the background refresh
cg-dev-auth status                # checks config, token lifetimes and live registry access
```

To manage only some tools: `cg-dev-auth init --org YOUR_ORG --ecosystems npm,python`
(the options are `npm`, which covers Yarn too, `python`, which covers pip and uv, and `docker`).

A healthy `status` looks like this:

```text
chainctl
  ✓ session      logged in (renews without a browser)
npm
  ✓ .npmrc       registry https://libraries.cgr.dev/javascript/
  ✓ token valid for 39m0s
  ✓ .yarnrc.yml  registry https://libraries.cgr.dev/javascript
  ✓ token valid for 39m0s
  ✓ registry     GET https://libraries.cgr.dev/javascript/lodash → 200
python
  ✓ keyring      Chainguard backend installed
  ✓ pip.conf     index https://libraries.cgr.dev/python/simple/
  ✓ uv.toml      index https://libraries.cgr.dev/python/simple/
  ✓ registry     GET https://libraries.cgr.dev/python/simple/requests/ → 200
docker
  ✓ config.json  credential helper for cgr.dev, skills.cgr.dev, uploads.cgr.dev
  ✓ helper       returns a credential for cgr.dev
background
  ✓ launchd      refreshing every 5m0s
```

Then try a tool for real: [TESTING.md](TESTING.md) has a recipe for each
one that confirms the packages came from Chainguard, using
`chainctl libraries verify`. It also shows how to compare upstream with
Chainguard.

**On another machine or org,** only `--org` changes. The Libraries URLs are
the same for every org, and your login picks the org. Only Docker image
paths (`cgr.dev/ORG/...`) and named repositories use the org name.

## Commands

| Command | |
|---|---|
| `init --org ORG [--ecosystems npm,python,docker] [--python-index python-remediated] [--repo NAME] [--force]` | Write the config |
| `install [--no-agent]` | Configure every enabled tool; on macOS also install the background refresh. Safe to re-run |
| `status` (`doctor`) `[--offline]` | Show what is configured and whether it works |
| `refresh` | Write a fresh token into the npm/yarn config now (never opens a browser) |
| `off [npm\|yarn\|python\|pip\|uv\|docker]...` | Switch tools back to your previous registries, e.g. to compare upstream and Chainguard (all tools when none are named) |
| `on [...]` | Switch them back to Chainguard |
| `login` | `chainctl auth login` (opens a browser), then refresh |
| `uninstall` | Undo everything. Backups and `~/.config/cg-dev-auth/config.yaml` are kept |

## When your chainctl login expires

chainctl mints the short-lived tokens (about an hour each) from the refresh
token that `chainctl auth login` saved, with no browser involved. Day to day
you do nothing. When that refresh token itself expires:

- The background refresh fails, and you get a macOS notification:
  *"Chainguard session expired: run `cg-dev-auth login`"* (at most once
  every 2 hours).
- npm and yarn keep working until their last token expires (up to about an
  hour), then fail with E401.
- pip, uv and Docker fail on their next request.

Run `cg-dev-auth login`, sign in in the browser, and everything works
again. Nothing needs reinstalling.

## Troubleshooting

| Symptom | Fix |
|---|---|
| npm/yarn `E401` | `cg-dev-auth login` (the chainctl login expired), then `cg-dev-auth status` |
| Yarn 1.x: `Couldn't find package "..." on the "npm" registry` | `~/.npmrc` is missing `always-auth`. Run `cg-dev-auth install` again (it detects Yarn 1.x); the background refresh also adds it within 5 minutes |
| Yarn 2+: `The nearest package directory ... doesn't seem to be part of the project declared in ...` | A `package.json` in a parent directory. Run `touch yarn.lock` in your project, or remove the stray file |
| `status`: `keyring ... not found on PATH` | `uv tool update-shell` (or `pipx ensurepath`), then open a new terminal |
| `docker pull cgr.dev/...`: `unauthorized` | Check `cg-dev-auth status` (the `helper` line), and that your org can pull that image: `chainctl images repos list --parent YOUR_ORG` |
| `brew install` won't overwrite `/opt/homebrew/bin/cg-dev-auth` | It's a copy you built by hand: delete it, run `brew install` again, then `cg-dev-auth install` |
| A "session expired" notification while offline | Expected for now: cg-dev-auth can't yet tell "offline" from "login expired". It clears once you're back online |
| `init`: `config.yaml already exists` | `cg-dev-auth init --org YOUR_ORG --force` |

The background refresh logs every run to `~/Library/Logs/cg-dev-auth.log`.
Check that `launchctl print gui/$(id -u)/dev.cg-dev-auth.refresh` shows the
job. `state = not running` is normal: it runs for a moment every 5 minutes
and exits.

## How it works

**npm / Yarn Classic.** A managed block goes at the end of `~/.npmrc` (npm
lets the last occurrence of a key win):

```ini
# >>> cg-dev-auth >>> (managed block: edit with cg-dev-auth, not by hand)
registry=https://libraries.cgr.dev/javascript/
//libraries.cgr.dev/javascript/:_authToken=<token from chainctl auth token --audience=libraries.cgr.dev>
# <<< cg-dev-auth <<<
```

`always-auth=true` is added when Yarn 1.x is installed: Yarn Classic needs
it, and npm 11+ warns about it. Nothing outside the block is touched.

**Yarn Berry.** `npmRegistryServer` plus an `npmRegistries` entry with
`npmAuthToken` in `~/.yarnrc.yml`. The YAML is edited as a tree, so your
comments and other keys stay.

**Background refresh (macOS).** The LaunchAgent `dev.cg-dev-auth.refresh`
runs `cg-dev-auth agent` at login and every `refresh_interval` (5m by
default). It writes only when chainctl hands out a new token, and launchd
catches up after the Mac wakes from sleep.

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

## Linux

Everything except the background refresh works on Linux. npm and yarn store
a token that lasts about an hour, so schedule a refresh yourself, e.g.
with cron:

```cron
*/5 * * * * /path/to/cg-dev-auth refresh >/dev/null 2>&1
```

## Roadmap

See [ROADMAP.md](ROADMAP.md): next up are better login-expiry handling, then
Go, .NET and Java, and APK packages.

## Development

```sh
go test ./...
go build -o cg-dev-auth ./cmd/cg-dev-auth
```

Releases: push a `v*` tag. GoReleaser builds darwin/linux binaries for arm64
and amd64 and updates the cask in `billgrant/homebrew-tap`. That needs a
`HOMEBREW_TAP_GITHUB_TOKEN` repository secret with contents:write on the tap.
