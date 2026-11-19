// Команда api запускає сервер застосунку «Догляд».
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"doglyad/backend/internal/api"
	"doglyad/backend/internal/config"
	"doglyad/backend/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("Сервер зупинено через помилку", "помилка", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg := config.Load()

	db, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close()

	server, err := api.New(db, cfg, log)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server.Background(ctx)

	httpServer := &http.Server{
		Addr:              cfg.Address,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// Потік подій SSE тримає з'єднання відкритим, тому запис не обмежуємо.
		IdleTimeout: 2 * time.Minute,
	}

	errs := make(chan error, 1)
	go func() {
		log.Info("Сервер «Догляд» запущено", "адреса", cfg.Address, "база", cfg.DatabasePath)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
			return
		}
		errs <- nil
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		log.Info("Зупиняємо сервер, чекаємо на завершення запитів")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}
