package notification

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// InsertOutbox writes one outbox record to admin_svc.outbox.
func InsertOutbox(ctx context.Context, pool *pgxpool.Pool,
	eventID, recipientEmail, recipientName, recipientUserID,
	correlationID, senderEmail, senderName,
	renderedSubject, renderedBody string,
) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO admin_svc.outbox
		 (event_id, recipient_email, recipient_name, recipient_user_id,
		  correlation_id, sender_email, sender_name,
		  rendered_subject, rendered_body, processing_status)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'PENDING')`,
		eventID,
		recipientEmail,
		nullStr(recipientName),
		nullStr(recipientUserID),
		nullStr(correlationID),
		nullStr(senderEmail),
		nullStr(senderName),
		renderedSubject,
		renderedBody,
	)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
