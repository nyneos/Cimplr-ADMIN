package workers

// audit.go — shared audit-log helper for all background workers.
//
// Every worker action that changes state (licence transitions, permission
// reverts, email dispatch outcomes, sync calls) is written to
// admin_svc.audit_log with:
//   entity_type  — the domain object touched (LICENCE, DEPLOYMENT, EMAIL, SYNC, etc.)
//   entity_id    — the PK of that object (UUID as text)
//   action       — what happened (GRACE_TRANSITION, EXPIRED, PERMISSION_TAMPERED, etc.)
//   actor_role   — always "SYSTEM" for worker actions
//   old_value    — state before the change (JSONB)
//   new_value    — state after the change  (JSONB)

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// workerAudit writes one row to admin_svc.audit_log.
// entityType: LICENCE | DEPLOYMENT | EMAIL | SYNC | INTEGRITY
// action:     a SCREAMING_SNAKE action verb
// entityID:   the UUID (as text) of the primary object
// old/new:    arbitrary maps — pass nil to omit
func workerAudit(pool *pgxpool.Pool, entityType, entityID, action string, old, new_ map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var oldJ, newJ []byte
	if old != nil {
		oldJ, _ = json.Marshal(old)
	}
	if new_ != nil {
		newJ, _ = json.Marshal(new_)
	}

	_, err := pool.Exec(ctx,
		`INSERT INTO admin_svc.audit_log
		 (entity_type, entity_id, action, actor_role, old_value, new_value)
		 VALUES($1,$2,$3,'SYSTEM',$4,$5)`,
		entityType, entityID, action,
		nullJSON(oldJ), nullJSON(newJ),
	)
	if err != nil {
		log.Printf("[worker_audit] failed to write audit (%s/%s/%s): %v", entityType, entityID, action, err)
	}
}

// nullJSON returns nil when b is empty so pgx stores NULL instead of "null".
func nullJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
