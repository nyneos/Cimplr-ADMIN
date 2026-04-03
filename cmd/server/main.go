package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
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

// atomicHandler serves requests before the DB is ready (health = "starting"),
// then atomically swaps in the real mux once DB + migrations are done.
type atomicHandler struct {
	v atomic.Pointer[http.ServeMux]
}

func (a *atomicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if mux := a.v.Load(); mux != nil {
		mux.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/cimplrADMIN/health" {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "starting", "db": "connecting", "time": time.Now().UTC(),
		})
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false, "error": "service_starting", "message": "server is starting, please retry",
	})
}

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
	_ = godotenv.Load()
	cfg := config.Load()

	// ── Root context with graceful shutdown ───────────────────────────────────
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── HTTP server starts IMMEDIATELY ────────────────────────────────────────
	// Render detects port 8080 right away. Real routes swap in once DB is ready.
	handler := &atomicHandler{}
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	go func() {
		log.Printf("CimplrAdmin listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// ── Database + migrations + workers (non-blocking) ────────────────────────
	go func() {
		var pool *pgxpool.Pool
		var err error
		backoff := 10 * time.Second
		for attempt := 1; attempt <= 10; attempt++ {
			// Per-attempt timeout independent of rootCtx — a slow Supabase
			// pooler handshake won't cancel the whole retry chain.
			attemptCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			pool, err = db.NewPool(attemptCtx, cfg.DSN())
			cancel()
			if err == nil {
				break
			}
			log.Printf("[db] connect attempt %d/10 failed: %v", attempt, err)
			select {
			case <-rootCtx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 60*time.Second {
				backoff *= 2
			}
		}
		if err != nil {
			log.Printf("[db] giving up after 10 attempts — running degraded: %v", err)
			return
		}
		log.Println("database connected")

		migCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		migErr := db.Migrate(migCtx, pool)
		cancel()
		if migErr != nil {
			log.Printf("[db] migration failed: %v", migErr)
			return
		}
		log.Println("migrations applied")

		// Swap in the real mux — all routes now live
		mux := http.NewServeMux()
		deps := api.NewDependencies(pool)
		api.RegisterRoutes(mux, deps)
		handler.v.Store(mux)

		// ── Background workers ────────────────────────────────────────────────
		if cfg.OutboxWorkerEnabled {
			go workers.StartOutboxWorker(rootCtx, pool,
				cfg.OutboxPollSecs, cfg.OutboxBatchSize, cfg.OutboxTimeoutSecs)
		}
		if cfg.LicenceCheckerEnabled {
			go workers.StartLicenceChecker(rootCtx, pool, cfg.LicenceCheckerPollHours)
		}
		go workers.StartIntegrityChecker(rootCtx, pool)
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	<-rootCtx.Done()
	log.Println("shutdown signal received, draining...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}
	log.Println("CimplrAdmin stopped")

	if os.Getenv("MASTER_KEY") == "" {
		log.Println("WARNING: MASTER_KEY is not set — emergency master access is disabled")
	}
}
