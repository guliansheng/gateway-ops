-- Independent account degradation schedules and retention/configuration.
BEGIN;
CREATE TABLE IF NOT EXISTS account_degradation_configs (
    id BIGSERIAL PRIMARY KEY,
    station_id BIGINT NOT NULL,
    account_external_id BIGINT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    method VARCHAR(16) NOT NULL DEFAULT 'pelican',
    model VARCHAR(256) NOT NULL DEFAULT '',
    judge_model VARCHAR(256) NOT NULL DEFAULT '',
    effort VARCHAR(16) NOT NULL DEFAULT 'medium',
    pelican_interval INTEGER NOT NULL DEFAULT 60,
    candy_interval INTEGER NOT NULL DEFAULT 10,
    history_limit INTEGER NOT NULL DEFAULT 100,
    disable_on_degraded BOOLEAN NOT NULL DEFAULT FALSE,
    protocol VARCHAR(16) NOT NULL DEFAULT '',
    pelican_next_run_at TIMESTAMPTZ,
    candy_next_run_at TIMESTAMPTZ,
    lease_token VARCHAR(64) NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ,
    CONSTRAINT chk_account_degradation_method CHECK (method IN ('pelican','candy','both')),
    CONSTRAINT chk_account_degradation_history CHECK (history_limit BETWEEN 1 AND 1000),
    CONSTRAINT chk_account_degradation_pelican_interval CHECK (pelican_interval BETWEEN 5 AND 1440),
    CONSTRAINT chk_account_degradation_candy_interval CHECK (candy_interval BETWEEN 5 AND 1440)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_account_degradation_config_scope ON account_degradation_configs (station_id, account_external_id);
CREATE INDEX IF NOT EXISTS idx_account_degradation_config_due ON account_degradation_configs (enabled, pelican_next_run_at, candy_next_run_at);
COMMIT;
