package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/bootstrap"
)

func main() {
	if err := run(); err != nil {
		log.Printf("control-api stopped with error: %v", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		return err
	}

	app, err := bootstrap.New(cfg)
	if err != nil {
		return err
	}

	return app.Run(ctx)
}
