// Command chartroomd is the Chartroom game server.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/server"
	"github.com/njdaniel/chartroom/internal/store"
	"github.com/njdaniel/chartroom/internal/table"
	"github.com/njdaniel/chartroom/internal/webclient"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	dataDir := flag.String("data", "data", "directory for the database and uploaded images")
	flag.Parse()

	if err := run(*addr, *dataDir); err != nil {
		slog.Error("chartroomd exited", "err", err)
		os.Exit(1)
	}
}

func run(addr, dataDir string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	assetsDir := filepath.Join(dataDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o700); err != nil {
		return err
	}
	db, err := store.Open(ctx, filepath.Join(dataDir, "chartroom.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	reg := eventlog.NewRegistry()
	table.Register(reg)
	client, ok := webclient.Files()
	if !ok {
		slog.Info("no browser client in this build; use `make build`, or `make dev` while developing")
		client = nil
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(server.Config{Log: eventlog.New(db, reg), AssetsDir: assetsDir, Client: client}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		slog.Info("chartroomd listening", "addr", addr, "data", dataDir)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	slog.Info("chartroomd shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
