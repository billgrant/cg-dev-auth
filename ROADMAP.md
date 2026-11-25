# Roadmap

Status as of 2026-11-25.

## Done

- npm, Yarn Classic, Yarn Berry, pip, uv and Docker (`cgr.dev/ORG`) configured
  by `cg-dev-auth install`, each confirmed on macOS by following
  [TESTING.md](TESTING.md).
- Background refresh (macOS LaunchAgent, every 5 minutes): installed and
  running. Tokens stayed valid when the Mac was left idle.
- `off` / `on` to compare upstream with Chainguard; `uninstall` restores every file.
- CI: GitHub Actions on Ubuntu and macOS (Node 24 actions), plus a GoReleaser
  check and snapshot build on every push.
- v0.1.0 released; the cask is published to `billgrant/homebrew-tap`.
  Release: push a `v*` tag (needs the `HOMEBREW_TAP_GITHUB_TOKEN` secret,
  a fine-grained token with contents:write on the tap; it expires, and
  releases fail when it does).

## Next

1. **Test the Homebrew install on macOS.** `brew install billgrant/tap/cg-dev-auth`;
   check that the quarantine removal (`postflight_steps` in the cask) lets the
   binary run, then run `cg-dev-auth install` so the LaunchAgent uses the brew
   binary.
2. **Chainguard checks.** Add a `.chainguard/ci` config so `run-ci` and the
   Chainguard checks run alongside GitHub Actions.
3. **Tell "offline" apart from "login expired".** Today any failure of
   `chainctl auth token` counts as an expired session, so being offline
   also triggers the "run `cg-dev-auth login`" notification. Notify only
   when the login really needs renewing; log network errors quietly.
4. **Warn before the chainctl login expires.** chainctl renews the
   short-lived tokens from its refresh token. When that refresh token
   expires, everything fails until a browser sign-in. `chainctl auth status`
   only reports the short-lived token's expiry, so either:
   - record the time of each `cg-dev-auth login` and warn ahead, once the
     refresh-token lifetime is known (from the log: first failure minus last
     login) or configured; or
   - ask the chainctl team to expose the refresh token's expiry in
     `chainctl auth status`, and read it from there.

   Not worth doing: `chainctl auth login --headless`. The device flow still
   needs a person to approve a short-lived code, so it doesn't remove the
   browser step.

## Later ideas

- pnpm and bun (both read `.npmrc`).
- Linux background refresh (systemd user timer); today Linux users schedule
  `cg-dev-auth refresh` themselves.
- Named Libraries repositories end to end (`init --repo`, partly built).
- A `compare` command that runs the upstream/Chainguard comparisons from
  TESTING.md automatically.
- Revisit Yarn Classic's `always-auth` detection. It worked once `install`
  was re-run with Yarn 1 on PATH; writing it to `~/.yarnrc` instead (which
  npm doesn't read) might remove the need for detection. Untested.

## Notes

- `chainctl auth token --audience=libraries.cgr.dev` renews from chainctl's
  cached refresh token without a browser. It returns the cached token until
  chainctl decides to renew it, and this chainctl has no force-refresh flag.
  That is why the refresh interval is 5 minutes.
- Yarn Berry lockfiles show about 0% in `chainctl libraries verify`: Berry
  stores checksums of its own repacked zips. Check the tarball URL instead.
- need to look at clean uninstall. The config is left behind on uninstall this
  causes the install to fail until the config is deleted.
