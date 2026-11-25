# Roadmap

Status as of 2026-11-24.

## Done

- npm, Yarn Classic, Yarn Berry, pip, uv and Docker (`cgr.dev/ORG`) configured
  by `cg-dev-auth install`, each confirmed on macOS by following
  [TESTING.md](TESTING.md).
- Background refresh (macOS LaunchAgent, every 5 minutes): installed and running.
- `off` / `on` to compare upstream with Chainguard; `uninstall` restores every file.
- CI: GitHub Actions on Ubuntu and macOS.

## Next

1. **Long-running refresh check.** After the Mac has been idle or asleep for
   more than an hour, `npm install` in a fresh directory should still work. If
   it gives E401, check `~/Library/Logs/cg-dev-auth.log`: chainctl may hand
   out a nearly expired token, and the refresh timing would need changing
   (`refresh_interval`, or renewing ahead of expiry).
2. **Homebrew tap.** Create `billgrant/homebrew-tap`, then add a
   `HOMEBREW_TAP_GITHUB_TOKEN` repository secret holding a token with
   contents:write on the tap. Push a `v0.1.0` tag; `.goreleaser.yaml` and
   `.github/workflows/release.yaml` are already set up.
   Then: `brew install billgrant/tap/cg-dev-auth`.
3. **Chainguard checks.** Add a `.chainguard/ci` config so `run-ci` and the
   Chainguard checks run alongside GitHub Actions.
4. **Cleanup.** Bump `actions/checkout` and `actions/setup-go` off the
   deprecated Node 20.
5. **Tell "offline" apart from "login expired".** Today any failure of
   `chainctl auth token` counts as an expired session, so being offline
   also triggers the "run `cg-dev-auth login`" notification. Notify only
   when the login really needs renewing; log network errors quietly.
6. **Warn before the chainctl login expires.** chainctl renews the
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
