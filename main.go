package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ray-x/finsight/internal/config"
	"github.com/ray-x/finsight/internal/logger"
	"github.com/ray-x/finsight/internal/ui"
)

var version = "0.1.0"

func main() {
	os.Exit(run())
}

func run() int {
	configPath := flag.String("config", "", "path to config file")
	showVersion := flag.Bool("version", false, "show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("finsight v%s\n", version)
		return 0
	}

	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}

	// Try local config first
	if _, err := os.Stat("config.yaml"); err == nil && *configPath == "" {
		cfgPath = "config.yaml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		return 1
	}

	if cfg.LogFile != "" {
		if err := logger.Init(cfg.LogFile); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
		defer logger.Close()
	}

	model := ui.NewModel(cfg, cfgPath)
	// Close runs BEFORE logger.Close() (LIFO) and, crucially, also
	// runs when tea.Run returns an error so the SQLite WAL is always
	// checkpointed into the main DB file.
	defer func() {
		if err := model.Close(); err != nil {
			logger.Log("error closing model: %v", err)
		}
	}()

	p := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}
