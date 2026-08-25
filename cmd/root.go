package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/smeltery/beam/internal/config"
	"github.com/spf13/cobra"
)

func newRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "beam",
		Short:         "Webhook notifications and interactive approvals",
		Long:          "Beam turns webhook requests into notification events and provides a CLI for scripted notifications, prompts, and live activity state.",
		SilenceErrors: true,
		SilenceUsage:  true,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
		Version: version,
	}

	root.SetVersionTemplate(fmt.Sprintf("beam version %s\n", version))

	// Subcommands
	root.AddCommand(newConfigCmd(), newAuthCmd(), newServeCmd(), newProviderWorkerCmd(), newNotifyCmd(), newAskCmd(), newInteractionCmd(), newActivityCmd(), newServicesCmd())

	return root
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
	}

	var force bool

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Create default config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, err := config.ConfigPath()
			if err != nil {
				return err
			}

			if !force {
				if _, err := os.Stat(cfgPath); err == nil {
					return fmt.Errorf("config already exists at %s (use --force to overwrite)", cfgPath)
				}
			}

			if err := config.Save(config.DefaultConfig()); err != nil {
				return err
			}

			// Shorten the path for display.
			display := cfgPath
			if home, err := os.UserHomeDir(); err == nil {
				if rel, err := filepath.Rel(home, cfgPath); err == nil {
					display = "~/" + rel
				}
			}

			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
				"ok":      true,
				"path":    display,
				"created": true,
			})
		},
	}
	initCmd.Flags().BoolVar(&force, "force", false, "overwrite existing config")

	cmd.AddCommand(initCmd)
	return cmd
}

// Execute runs the root command.
func Execute(version string) error {
	return newRootCmd(version).Execute()
}

// Run executes the CLI and writes diagnostics to stderr.
func Run(version string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root := newRootCmd(version)
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintf(root.ErrOrStderr(), "beam: %v\n", err)
		if isCobraUsageError(err) {
			return 2
		}
		return ExitCode(err)
	}
	return 0
}

func isCobraUsageError(err error) bool {
	msg := err.Error()
	for _, prefix := range []string{
		"unknown command ",
		"unknown flag: ",
		"requires at least ",
		"requires at most ",
		"accepts ",
	} {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}
	return false
}
