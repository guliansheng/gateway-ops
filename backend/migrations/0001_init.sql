-- GatewayOps schema 0001_init
-- 此文件镜像 AutoMigrate 产生的结构，仅供生产人工核对；正常启动由 GORM AutoMigrate 自动建表。

CREATE TABLE IF NOT EXISTS channels (
    id                BIGSERIAL PRIMARY KEY,
    name              VARCHAR(128) NOT NULL,
    type              VARCHAR(32)  NOT NULL,
    site_url          VARCHAR(512) NOT NULL,
    username          VARCHAR(256) NOT NULL,
    password_cipher   VARCHAR(4096) NOT NULL,
    credential_mode   VARCHAR(16)  NOT NULL DEFAULT 'password',
    balance_mode      VARCHAR(16)  NOT NULL DEFAULT 'auto',
    manual_balance    DOUBLE PRECISION NOT NULL DEFAULT 0,
	manual_usage_baseline DOUBLE PRECISION,
	manual_usage_basis VARCHAR(64) NOT NULL DEFAULT '',
    remark            VARCHAR(512),
    turnstile_enabled BOOLEAN DEFAULT false,
    captcha_config_id BIGINT,
    balance_threshold DOUBLE PRECISION DEFAULT 0,
    monitor_enabled   BOOLEAN DEFAULT true,
    last_balance      DOUBLE PRECISION,
    last_balance_at   TIMESTAMPTZ,
    last_error        TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ
);
DROP INDEX IF EXISTS idx_channels_name;
CREATE UNIQUE INDEX IF NOT EXISTS idx_channels_name ON channels (name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_channels_type ON channels (type);
CREATE INDEX IF NOT EXISTS idx_channels_deleted_at ON channels (deleted_at);
ALTER TABLE channels ADD COLUMN IF NOT EXISTS manual_balance DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN IF NOT EXISTS manual_usage_baseline DOUBLE PRECISION;
ALTER TABLE channels ADD COLUMN IF NOT EXISTS manual_usage_basis VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN IF NOT EXISTS remark VARCHAR(512);

CREATE TABLE IF NOT EXISTS auth_sessions (
    channel_id          BIGINT PRIMARY KEY,
    user_id             VARCHAR(64),
    access_token_cipher TEXT,
    cookie_cipher       TEXT,
    csrf_token_cipher   VARCHAR(1024),
    expires_at          TIMESTAMPTZ,
    last_login_at       TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS captcha_configs (
    id              BIGSERIAL PRIMARY KEY,
    name            VARCHAR(128) NOT NULL,
    type            VARCHAR(32)  NOT NULL,
    api_key_cipher  VARCHAR(1024),
    endpoint        VARCHAR(512),
    extra           TEXT,
    enabled         BOOLEAN DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_captcha_configs_name ON captcha_configs (name);
CREATE INDEX IF NOT EXISTS idx_captcha_configs_type ON captcha_configs (type);
CREATE INDEX IF NOT EXISTS idx_captcha_configs_deleted_at ON captcha_configs (deleted_at);

CREATE TABLE IF NOT EXISTS rate_snapshots (
    id                BIGSERIAL PRIMARY KEY,
    channel_id        BIGINT NOT NULL,
    model_name        VARCHAR(256) NOT NULL,
    description       VARCHAR(512),
    ratio             DOUBLE PRECISION NOT NULL,
    completion_ratio  DOUBLE PRECISION,
    source            VARCHAR(32) NOT NULL DEFAULT 'upstream',
    relay_station_id  BIGINT,
    relay_account_external_id BIGINT,
    first_seen_at     TIMESTAMPTZ NOT NULL,
    last_seen_at      TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_rate_chan_model ON rate_snapshots (channel_id, model_name);
CREATE INDEX IF NOT EXISTS idx_rate_snapshots_source ON rate_snapshots (source);
CREATE INDEX IF NOT EXISTS idx_rate_snapshots_relay_station_id ON rate_snapshots (relay_station_id);
CREATE INDEX IF NOT EXISTS idx_rate_snapshots_relay_account_external_id ON rate_snapshots (relay_account_external_id);
ALTER TABLE rate_snapshots ADD COLUMN IF NOT EXISTS source VARCHAR(32) NOT NULL DEFAULT 'upstream';
ALTER TABLE rate_snapshots ADD COLUMN IF NOT EXISTS relay_station_id BIGINT;
ALTER TABLE rate_snapshots ADD COLUMN IF NOT EXISTS relay_account_external_id BIGINT;

CREATE TABLE IF NOT EXISTS rate_change_logs (
    id                    BIGSERIAL PRIMARY KEY,
    channel_id            BIGINT NOT NULL,
    model_name            VARCHAR(256) NOT NULL,
    old_ratio             DOUBLE PRECISION,
    new_ratio             DOUBLE PRECISION NOT NULL,
    old_completion_ratio  DOUBLE PRECISION,
    new_completion_ratio  DOUBLE PRECISION,
    changed_at            TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_rate_change_channel ON rate_change_logs (channel_id);
CREATE INDEX IF NOT EXISTS idx_rate_change_model ON rate_change_logs (model_name);
CREATE INDEX IF NOT EXISTS idx_rate_change_at ON rate_change_logs (changed_at);

CREATE TABLE IF NOT EXISTS balance_snapshots (
    id         BIGSERIAL PRIMARY KEY,
    channel_id BIGINT NOT NULL,
    balance    DOUBLE PRECISION NOT NULL,
    sampled_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_balance_channel ON balance_snapshots (channel_id);
CREATE INDEX IF NOT EXISTS idx_balance_at ON balance_snapshots (sampled_at);

CREATE TABLE IF NOT EXISTS channel_daily_balances (
    id          BIGSERIAL PRIMARY KEY,
    channel_id  BIGINT NOT NULL,
    day         DATE NOT NULL,
    balance     DOUBLE PRECISION NOT NULL,
    captured_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_daily_balance ON channel_daily_balances (channel_id, day);
CREATE INDEX IF NOT EXISTS idx_channel_daily_balances_captured_at ON channel_daily_balances (captured_at);

CREATE TABLE IF NOT EXISTS balance_change_logs (
    id                  BIGSERIAL PRIMARY KEY,
    channel_id          BIGINT NOT NULL,
    balance_snapshot_id BIGINT NOT NULL,
    previous_balance    DOUBLE PRECISION NOT NULL,
    new_balance         DOUBLE PRECISION NOT NULL,
    delta               DOUBLE PRECISION NOT NULL,
    kind                VARCHAR(24) NOT NULL,
    detected_at         TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_balance_change_snapshot ON balance_change_logs (balance_snapshot_id);
CREATE INDEX IF NOT EXISTS idx_balance_change_channel ON balance_change_logs (channel_id);
CREATE INDEX IF NOT EXISTS idx_balance_change_kind ON balance_change_logs (kind);
CREATE INDEX IF NOT EXISTS idx_balance_change_detected ON balance_change_logs (detected_at);

CREATE TABLE IF NOT EXISTS notification_channels (
    id            BIGSERIAL PRIMARY KEY,
    name          VARCHAR(128) NOT NULL,
    type          VARCHAR(32)  NOT NULL,
    config_cipher TEXT NOT NULL,
    subscriptions TEXT NOT NULL DEFAULT '[]',
    enabled       BOOLEAN DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_notification_channels_name ON notification_channels (name);
CREATE INDEX IF NOT EXISTS idx_notification_channels_type ON notification_channels (type);
CREATE INDEX IF NOT EXISTS idx_notification_channels_deleted_at ON notification_channels (deleted_at);

CREATE TABLE IF NOT EXISTS notification_cooldowns (
    channel_id    BIGINT       NOT NULL,
    event         VARCHAR(64)  NOT NULL,
    last_sent_at  TIMESTAMPTZ  NOT NULL,
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, event)
);

CREATE TABLE IF NOT EXISTS notification_logs (
    id            BIGSERIAL PRIMARY KEY,
    channel_id    BIGINT NOT NULL,
    event         VARCHAR(64) NOT NULL,
    subject       VARCHAR(512) NOT NULL,
    body          TEXT,
    success       BOOLEAN NOT NULL,
    error_message TEXT,
    sent_at       TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notification_logs_channel ON notification_logs (channel_id);
CREATE INDEX IF NOT EXISTS idx_notification_logs_event ON notification_logs (event);
CREATE INDEX IF NOT EXISTS idx_notification_logs_at ON notification_logs (sent_at);

CREATE TABLE IF NOT EXISTS monitor_logs (
    id            BIGSERIAL PRIMARY KEY,
    channel_id    BIGINT NOT NULL,
    job           VARCHAR(32) NOT NULL,
    success       BOOLEAN NOT NULL,
    error_message TEXT,
    duration_ms   BIGINT,
    started_at    TIMESTAMPTZ NOT NULL,
    finished_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_monitor_logs_channel ON monitor_logs (channel_id);
CREATE INDEX IF NOT EXISTS idx_monitor_logs_job ON monitor_logs (job);
CREATE INDEX IF NOT EXISTS idx_monitor_logs_started ON monitor_logs (started_at);

CREATE TABLE IF NOT EXISTS app_settings (
    key        VARCHAR(128) PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS relay_stations (
    id                BIGSERIAL PRIMARY KEY,
    name              VARCHAR(128) NOT NULL,
    base_url          VARCHAR(512) NOT NULL,
    api_key_cipher    TEXT NOT NULL,
    auto_adjust_enabled BOOLEAN NOT NULL DEFAULT false,
    auto_adjust_no_profit_enabled BOOLEAN NOT NULL DEFAULT false,
    auto_priority_enabled BOOLEAN NOT NULL DEFAULT false,
    auto_priority_recall_enabled BOOLEAN NOT NULL DEFAULT false,
    auto_priority_recall_minutes INTEGER NOT NULL DEFAULT 180,
    last_synced_at    TIMESTAMPTZ,
    last_error        TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_relay_stations_name ON relay_stations (name);
CREATE INDEX IF NOT EXISTS idx_relay_stations_deleted_at ON relay_stations (deleted_at);
ALTER TABLE relay_stations ADD COLUMN IF NOT EXISTS auto_priority_recall_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE relay_stations ADD COLUMN IF NOT EXISTS auto_priority_recall_minutes INTEGER NOT NULL DEFAULT 180;

CREATE TABLE IF NOT EXISTS relay_groups (
    id                BIGSERIAL PRIMARY KEY,
    relay_station_id  BIGINT NOT NULL REFERENCES relay_stations(id) ON DELETE CASCADE,
    external_id       BIGINT NOT NULL,
    name              VARCHAR(256) NOT NULL,
    description       VARCHAR(512),
    platform          VARCHAR(64),
    status            VARCHAR(32),
    is_exclusive      BOOLEAN NOT NULL DEFAULT false,
    require_oauth_only BOOLEAN NOT NULL DEFAULT false,
    sort_order        INTEGER NOT NULL DEFAULT 0,
    model_types_json TEXT,
    monitor_enabled   BOOLEAN NOT NULL DEFAULT false,
    rate_multiplier   DOUBLE PRECISION NOT NULL,
    allow_image_generation BOOLEAN NOT NULL DEFAULT false,
    image_rate_independent BOOLEAN NOT NULL DEFAULT false,
    image_rate_multiplier DOUBLE PRECISION NOT NULL DEFAULT 1,
    image_price_1k NUMERIC(20,8),
    image_price_2k NUMERIC(20,8),
    image_price_4k NUMERIC(20,8),
    synced_at         TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_relay_group_external ON relay_groups (relay_station_id, external_id);
ALTER TABLE relay_groups ADD COLUMN IF NOT EXISTS sort_order INTEGER NOT NULL DEFAULT 0;
ALTER TABLE relay_groups ADD COLUMN IF NOT EXISTS monitor_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE relay_groups ADD COLUMN IF NOT EXISTS allow_image_generation BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE relay_groups ADD COLUMN IF NOT EXISTS image_rate_independent BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE relay_groups ADD COLUMN IF NOT EXISTS image_rate_multiplier DOUBLE PRECISION NOT NULL DEFAULT 1;
ALTER TABLE relay_groups ADD COLUMN IF NOT EXISTS image_price_1k NUMERIC(20,8);
ALTER TABLE relay_groups ADD COLUMN IF NOT EXISTS image_price_2k NUMERIC(20,8);
ALTER TABLE relay_groups ADD COLUMN IF NOT EXISTS image_price_4k NUMERIC(20,8);

CREATE TABLE IF NOT EXISTS relay_channels (
    id                             BIGSERIAL PRIMARY KEY,
    relay_station_id               BIGINT NOT NULL REFERENCES relay_stations(id) ON DELETE CASCADE,
    external_id                    BIGINT NOT NULL,
    name                           VARCHAR(256) NOT NULL,
    description                    VARCHAR(512),
    status                         VARCHAR(32),
    billing_model_source           VARCHAR(64),
    apply_pricing_to_account_stats BOOLEAN DEFAULT false,
    pricing_json                   TEXT,
    pricing_hash                   VARCHAR(64),
    pricing_models_json            TEXT,
    pricing_model_count            BIGINT DEFAULT 0,
    pricing_rule_count             BIGINT DEFAULT 0,
    pricing_changed_at             TIMESTAMPTZ,
    synced_at                      TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_relay_channel_external ON relay_channels (relay_station_id, external_id);

CREATE TABLE IF NOT EXISTS relay_channel_groups (
    relay_channel_id  BIGINT NOT NULL REFERENCES relay_channels(id) ON DELETE CASCADE,
    relay_group_id    BIGINT NOT NULL REFERENCES relay_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (relay_channel_id, relay_group_id)
);

CREATE TABLE IF NOT EXISTS relay_channel_mappings (
    id                         BIGSERIAL PRIMARY KEY,
    relay_station_id           BIGINT NOT NULL REFERENCES relay_stations(id) ON DELETE CASCADE,
    relay_channel_external_id  BIGINT NOT NULL,
    monitor_channel_id         BIGINT REFERENCES channels(id) ON DELETE SET NULL,
    upstream_group             VARCHAR(256),
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_relay_mapping_channel ON relay_channel_mappings (relay_station_id, relay_channel_external_id);

CREATE TABLE IF NOT EXISTS relay_channel_pricing_changes (
    id                         BIGSERIAL PRIMARY KEY,
    relay_station_id           BIGINT NOT NULL REFERENCES relay_stations(id) ON DELETE CASCADE,
    relay_channel_external_id  BIGINT NOT NULL,
    old_pricing_json           TEXT,
    new_pricing_json           TEXT,
    old_model_count            BIGINT DEFAULT 0,
    new_model_count            BIGINT DEFAULT 0,
    old_rule_count             BIGINT DEFAULT 0,
    new_rule_count             BIGINT DEFAULT 0,
    changed_at                 TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_relay_channel_pricing_changes_station ON relay_channel_pricing_changes (relay_station_id);
CREATE INDEX IF NOT EXISTS idx_relay_channel_pricing_changes_channel ON relay_channel_pricing_changes (relay_channel_external_id);
CREATE INDEX IF NOT EXISTS idx_relay_channel_pricing_changes_at ON relay_channel_pricing_changes (changed_at);

CREATE TABLE IF NOT EXISTS relay_accounts (
    id                BIGSERIAL PRIMARY KEY,
    relay_station_id  BIGINT NOT NULL REFERENCES relay_stations(id) ON DELETE CASCADE,
    external_id       BIGINT NOT NULL,
    name              VARCHAR(256) NOT NULL,
    base_url          VARCHAR(1024),
    platform          VARCHAR(64),
    type              VARCHAR(64),
    status            VARCHAR(32),
    schedulable       BOOLEAN NOT NULL DEFAULT true,
    concurrency       BIGINT NOT NULL DEFAULT 3,
    current_concurrency BIGINT NOT NULL DEFAULT 0,
    priority          BIGINT NOT NULL DEFAULT 50,
    pool_mode         BOOLEAN NOT NULL DEFAULT false,
    pool_mode_retry_count INTEGER NOT NULL DEFAULT 3,
    account_plan      VARCHAR(64),
    model_type        VARCHAR(64),
    rate_multiplier   DOUBLE PRECISION,
    rate_source       VARCHAR(32),
    rate_observed_at  TIMESTAMPTZ,
    latency_samples_json TEXT,
    last_used_at      TIMESTAMPTZ,
    synced_at         TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_relay_account_external ON relay_accounts (relay_station_id, external_id);
ALTER TABLE relay_accounts ADD COLUMN IF NOT EXISTS account_plan VARCHAR(64);
ALTER TABLE relay_accounts ADD COLUMN IF NOT EXISTS model_type VARCHAR(64);
ALTER TABLE relay_accounts ADD COLUMN IF NOT EXISTS current_concurrency BIGINT NOT NULL DEFAULT 0;
ALTER TABLE relay_accounts ADD COLUMN IF NOT EXISTS pool_mode BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE relay_accounts ADD COLUMN IF NOT EXISTS pool_mode_retry_count INTEGER NOT NULL DEFAULT 3;
ALTER TABLE relay_groups ADD COLUMN IF NOT EXISTS model_types_json TEXT;
ALTER TABLE relay_accounts ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_relay_accounts_synced_at ON relay_accounts (synced_at);

CREATE TABLE IF NOT EXISTS relay_account_groups (
    relay_account_id  BIGINT NOT NULL REFERENCES relay_accounts(id) ON DELETE CASCADE,
    relay_group_id    BIGINT NOT NULL REFERENCES relay_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (relay_account_id, relay_group_id)
);

DROP TABLE IF EXISTS relay_cost_fallbacks;

CREATE TABLE IF NOT EXISTS relay_account_cost_overrides (
    id                        BIGSERIAL PRIMARY KEY,
    relay_station_id          BIGINT NOT NULL REFERENCES relay_stations(id) ON DELETE CASCADE,
    relay_account_external_id BIGINT NOT NULL,
    mode                      VARCHAR(24) NOT NULL,
    monitor_channel_id        BIGINT REFERENCES channels(id) ON DELETE SET NULL,
    upstream_group            VARCHAR(256),
    manual_multiplier         DOUBLE PRECISION,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_relay_account_cost_override ON relay_account_cost_overrides (relay_station_id, relay_account_external_id);

CREATE TABLE IF NOT EXISTS relay_account_adjustment_logs (
    id                         BIGSERIAL PRIMARY KEY,
    relay_station_id           BIGINT NOT NULL REFERENCES relay_stations(id) ON DELETE CASCADE,
    relay_account_external_id  BIGINT NOT NULL,
    account_name               VARCHAR(256),
    account_platform           VARCHAR(64),
    cost_multiplier            DOUBLE PRECISION NOT NULL,
    old_group_ids_json         TEXT NOT NULL,
    new_group_ids_json         TEXT NOT NULL,
    recommended_group_id       BIGINT,
    old_concurrency            INTEGER,
    new_concurrency            INTEGER,
    old_priority               INTEGER,
    new_priority               INTEGER,
    old_pool_mode_retry_count  INTEGER,
    new_pool_mode_retry_count  INTEGER,
    source                     VARCHAR(16) NOT NULL,
    action                     VARCHAR(32) NOT NULL DEFAULT 'group_update',
    success                    BOOLEAN NOT NULL,
    error_message              TEXT,
    applied_at                 TIMESTAMPTZ NOT NULL
);
ALTER TABLE relay_account_adjustment_logs ADD COLUMN IF NOT EXISTS old_concurrency INTEGER;
ALTER TABLE relay_account_adjustment_logs ADD COLUMN IF NOT EXISTS new_concurrency INTEGER;
ALTER TABLE relay_account_adjustment_logs ADD COLUMN IF NOT EXISTS old_priority INTEGER;
ALTER TABLE relay_account_adjustment_logs ADD COLUMN IF NOT EXISTS new_priority INTEGER;
ALTER TABLE relay_account_adjustment_logs ADD COLUMN IF NOT EXISTS old_pool_mode_retry_count INTEGER;
ALTER TABLE relay_account_adjustment_logs ADD COLUMN IF NOT EXISTS new_pool_mode_retry_count INTEGER;
CREATE INDEX IF NOT EXISTS idx_relay_account_adjustment_logs_station ON relay_account_adjustment_logs (relay_station_id);
CREATE INDEX IF NOT EXISTS idx_relay_account_adjustment_logs_account ON relay_account_adjustment_logs (relay_account_external_id);
CREATE INDEX IF NOT EXISTS idx_relay_account_adjustment_logs_at ON relay_account_adjustment_logs (applied_at);

CREATE TABLE IF NOT EXISTS local_accounts (
    id                         BIGSERIAL PRIMARY KEY,
    name                       VARCHAR(256) NOT NULL,
    identifier                 VARCHAR(256) NOT NULL,
    platform                   VARCHAR(64) NOT NULL,
    account_type               VARCHAR(64) NOT NULL DEFAULT 'oauth',
    status                     VARCHAR(32) NOT NULL DEFAULT 'ready',
    purchase_cost              DOUBLE PRECISION NOT NULL DEFAULT 0,
    expected_quota             DOUBLE PRECISION NOT NULL DEFAULT 0,
    purchased_at               TIMESTAMPTZ NOT NULL,
    expires_at                 TIMESTAMPTZ,
    notes                      TEXT,
    relay_station_id           BIGINT REFERENCES relay_stations(id) ON DELETE SET NULL,
    relay_account_external_id  BIGINT,
    linked_at                  TIMESTAMPTZ,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_local_account_identity ON local_accounts (identifier, platform);
CREATE INDEX IF NOT EXISTS idx_local_accounts_status ON local_accounts (status);
CREATE INDEX IF NOT EXISTS idx_local_accounts_purchased_at ON local_accounts (purchased_at);
CREATE INDEX IF NOT EXISTS idx_local_accounts_relay_station ON local_accounts (relay_station_id);
CREATE INDEX IF NOT EXISTS idx_local_accounts_relay_external ON local_accounts (relay_account_external_id);

CREATE TABLE IF NOT EXISTS operation_ledger_entries (
    id                BIGSERIAL PRIMARY KEY,
    direction         VARCHAR(16) NOT NULL,
    category          VARCHAR(64) NOT NULL,
    amount            DOUBLE PRECISION NOT NULL,
    currency          VARCHAR(8) NOT NULL DEFAULT 'CNY',
    description       VARCHAR(512) NOT NULL,
    source            VARCHAR(32) NOT NULL DEFAULT 'manual',
    channel_id        BIGINT REFERENCES channels(id) ON DELETE SET NULL,
    relay_station_id  BIGINT REFERENCES relay_stations(id) ON DELETE SET NULL,
    local_account_id  BIGINT REFERENCES local_accounts(id) ON DELETE SET NULL,
    occurred_at       TIMESTAMPTZ NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_operation_ledger_direction ON operation_ledger_entries (direction);
CREATE INDEX IF NOT EXISTS idx_operation_ledger_category ON operation_ledger_entries (category);
CREATE INDEX IF NOT EXISTS idx_operation_ledger_source ON operation_ledger_entries (source);
CREATE INDEX IF NOT EXISTS idx_operation_ledger_channel ON operation_ledger_entries (channel_id);
CREATE INDEX IF NOT EXISTS idx_operation_ledger_station ON operation_ledger_entries (relay_station_id);
CREATE INDEX IF NOT EXISTS idx_operation_ledger_occurred_at ON operation_ledger_entries (occurred_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_operation_ledger_local_account ON operation_ledger_entries (local_account_id) WHERE local_account_id IS NOT NULL;
