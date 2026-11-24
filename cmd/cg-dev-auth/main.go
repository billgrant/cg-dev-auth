// Command cg-dev-auth points a developer machine's package tools at
// Chainguard and keeps their credentials fresh.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"

	"github.com/billgrant/cg-dev-auth/internal/app"
	"github.com/billgrant/cg-dev-auth/internal/config"
)

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := newRoot().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "cg-dev-auth",
		Short: "Point npm, yarn, pip, uv and Docker at Chainguard, and keep their credentials fresh",
		Long: `cg-dev-auth configures this machine's package tools to pull from Chainguard
(Libraries at libraries.cgr.dev, images at cgr.dev/ORG) using your chainctl
login, and keeps short-lived tokens fresh so the tools just work.

Get started:
  cg-dev-auth init --org YOUR_ORG
  cg-dev-auth install
  cg-dev-auth status`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newInit(), newInstall(), newRefresh(), newStatus(), newLogin(),
		newOn(), newOff(), newUninstall(), newAgent())
	return root
}

func env(cmd *cobra.Command, load bool) (*app.Env, error) {
	e, err := app.NewEnv(cmd.OutOrStdout())
	if err != nil {
		return nil, err
	}
	if load {
		if err := e.LoadConfig(); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func newInit() *cobra.Command {
	var (
		org, pythonIndex, repo string
		ecosystems             []string
		force                  bool
	)
	cmd := &cobra.Command{
		Use:   "init --org ORG",
		Short: "Write the cg-dev-auth config for an organization",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := env(cmd, false)
			if err != nil {
				return err
			}
			if _, err := os.Stat(e.ConfigPath()); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", e.ConfigPath())
			}
			c := config.Default(org)
			if cmd.Flags().Changed("ecosystems") {
				c.Ecosystems = ecosystems
			}
			c.PythonIndex = pythonIndex
			c.LibrariesRepo = repo
			if err := config.Save(e.ConfigPath(), c); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ wrote %s (org %s)\n", e.ConfigPath(), org)
			if err := e.Chainctl.Status(cmd.Context()); err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "! %v\n", err)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "✓ chainctl is logged in")
			}
			fmt.Fprintln(cmd.OutOrStdout(), "next: cg-dev-auth install")
			return nil
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "Chainguard organization name, e.g. example.com (required)")
	cmd.Flags().StringSliceVar(&ecosystems, "ecosystems", nil, "ecosystems to manage: "+strings.Join(config.KnownEcosystems, ","))
	cmd.Flags().StringVar(&pythonIndex, "python-index", "python", "python or python-remediated")
	cmd.Flags().StringVar(&repo, "repo", "", "named Libraries repository in the org (default: the org's default repository)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config")
	_ = cmd.MarkFlagRequired("org")
	return cmd
}

func newInstall() *cobra.Command {
	var opts app.InstallOptions
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Configure every enabled ecosystem to use Chainguard",
		Long: `Configure every enabled ecosystem to use Chainguard:

  npm, Yarn Classic  ~/.npmrc        registry + short-lived token (managed block)
  Yarn Berry         ~/.yarnrc.yml   npmRegistryServer + token
  pip                pip.conf        index-url + keyring-provider=subprocess
  uv                 uv.toml         [[index]] + keyring-provider=subprocess
  Docker             config.json     chainctl credential helper for cgr.dev

On macOS it also installs a LaunchAgent that refreshes the npm/yarn token in
the background. Every edited file is backed up first; run again at any time.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := env(cmd, true)
			if err != nil {
				return err
			}
			return app.Install(cmd.Context(), e, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.NoAgent, "no-agent", false, "do not install the background refresh agent")
	return cmd
}

func newOff() *cobra.Command {
	return &cobra.Command{
		Use:   "off [npm|yarn|python|pip|uv|docker]...",
		Short: "Switch tools back to the registry settings you had before (all when none named)",
		Long: `Switch tools back to the registry settings in place before cg-dev-auth, to
compare upstream with Chainguard. Refresh skips them until "cg-dev-auth on".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env(cmd, true)
			if err != nil {
				return err
			}
			return app.Off(cmd.Context(), e, args)
		},
	}
}

func newOn() *cobra.Command {
	return &cobra.Command{
		Use:   "on [npm|yarn|python|pip|uv|docker]...",
		Short: "Switch tools back to Chainguard after `off` (all when none named)",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env(cmd, true)
			if err != nil {
				return err
			}
			return app.On(cmd.Context(), e, args)
		},
	}
}

func newUninstall() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Undo everything cg-dev-auth changed (backups and config are kept)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := env(cmd, true)
			if err != nil {
				return err
			}
			return app.Uninstall(cmd.Context(), e)
		},
	}
}

func newAgent() *cobra.Command {
	return &cobra.Command{
		Use:    "agent",
		Short:  "Run one background refresh (used by the LaunchAgent)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := env(cmd, true)
			if err != nil {
				return err
			}
			return app.Agent(cmd.Context(), e)
		},
	}
}

func newRefresh() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Write a fresh Libraries token into the npm and yarn config (never opens a browser)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := env(cmd, true)
			if err != nil {
				return err
			}
			return app.Refresh(cmd.Context(), e)
		},
	}
}

func newStatus() *cobra.Command {
	var offline bool
	cmd := &cobra.Command{
		Use:     "status",
		Aliases: []string{"doctor"},
		Short:   "Show how each tool is configured and whether its credentials work",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := env(cmd, true)
			if err != nil {
				return err
			}
			return app.Status(cmd.Context(), e, !offline)
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "skip the live registry checks")
	return cmd
}

func newLogin() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Sign in with chainctl (opens a browser) and refresh tokens",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := exec.CommandContext(cmd.Context(), "chainctl", "auth", "login")
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := c.Run(); err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					return fmt.Errorf("chainctl auth login exited with status %d", ee.ExitCode())
				}
				return err
			}
			e, err := env(cmd, true)
			if err != nil {
				return err
			}
			return app.Refresh(cmd.Context(), e)
		},
	}
}
