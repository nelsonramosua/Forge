package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"forge/control-plane/internal/config"
	"forge/control-plane/internal/server"
	"forge/control-plane/internal/store"
	"forge/control-plane/internal/vault"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	if wantsVersion(os.Args) {
		fmt.Printf("forge-control-plane %s (commit %s, built %s)\n", version, commit, buildTime)
		return
	}

	cfg, err := config.FromEnv()
	if err != nil {
		log.Fatal(err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	vt, err := vault.New(cfg.MasterKey)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := server.New(cfg, st, vt).Run(ctx); err != nil {
		log.Fatal(err)
	}
}

func wantsVersion(args []string) bool {
	if len(args) != 2 {
		return false
	}
	return args[1] == "--version" || args[1] == "-version" || args[1] == "version"
}
