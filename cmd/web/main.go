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

	buildinfo "educoder-cli"
	"educoder-cli/internal/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "web server:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := web.LoadConfig()
	if err != nil {
		return err
	}
	app := web.NewServer(cfg)
	jobRuntime, err := web.NewJobRuntime(cfg)
	if err != nil {
		return err
	}
	app.AttachJobs(jobRuntime)
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := jobRuntime.Close(shutdown); err != nil {
			fmt.Fprintln(os.Stderr, "job shutdown:", err)
		}
	}()
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				app.Store().Sweep()
			case <-ctx.Done():
				return
			}
		}
	}()
	shutdownDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		close(shutdownDone)
	}()
	fmt.Printf("educoder-workbench version=%s starting on %s\n", buildinfo.Current(), cfg.HTTPAddr)
	err = server.ListenAndServe()
	stop()
	<-shutdownDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
