// Command warehouse runs the inventory and warehouse management service.
//
// Usage:
//
//	warehouse          start the HTTP server (applies migrations first unless AUTO_MIGRATE=false)
//	warehouse serve    same as above
//	warehouse migrate  apply pending migrations and exit
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vicitritellacarmida/warehouse-go/internal/config"
	"github.com/vicitritellacarmida/warehouse-go/internal/httpapi"
	"github.com/vicitritellacarmida/warehouse-go/internal/migrate"
	"github.com/vicitritellacarmida/warehouse-go/internal/service"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
	"github.com/vicitritellacarmida/warehouse-go/migrations"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	if cmd != "serve" && cmd != "migrate" {
		return fmt.Errorf("unknown command %q (want serve or migrate)", cmd)
	}

	cfg, err := config.Load(".env")
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.DatabaseDSN)
	if err != nil {
		return err
	}
	defer db.Close()

	if cmd == "migrate" || cfg.AutoMigrate {
		applied, err := migrate.Up(ctx, db, migrations.FS, logger)
		if err != nil {
			return err
		}
		logger.Info("migrations up to date", "applied", len(applied))
		if cmd == "migrate" {
			return nil
		}
	}

	st := store.New(db)
	svc := service.New(st, service.Options{DefaultToleranceBP: cfg.ReceiptToleranceBP})
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(svc, st, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", cfg.HTTPAddr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}
	return nil
}
