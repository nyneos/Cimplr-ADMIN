-- CimplrAdmin full database schema
-- Schema: admin_svc
-- Run this once against your PostgreSQL instance.

CREATE SCHEMA IF NOT EXISTS admin_svc;

-- ── 3A. USERS ────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS admin_svc.users (
    user_id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username         TEXT NOT NULL UNIQUE,
    email            TEXT NOT NULL UNIQUE,
    password_hash    TEXT NOT NULL,
    full_name        TEXT,
    phone            TEXT,
    role             TEXT NOT NULL DEFAULT 'MAKER',
    status           TEXT NOT NULL DEFAULT 'PENDING',
    created_by       UUID REFERENCES admin_svc.users(user_id),
    approved_by      UUID REFERENCES admin_svc.users(user_id),
    approved_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── 3B. AUDIT LOG ────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS admin_svc.audit_log (
    audit_id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type      TEXT NOT NULL,
    entity_id        TEXT NOT NULL,
    action           TEXT NOT NULL,
    actor_user_id    UUID,
    actor_role       TEXT,
    old_value        JSONB,
    new_value        JSONB,
    ip_address       TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_log_entity ON admin_svc.audit_log(entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_actor  ON admin_svc.audit_log(actor_user_id);

-- ── 3C. DEPLOYMENTS ──────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS admin_svc.deployments (
    deployment_id    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_name     TEXT NOT NULL,
    company_email    TEXT NOT NULL,
    company_phone    TEXT,
    contact_person   TEXT,
    company_address  TEXT,
    db_user          TEXT NOT NULL,
    db_password      TEXT NOT NULL,
    db_host          TEXT NOT NULL,
    db_port          TEXT NOT NULL DEFAULT '5432',
    db_name          TEXT NOT NULL,
    db_url           TEXT,
    status           TEXT NOT NULL DEFAULT 'PENDING',
    is_active        BOOLEAN NOT NULL DEFAULT FALSE,
    created_by       UUID REFERENCES admin_svc.users(user_id),
    approved_by      UUID REFERENCES admin_svc.users(user_id),
    approved_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── 3D. ACCESS PACKAGES ──────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS admin_svc.access_packages (
    package_id    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    package_code  TEXT NOT NULL UNIQUE,
    display_name  TEXT NOT NULL,
    description   TEXT,
    is_active     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS admin_svc.package_permissions (
    perm_id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    package_id   UUID NOT NULL REFERENCES admin_svc.access_packages(package_id),
    module       TEXT NOT NULL,
    sub_module   TEXT NOT NULL DEFAULT 'default',
    action       TEXT NOT NULL,
    is_allowed   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(package_id, module, sub_module, action)
);
CREATE INDEX IF NOT EXISTS idx_pkg_perms_lookup ON admin_svc.package_permissions(package_id, module, sub_module);
CREATE INDEX IF NOT EXISTS idx_pkg_perms_full   ON admin_svc.package_permissions(package_id, module, sub_module, action);

CREATE TABLE IF NOT EXISTS admin_svc.deployment_packages (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id UUID NOT NULL REFERENCES admin_svc.deployments(deployment_id),
    package_id    UUID NOT NULL REFERENCES admin_svc.access_packages(package_id),
    assigned_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    assigned_by   UUID REFERENCES admin_svc.users(user_id),
    UNIQUE(deployment_id, package_id)
);

-- ── 3E. LICENCES ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS admin_svc.licences (
    licence_id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id    UUID NOT NULL REFERENCES admin_svc.deployments(deployment_id),
    starts_at        TIMESTAMPTZ NOT NULL,
    expires_at       TIMESTAMPTZ NOT NULL,
    grace_days       INT NOT NULL DEFAULT 7,
    status           TEXT NOT NULL DEFAULT 'ACTIVE',
    notified_expiry  BOOLEAN NOT NULL DEFAULT FALSE,
    notified_grace   BOOLEAN NOT NULL DEFAULT FALSE,
    created_by       UUID REFERENCES admin_svc.users(user_id),
    renewed_by       UUID REFERENCES admin_svc.users(user_id),
    renewed_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_licences_deployment_status ON admin_svc.licences(deployment_id, status);
CREATE INDEX IF NOT EXISTS idx_licences_expires_active    ON admin_svc.licences(expires_at) WHERE status IN ('ACTIVE','GRACE');

-- ── 3F. NOTIFICATION OUTBOX + SEND HISTORY ───────────────────────────────────
CREATE TABLE IF NOT EXISTS admin_svc.outbox (
    outbox_id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel            TEXT NOT NULL DEFAULT 'EMAIL',
    event_id           TEXT NOT NULL,
    correlation_id     TEXT,
    audit_id           UUID,
    recipient_email    TEXT NOT NULL,
    recipient_name     TEXT,
    recipient_user_id  UUID,
    sender_email       TEXT,
    sender_name        TEXT,
    rendered_subject   TEXT NOT NULL,
    rendered_body      TEXT NOT NULL,
    processing_status  TEXT NOT NULL DEFAULT 'PENDING',
    retry_count        INT NOT NULL DEFAULT 0,
    priority_level     INT NOT NULL DEFAULT 5,
    scheduled_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at       TIMESTAMPTZ,
    sent_at            TIMESTAMPTZ,
    last_error         TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON admin_svc.outbox(processing_status, scheduled_at)
    WHERE processing_status = 'PENDING';

CREATE TABLE IF NOT EXISTS admin_svc.send_history (
    history_id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    outbox_id            UUID NOT NULL,
    correlation_id       TEXT,
    event_id             TEXT,
    audit_id             UUID,
    channel              TEXT,
    recipient_user_id    UUID,
    recipient_email      TEXT,
    sender_email         TEXT,
    sender_name          TEXT,
    rendered_subject     TEXT,
    rendered_body        TEXT,
    processing_status    TEXT,
    provider_response    TEXT,
    provider_message_id  TEXT,
    attempt_number       INT,
    attempted_at         TIMESTAMPTZ
);

-- ── 3G. NOTIFICATION CONFIG ───────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS admin_svc.notification_config (
    config_id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id           TEXT NOT NULL,
    channel            TEXT NOT NULL DEFAULT 'EMAIL',
    retry_max          INT NOT NULL DEFAULT 3,
    retry_backoff_secs INT NOT NULL DEFAULT 60,
    UNIQUE(event_id, channel)
);

INSERT INTO admin_svc.notification_config(event_id, channel, retry_max, retry_backoff_secs)
VALUES
  ('LICENCE_EXPIRY_WARNING', 'EMAIL', 3,  60),
  ('LICENCE_GRACE_WARNING',  'EMAIL', 3,  60),
  ('LICENCE_EXPIRED',        'EMAIL', 5, 120),
  ('USER_APPROVED',          'EMAIL', 3,  60),
  ('USER_REJECTED',          'EMAIL', 3,  60),
  ('DEPLOYMENT_APPROVED',    'EMAIL', 3,  60)
ON CONFLICT (event_id, channel) DO NOTHING;

-- ── CONFIG SCHEMA (CimplrAdmin DB) ─────────────────────────────────────────
-- These tables live in the CimplrAdmin database only.
-- CimplrAdmin pushes a snapshot of these INTO the client deployment's own DB
-- via the sync worker. The client DB gets its own flat config.permissions and
-- config.settings tables with NO foreign-key references to admin_svc.
CREATE SCHEMA IF NOT EXISTS config;

-- Per-deployment runtime settings stored in CimplrAdmin (source of truth)
CREATE TABLE IF NOT EXISTS config.settings (
    setting_id    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id UUID REFERENCES admin_svc.deployments(deployment_id),
    key           TEXT NOT NULL,
    value         TEXT NOT NULL,
    description   TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(deployment_id, key)
);
CREATE INDEX IF NOT EXISTS idx_config_settings_deployment ON config.settings(deployment_id);

-- Global settings (deployment_id IS NULL = applies to all)
CREATE TABLE IF NOT EXISTS config.global_settings (
    setting_id  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key         TEXT NOT NULL UNIQUE,
    value       TEXT NOT NULL,
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── WHAT GETS PUSHED INTO THE CLIENT DB ──────────────────────────────────────
-- When CimplrAdmin syncs a deployment, it creates these two flat tables
-- in the CLIENT'S own Postgres database (no admin_svc references):
--
--   config.permissions
--     module      TEXT
--     sub_module  TEXT
--     action      TEXT
--     is_allowed  BOOLEAN
--     synced_at   TIMESTAMPTZ
--     UNIQUE(module, sub_module, action)
--
--   config.settings   (flat key/value — no deployment_id, no FK)
--     key        TEXT PRIMARY KEY
--     value      TEXT
--     synced_at  TIMESTAMPTZ
--
--   Keys written by CimplrAdmin into client config.settings:
--     licence_status          → 'ACTIVE' | 'GRACE' | 'EXPIRED' | 'NONE'
--     licence_expires_at      → ISO-8601 timestamp
--     deployment_is_active    → 'true' | 'false'
