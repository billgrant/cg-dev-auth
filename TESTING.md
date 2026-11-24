# Testing that each tool really uses Chainguard

Each recipe installs something in a fresh scratch directory, then uses
`chainctl libraries verify` to check that what arrived was built by
Chainguard. `verify` checks artifacts against Chainguard's attestations;
coverage near 100% means the packages came from Chainguard Libraries.

Run `cg-dev-auth status` first; everything it lists should be ✓.

If you belong to more than one organization, add `--parent YOUR_ORG` to the
`chainctl libraries verify` commands.

> Coverage below 100% isn't automatically a misconfiguration. With upstream
> fallback on, packages Chainguard hasn't built yet are served from upstream
> and won't verify. A misconfigured tool shows close to 0%.

## pip

```sh
mkdir -p /tmp/cg-pip && cd /tmp/cg-pip
pip3 download flask                     # flask and its dependencies, as wheels
chainctl libraries verify *.whl
```

`pip3 download` prints `Looking in indexes: https://****@libraries.cgr.dev/python/simple/`.
Add `--detailed` for one line per wheel.

## uv

```sh
mkdir -p /tmp/cg-uv && cd /tmp/cg-uv
uv venv                                 # creates .venv
uv pip install flask
chainctl libraries verify --detailed .venv/
```

To see the index uv used: `uv pip install -v flask 2>&1 | grep libraries.cgr.dev`.

## npm

```sh
mkdir -p /tmp/cg-npm && cd /tmp/cg-npm
npm init -y
npm install express
grep -m3 '"resolved"' package-lock.json      # https://libraries.cgr.dev/javascript/...
chainctl libraries verify package-lock.json
```

You can also verify what's on disk: `chainctl libraries verify node_modules`.

## Yarn

Yarn has two very different generations, and cg-dev-auth configures both:

| | Yarn Classic (1.x) | Yarn Berry (2, 3, 4) |
|---|---|---|
| `yarn --version` | `1.22.x` | `4.x` |
| Reads registry config from | `~/.npmrc` | `~/.yarnrc.yml` |
| Get it | `npm install -g yarn` or `brew install yarn` | inside a project: `yarn set version stable` (needs Yarn installed first) |

`yarn --version` inside a project tells you which one that project uses: a
project with `packageManager: "yarn@4..."` in `package.json`, or a
`.yarnrc.yml` with `yarnPath`, runs Berry even if the global `yarn` is 1.x.

### Yarn Classic

```sh
mkdir -p /tmp/cg-yarn1 && cd /tmp/cg-yarn1
yarn --version                          # should print 1.x
echo '{"name":"t","version":"1.0.0"}' > package.json
yarn add express
grep -m3 resolved yarn.lock              # https://libraries.cgr.dev/javascript/...
chainctl libraries verify yarn.lock
```

If `yarn add` fails with `Couldn't find package "..." on the "npm" registry`,
`~/.npmrc` is missing `always-auth=true`. Yarn Classic needs it to send the
token. cg-dev-auth adds it when it detects Yarn 1.x, so run
`cg-dev-auth install` again after installing Yarn 1.x (the background
refresh also adds it within a few minutes). `cg-dev-auth status` warns about
this.

### Yarn Berry

```sh
mkdir -p /tmp/cg-yarn4 && cd /tmp/cg-yarn4
echo '{"name":"t","version":"1.0.0"}' > package.json
yarn set version stable                 # switches this directory to Berry
yarn --version                          # should print 4.x
yarn config get npmRegistryServer       # https://libraries.cgr.dev/javascript
yarn add express
yarn npm info express --fields dist --json | grep tarball   # https://libraries.cgr.dev/javascript/...
```

`chainctl libraries verify yarn.lock` reports about 0% for Berry lockfiles,
with "without a usable digest". That's expected: Berry records checksums of
its own repacked zip files, not of the registry tarballs, so they can't be
matched to attestations. The tarball URL above is the reliable check.

## Docker

```sh
docker pull cgr.dev/YOUR_ORG/IMAGE:TAG
```

To list the images your org can pull: `chainctl images repos list --parent YOUR_ORG`.
To find Chainguard equivalents of the images a project uses: `chainctl images discover`.

If the pull fails with `unauthorized`, check `cg-dev-auth status`; the
`helper` line runs the credential helper directly.

## Comparing upstream with Chainguard

`off` puts back the registries you had before cg-dev-auth; `on` switches
back. Use a fresh directory each time, so lockfiles and installed packages
from the other source don't carry over.

```sh
cg-dev-auth off npm
mkdir -p /tmp/up-npm && cd /tmp/up-npm && npm init -y >/dev/null && npm install express
chainctl libraries verify package-lock.json       # low: these came from npmjs
cg-dev-auth on npm

cg-dev-auth off python
mkdir -p /tmp/up-pip && cd /tmp/up-pip && pip3 download flask
chainctl libraries verify *.whl                   # low: these came from PyPI
cg-dev-auth on python
```

pip and uv keep a local cache keyed by URL, so the "off" run downloads
from PyPI even after earlier Chainguard runs. npm's cache is keyed by
content hash, and Chainguard builds have different hashes from npmjs, so
the two don't mix either.

## Background refresh (macOS)

npm and yarn store a token that lasts about an hour. The LaunchAgent replaces
it every 5 minutes.

```sh
launchctl print gui/$(id -u)/dev.cg-dev-auth.refresh | head -20   # loaded?
tail -f ~/Library/Logs/cg-dev-auth.log                             # one line per run
```

The real test: leave the machine for over an hour (including sleep), then:

```sh
cd /tmp/cg-npm && rm -rf node_modules && npm install    # should work without a refresh
```

To see what happens when the chainctl session itself expires, run
`chainctl auth logout`. Within 5 minutes you should get a macOS notification
asking you to run `cg-dev-auth login`, and npm will fail with E401 until you
do.
