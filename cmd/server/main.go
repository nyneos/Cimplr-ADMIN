package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"CimplrCorpSaas/admin/api"
	"CimplrCorpSaas/admin/internal/config"
	"CimplrCorpSaas/admin/internal/db"
	applogger "CimplrCorpSaas/admin/internal/logger"
	"CimplrCorpSaas/admin/internal/workers"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func main() {
	// ── Logger ────────────────────────────────────────────────────────────────
	logSvc := applogger.NewLoggerService(map[string]interface{}{
		"folder_path":    "./logs",
		"max_file_mb":    50,
		"retention_days": 30,
	})
	if err := logSvc.Start(); err != nil {
		log.Fatalf("failed to start logger: %v", err)
	}
	defer logSvc.Stop()
	applogger.SetGlobalLogger(logSvc)

	// ── Config ────────────────────────────────────────────────────────────────
	_ = godotenv.Load() // silently ignored if .env is missing in production
	cfg := config.Load()

	// ── Root context with graceful shutdown ───────────────────────────────────
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── Database ──────────────────────────────────────────────────────────────
	// Retry with exponential backoff so a transient network hiccup at startup
	// doesn't hard-crash the app and cause a restart loop on Render.
	var pool *pgxpool.Pool
	{
		var err error
		backoff := 5 * time.Second
		for attempt := 1; attempt <= 6; attempt++ {
			pool, err = db.NewPool(rootCtx, cfg.DSN())
			if err == nil {
				break
			}
			log.Printf("failed to connect to database (attempt %d/6): %v", attempt, err)
			if attempt == 6 {
				log.Fatalf("giving up after 6 attempts: %v", err)
			}
			select {
			case <-rootCtx.Done():
				log.Fatalf("context cancelled before DB connected: %v", rootCtx.Err())
			case <-time.After(backoff):
			}
			backoff *= 2 // 5s → 10s → 20s → 40s → 80s
		}
	}
	defer pool.Close()
	log.Println("database connected")

	// ── Migrations ────────────────────────────────────────────────────────────
	if err := db.Migrate(rootCtx, pool); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
	log.Println("migrations applied")

	// ── Background workers ────────────────────────────────────────────────────
	if cfg.OutboxWorkerEnabled {
		go workers.StartOutboxWorker(rootCtx, pool,
			cfg.OutboxPollSecs, cfg.OutboxBatchSize, cfg.OutboxTimeoutSecs)
	}
	if cfg.LicenceCheckerEnabled {
		go workers.StartLicenceChecker(rootCtx, pool, cfg.LicenceCheckerPollHours)
	}

	// ── HTTP server ───────────────────────────────────────────────────────────
	mux := http.NewServeMux()
	deps := api.NewDependencies(pool)
	api.RegisterRoutes(mux, deps)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Printf("CimplrAdmin listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// ── Graceful shutdown ────────────────────────────────────────────────────
	<-rootCtx.Done()
	log.Println("shutdown signal received, draining...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}
	log.Println("CimplrAdmin stopped")

	// Print master key warning if not set
	if os.Getenv("MASTER_KEY") == "" {
		log.Println("WARNING: MASTER_KEY is not set — emergency master access is disabled")
	}
}
