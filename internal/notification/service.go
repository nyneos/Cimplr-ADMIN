package notification

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EnqueueRequest holds everything needed to create an outbox entry.
type EnqueueRequest struct {
	EventID         string
	RecipientEmail  string
	RecipientName   string
	RecipientUserID string
	CorrelationID   string
	DeploymentID    string // optional context
	TemplateData    map[string]string
	SenderEmail     string
	SenderName      string
}

// Enqueue renders the template and inserts an outbox record.
func Enqueue(ctx context.Context, pool *pgxpool.Pool, req EnqueueRequest) error {
	// Merge RecipientName into template data so templates can use {{.Name}}
	data := make(map[string]string)
	for k, v := range req.TemplateData {
		data[k] = v
	}
	if _, ok := data["Name"]; !ok {
		data["Name"] = req.RecipientName
	}

	subject, body, err := RenderTemplate(req.EventID, data)
	if err != nil {
		// Fallback: use raw event ID as subject if template missing
		subject = req.EventID
		body = fmt.Sprintf("Event: %s", req.EventID)
	}

	return InsertOutbox(ctx, pool,
		req.EventID,
		req.RecipientEmail,
		req.RecipientName,
		req.RecipientUserID,
		req.CorrelationID,
		req.SenderEmail,
		req.SenderName,
		subject,
		body,
	)
}
