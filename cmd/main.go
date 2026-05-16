package main

import (
	"fmt"
	"os"

	"github.com/VaalaCat/ai-gateway/internal/agent"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/version"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var rootCmd = &cobra.Command{
	Use:   "ai-gateway",
	Short: "Distributed AI Gateway",
	Long: `AI Gateway is a distributed AI API gateway that supports 50+ providers.

It runs in two modes:
  master  - Control plane: manages users, tokens, channels, and coordinates agents
  agent   - Data plane: connects to master, syncs config, and relays API requests

Quick start:
  ai-gateway master                          Start master with default config
  ai-gateway agent --master http://host:8140 --enrollment-token <token>  Start agent`,
}

var masterCmd = &cobra.Command{
	Use:   "master",
	Short: "Start the master control node",
	Long: `Start the master control plane node.

The master provides:
  - Management API and Web UI for users, tokens, channels, models, and agents
  - WebSocket hub for real-time agent synchronization
  - Billing settlement and quota enforcement

Examples:
  ai-gateway master                           Use default config.yaml
  ai-gateway master --config /etc/gw.yaml     Use custom config
  ai-gateway master --listen :9000            Override listen address`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")
		cfg, err := config.LoadMaster(cfgPath)
		if err != nil {
			return err
		}

		if v, _ := cmd.Flags().GetString("listen"); v != "" {
			cfg.Master.Listen = v
		}
		if v, _ := cmd.Flags().GetString("log-level"); v != "" {
			cfg.LogLevel = v
		}

		var logger *zap.Logger
		if cfg.LogLevel == "debug" {
			logger, _ = zap.NewDevelopment()
		} else {
			logger, _ = zap.NewProduction()
		}
		zap.ReplaceGlobals(logger)
		defer logger.Sync()

		logger.Info("starting master", zap.String("listen", cfg.Master.Listen))
		srv, err := master.New(cfg, logger)
		if err != nil {
			logger.Fatal("failed to init master", zap.Error(err))
		}
		if err := srv.InitAdminUser(cfg.Master.AdminUser, cfg.Master.AdminPassword); err != nil {
			logger.Warn("init admin user failed", zap.Error(err))
		}
		return srv.Run()
	},
}

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Start an agent worker node",
	Long: `Start an agent (data plane) worker node.

The agent:
  - Connects to the master via WebSocket for real-time config sync
  - Caches tokens, channels, and model configs locally
  - Relays API requests (OpenAI-compatible) to upstream providers
  - Reports usage metrics back to master

First-time setup (with enrollment token from master UI):
  ai-gateway agent --master http://master-host:8140 --enrollment-token <token>

Subsequent starts (credentials saved to file):
  ai-gateway agent --master http://master-host:8140

With config file:
  ai-gateway agent --config agent.yaml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")
		cfg, err := config.LoadAgent(cfgPath)
		if err != nil {
			return err
		}

		if v, _ := cmd.Flags().GetString("listen"); v != "" {
			cfg.Agent.Listen = v
		}
		if v, _ := cmd.Flags().GetString("log-level"); v != "" {
			cfg.LogLevel = v
		}
		if v, _ := cmd.Flags().GetString("master"); v != "" {
			cfg.Agent.MasterURL = v
		}
		if v, _ := cmd.Flags().GetString("enrollment-token"); v != "" {
			cfg.Agent.EnrollmentToken = v
		}

		var logger *zap.Logger
		if cfg.LogLevel == "debug" {
			logger, _ = zap.NewDevelopment()
		} else {
			logger, _ = zap.NewProduction()
		}
		zap.ReplaceGlobals(logger)
		defer logger.Sync()

		logger.Info("starting agent", zap.String("listen", cfg.Agent.Listen))
		srv, err := agent.New(cfg, logger)
		if err != nil {
			logger.Fatal("failed to init agent", zap.Error(err))
		}
		return srv.Run()
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  "Display the version, git commit, build date, and Go version of ai-gateway.",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(version.Print())
	},
}

func init() {
	// Register master command flags and subcommand
	masterCmd.Flags().String("config", "config.yaml", "Config file path")
	masterCmd.Flags().String("listen", "", "Listen address (overrides config)")
	masterCmd.Flags().String("log-level", "", "Log level: debug, info, warn, error")
	rootCmd.AddCommand(masterCmd)

	// Register agent command flags and subcommand
	agentCmd.Flags().String("config", "config.yaml", "Config file path")
	agentCmd.Flags().String("listen", "", "Listen address, default :8139 (overrides config)")
	agentCmd.Flags().String("log-level", "", "Log level: debug, info, warn, error")
	agentCmd.Flags().String("master", "", "Master URL (e.g. http://localhost:8140)")
	agentCmd.Flags().String("enrollment-token", "", "Enrollment token for registration (reusable until expiry)")
	rootCmd.AddCommand(agentCmd)

	// Register version subcommand
	rootCmd.AddCommand(versionCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
