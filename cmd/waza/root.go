package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/microsoft/waza/cmd/waza/dev"
	"github.com/microsoft/waza/cmd/waza/tokens"
	versionpkg "github.com/microsoft/waza/internal/version"
	"github.com/spf13/cobra"
)

var version = "dev"

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wazatator",
		Short: "Wazatator - evaluate Agent Skills with Claude Code",
		Long: `Wazatator is a command-line tool for evaluating Agent Skills with Claude Code
(and the GitHub Copilot SDK).

It provides tools to run benchmarks, validate agent behavior, and measure
performance against predefined test cases.

Based on Microsoft Waza (MIT). Not affiliated with or endorsed by Microsoft.`,
		Version:      version,
		SilenceUsage: true,
	}

	logLevel := &slog.LevelVar{}
	logLevel.Set(slog.LevelInfo)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	debugLogging := cmd.PersistentFlags().Bool("debug", false, "Enable debug logging")
	noUpdateCheck := cmd.PersistentFlags().Bool("no-update-check", false, "Disable automatic update check")

	var checker *versionpkg.Checker
	cmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if *debugLogging {
			logLevel.Set(slog.LevelDebug)
		}
		if shouldRunUpdateCheck(cmd, *noUpdateCheck) {
			checker = versionpkg.NewChecker(version)
			checker.Run(context.Background())
		}
	}
	cmd.PersistentPostRun = func(cmd *cobra.Command, args []string) {
		if checker != nil {
			versionpkg.PrintNotice(checker.Result(), "")
		}
	}

	// Add subcommands
	cmd.AddCommand(newRunCommand())
	cmd.AddCommand(newInitCommand())
	cmd.AddCommand(newGetCommand())
	cmd.AddCommand(tokens.NewCommand())
	cmd.AddCommand(newCompareCommand())
	cmd.AddCommand(newGateCommand())
	cmd.AddCommand(newCoverageCommand())
	cmd.AddCommand(dev.NewCommand())
	cmd.AddCommand(newGradeCommand())
	cmd.AddCommand(newMetadataCommand(cmd))
	cmd.AddCommand(newCheckCommand())
	cmd.AddCommand(newSuggestCommand())
	cmd.AddCommand(newCacheCommand())
	cmd.AddCommand(newNewCommand())
	cmd.AddCommand(newSessionCommand())
	cmd.AddCommand(newServeCommand())
	cmd.AddCommand(newResultsCommand())
	cmd.AddCommand(newModelsCommand())
	cmd.AddCommand(newQualityCommand())
	cmd.AddCommand(newUpdateCommand())
	cmd.AddCommand(newSpecCommand())
	cmd.AddCommand(newMigrateCommand())
	cmd.AddCommand(newMCPMockCommand())
	cmd.AddCommand(newReplayCommand())
	cmd.AddCommand(newAdversarialCommand())
	cmd.AddCommand(newRegistryCommand())

	return cmd
}

func execute() error {
	rootCmd := newRootCommand()
	return rootCmd.Execute()
}

func shouldRunUpdateCheck(cmd *cobra.Command, noUpdateCheck bool) bool {
	if noUpdateCheck || os.Getenv("WAZA_NO_UPDATE_CHECK") != "" {
		return false
	}
	for c := cmd; c != nil; c = c.Parent() {
		if c.Name() == "update" {
			return false
		}
	}
	return true
}
