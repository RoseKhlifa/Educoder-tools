package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"educoder-cli/internal/compat"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "reuse service:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := compat.LoadConfig()
	if err != nil {
		return err
	}
	app := compat.NewServer(cfg)
	server := &http.Server{Addr: cfg.Addr, Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				app.Sweep()
			case <-ctx.Done():
				shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = server.Shutdown(shutdown)
				cancel()
				return
			}
		}
	}()
	fmt.Printf("Educoder compiled build: http://%s\n", cfg.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
