package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StartOutboxWorker polls admin_svc.outbox and dispatches PENDING messages.
// It respects OUTBOX_WORKER_ENABLED, OUTBOX_WORKER_POLL_SECS, OUTBOX_WORKER_BATCH_SIZE,
// OUTBOX_WORKER_TIMEOUT_SECS environment variables.
func StartOutboxWorker(ctx context.Context, pool *pgxpool.Pool, pollSecs, batchSize, timeoutSecs int) {
	ticker := time.NewTicker(time.Duration(pollSecs) * time.Second)
	defer ticker.Stop()
	log.Printf("[outbox_worker] started (poll=%ds, batch=%d)", pollSecs, batchSize)

	for {
		select {
		case <-ctx.Done():
			log.Println("[outbox_worker] stopped")
			return
		case <-ticker.C:
			processOutboxBatch(ctx, pool, batchSize, timeoutSecs)
		}
	}
}

type outboxRow struct {
	OutboxID        string
	EventID         string
	RecipientEmail  string
	RecipientName   string
	SenderEmail     string
	SenderName      string
	RenderedSubject string
	RenderedBody    string
	RetryCount      int
}

func processOutboxBatch(ctx context.Context, pool *pgxpool.Pool, batchSize, timeoutSecs int) {
	// Use a fresh background context for all DB operations — independent of the
	// worker shutdown context. This prevents Render's graceful-shutdown SIGTERM
	// from aborting an in-flight transaction mid-write.
	dbCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tx, err := pool.Begin(dbCtx)
	if err != nil {
		log.Printf("[outbox_worker] begin tx: %v", err)
		return
	}
	defer tx.Rollback(dbCtx)

	rows, err := tx.Query(dbCtx,
		`SELECT outbox_id::text, event_id, recipient_email,
		        COALESCE(recipient_name,''), COALESCE(sender_email,''), COALESCE(sender_name,''),
		        rendered_subject, rendered_body, retry_count
		 FROM admin_svc.outbox
		 WHERE processing_status = 'PENDING'
		   AND scheduled_at <= now()
		 ORDER BY priority_level ASC, scheduled_at ASC
		 LIMIT $1
		 FOR UPDATE SKIP LOCKED`, batchSize)
	if err != nil {
		log.Printf("[outbox_worker] query: %v", err)
		return
	}

	var batch []outboxRow
	for rows.Next() {
		var o outboxRow
		if err := rows.Scan(&o.OutboxID, &o.EventID, &o.RecipientEmail, &o.RecipientName,
			&o.SenderEmail, &o.SenderName, &o.RenderedSubject, &o.RenderedBody, &o.RetryCount); err != nil {
			log.Printf("[outbox_worker] scan: %v", err)
			continue
		}
		batch = append(batch, o)
	}
	rows.Close()
	if len(batch) == 0 {
		tx.Rollback(dbCtx)
		return
	}

	// Mark PROCESSING
	ids := make([]string, len(batch))
	for i, o := range batch {
		ids[i] = o.OutboxID
	}
	_, err = tx.Exec(dbCtx,
		`UPDATE admin_svc.outbox SET processing_status='PROCESSING', processed_at=now()
		 WHERE outbox_id = ANY($1::uuid[])`, ids)
	if err != nil {
		log.Printf("[outbox_worker] mark processing: %v", err)
		return
	}
	if err := tx.Commit(dbCtx); err != nil {
		log.Printf("[outbox_worker] commit: %v", err)
		return
	}

	// Dispatch each message
	httpClient := &http.Client{Timeout: time.Duration(timeoutSecs) * time.Second}
	for _, o := range batch {
		dispatchMessage(ctx, pool, httpClient, o)
	}
}

type sendPayload struct {
	EventID        string `json:"event_id"`
	RecipientEmail string `json:"recipient_email"`
	RecipientName  string `json:"recipient_name"`
	SenderEmail    string `json:"sender_email"`
	SenderName     string `json:"sender_name"`
	Subject        string `json:"subject"`
	Body           string `json:"body"`
}

