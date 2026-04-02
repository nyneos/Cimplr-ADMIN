package notification

import (
	"bytes"
	"fmt"
	"text/template"
)

// templates holds all email templates keyed by EventID.
var templates = map[string]struct{ subject, body string }{
	"LICENCE_EXPIRY_WARNING": {
		subject: "Action Required: Your Cimplr licence expires soon",
		body: `Dear {{.Name}},

Your Cimplr licence for {{.CompanyName}} is due to expire on {{.ExpiresAt}}.

Please renew your licence before the expiry date to avoid service interruption.

If you have already renewed, please disregard this message.

— Cimplr Admin`,
	},
	"LICENCE_GRACE_WARNING": {
		subject: "Warning: Your Cimplr licence has expired — grace period active",
		body: `Dear {{.Name}},

Your Cimplr licence for {{.CompanyName}} has expired.

You have {{.GraceDays}} days remaining in your grace period to renew your licence before access is suspended.

Renew now at: /cimplrADMIN/licence/renew

— Cimplr Admin`,
	},
	"LICENCE_EXPIRED": {
		subject: "URGENT: Your Cimplr licence has been suspended",
		body: `Dear {{.Name}},

Your Cimplr licence for {{.CompanyName}} has been suspended due to non-renewal.

All access to Cimplr services has been disabled.

To reactivate your account, please contact your administrator or renew at: /cimplrADMIN/licence/renew

— Cimplr Admin`,
	},
	"USER_APPROVED": {
		subject: "Your Cimplr admin account has been approved",
		body: `Dear {{.Name}},

Your Cimplr admin account has been approved. You may now log in.

Login at: /cimplrADMIN/auth/login

— Cimplr Admin`,
	},
	"USER_REJECTED": {
		subject: "Your Cimplr account request was not approved",
		body: `Dear {{.Name}},

Your Cimplr admin account request has been rejected.

Reason: {{.Reason}}

If you believe this is an error, please contact your administrator.

— Cimplr Admin`,
	},
	"DEPLOYMENT_APPROVED": {
		subject: "Your Cimplr deployment is now live",
		body: `Dear {{.Name}},

The deployment for {{.CompanyName}} has been approved and is now live.

— Cimplr Admin`,
	},
	"USER_CREATED": {
		subject: "New user pending approval",
		body: `Hi {{.Name}},

A new admin user has been created and is pending your approval.

Username: {{.Username}}
Role:     {{.Role}}

Please review and approve or reject at: /cimplrADMIN/user/approve

— Cimplr Admin`,
	},
}

// RenderTemplate renders subject and body for the given eventID with the provided data.
func RenderTemplate(eventID string, data map[string]string) (subject, body string, err error) {
	tmpl, ok := templates[eventID]
	if !ok {
		return "", "", fmt.Errorf("no template for event: %s", eventID)
	}

	subj, err := renderStr(tmpl.subject, data)
	if err != nil {
		return "", "", err
	}
	bd, err := renderStr(tmpl.body, data)
	if err != nil {
		return "", "", err
	}
	return subj, bd, nil
}

func renderStr(tmplStr string, data map[string]string) (string, error) {
	t, err := template.New("").Parse(tmplStr)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
