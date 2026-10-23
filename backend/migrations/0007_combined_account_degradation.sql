-- Independent method clocks; zero intervals retain legacy configuration on read.
BEGIN;
ALTER TABLE group_degradation_configs
    ADD COLUMN IF NOT EXISTS pelican_interval_minutes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS candy_interval_minutes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS pelican_last_run_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS candy_last_run_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS pelican_next_run_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS candy_next_run_at TIMESTAMPTZ;
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_degradation_pelican_interval' AND conrelid = 'group_degradation_configs'::regclass) THEN
        ALTER TABLE group_degradation_configs ADD CONSTRAINT chk_degradation_pelican_interval CHECK (pelican_interval_minutes = 0 OR pelican_interval_minutes BETWEEN 5 AND 1440);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_degradation_candy_interval' AND conrelid = 'group_degradation_configs'::regclass) THEN
        ALTER TABLE group_degradation_configs ADD CONSTRAINT chk_degradation_candy_interval CHECK (candy_interval_minutes = 0 OR candy_interval_minutes BETWEEN 5 AND 1440);
    END IF;
END $$;
CREATE TABLE IF NOT EXISTS account_degradation_runs (
    id BIGSERIAL PRIMARY KEY,
    station_id BIGINT NOT NULL,
    account_external_id BIGINT NOT NULL,
    lease_token VARCHAR(64) NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_account_degradation_scope ON account_degradation_runs (station_id, account_external_id);
CREATE TABLE IF NOT EXISTS account_degradation_results (
    id BIGSERIAL PRIMARY KEY,
    station_id BIGINT NOT NULL,
    group_external_id BIGINT NOT NULL DEFAULT 0,
    account_external_id BIGINT NOT NULL,
    method VARCHAR(16) NOT NULL CHECK (method IN ('pelican', 'candy')),
    model VARCHAR(256) NOT NULL,
    judge_model VARCHAR(256) NOT NULL,
    effort VARCHAR(16) NOT NULL CHECK (effort IN ('none','low','medium','high')),
    status VARCHAR(16) NOT NULL CHECK (status IN ('normal','degraded','error','inconclusive')),
    reason TEXT NOT NULL,
    html TEXT NOT NULL DEFAULT '',
    answer TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_account_degradation_results ON account_degradation_results (station_id, account_external_id, created_at DESC, id DESC);
COMMIT;