func dispatchMessage(ctx context.Context, pool *pgxpool.Pool, client *http.Client, o outboxRow) {
	endpointURL := os.Getenv("SEND_ENDPOINT_URL")
	apiKey := os.Getenv("SEND_ENDPOINT_API_KEY")

	payload := sendPayload{
		EventID:        o.EventID,
		RecipientEmail: o.RecipientEmail,
		RecipientName:  o.RecipientName,
		SenderEmail:    o.SenderEmail,
		SenderName:     o.SenderName,
		Subject:        o.RenderedSubject,
		Body:           o.RenderedBody,
	}
	// Endpoint expects an array — wrap single message in []
	body, _ := json.Marshal([]sendPayload{payload})

	var providerResp, providerMsgID, lastErr string
	var finalStatus string

	if endpointURL == "" {
		lastErr = "SEND_ENDPOINT_URL not configured"
		finalStatus = "DEAD"
	} else {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(body))
		if err != nil {
			lastErr = err.Error()
			finalStatus = "DEAD"
		} else {
			req.Header.Set("Content-Type", "application/json")
			if apiKey != "" {
				req.Header.Set("Authorization", "Bearer "+apiKey)
			}
			resp, err := client.Do(req)
			if err != nil {
				lastErr = err.Error()
				finalStatus = markRetryOrDead(ctx, pool, o)
				recordHistory(ctx, pool, o, finalStatus, lastErr, "", o.RetryCount+1)
				workerAudit(pool, "EMAIL", o.OutboxID, "EMAIL_DISPATCH_ERROR",
					map[string]any{"retry_count": o.RetryCount},
					map[string]any{"event_id": o.EventID, "recipient": o.RecipientEmail, "error": lastErr, "new_status": finalStatus})
				return
			}
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			providerResp = string(respBody)
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				finalStatus = "SENT"
			} else {
				lastErr = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, providerResp)
				finalStatus = markRetryOrDead(ctx, pool, o)
			}
		}
	}

	dbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if finalStatus == "SENT" {
		_, _ = pool.Exec(dbCtx,
			`UPDATE admin_svc.outbox SET processing_status='SENT', sent_at=now(), retry_count=$2
			 WHERE outbox_id=$1`, o.OutboxID, o.RetryCount+1)
		workerAudit(pool, "EMAIL", o.OutboxID, "EMAIL_SENT",
			map[string]any{"processing_status": "PROCESSING", "retry_count": o.RetryCount},
			map[string]any{"processing_status": "SENT", "event_id": o.EventID, "recipient": o.RecipientEmail, "attempt": o.RetryCount + 1})
	} else {
		_, _ = pool.Exec(dbCtx,
			`UPDATE admin_svc.outbox SET processing_status=$2, retry_count=$3, last_error=$4
			 WHERE outbox_id=$1`, o.OutboxID, finalStatus, o.RetryCount+1, lastErr)
		workerAudit(pool, "EMAIL", o.OutboxID, "EMAIL_"+finalStatus,
			map[string]any{"processing_status": "PROCESSING", "retry_count": o.RetryCount},
			map[string]any{"processing_status": finalStatus, "event_id": o.EventID, "recipient": o.RecipientEmail, "attempt": o.RetryCount + 1, "error": lastErr})
	}
	recordHistory(dbCtx, pool, o, finalStatus, lastErr, providerMsgID+providerResp, o.RetryCount+1)
}

func markRetryOrDead(ctx context.Context, pool *pgxpool.Pool, o outboxRow) string {
	dbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var retryMax int
	err := pool.QueryRow(dbCtx,
		`SELECT retry_max FROM admin_svc.notification_config WHERE event_id=$1 AND channel='EMAIL'`,
		o.EventID).Scan(&retryMax)
	if err != nil || o.RetryCount+1 >= retryMax {
		return "DEAD"
	}
	return "PENDING"
}

func recordHistory(ctx context.Context, pool *pgxpool.Pool, o outboxRow, status, lastErr, providerResp string, attempt int) {
	_, _ = pool.Exec(ctx,
		`INSERT INTO admin_svc.send_history
		 (outbox_id, event_id, recipient_email, sender_email, sender_name,
		  rendered_subject, rendered_body, processing_status, provider_response, attempt_number, attempted_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now())`,
		o.OutboxID, o.EventID, o.RecipientEmail, o.SenderEmail, o.SenderName,
		o.RenderedSubject, o.RenderedBody, status, providerResp, attempt)
}
