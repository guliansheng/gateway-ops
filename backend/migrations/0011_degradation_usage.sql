-- Record the upstream account and the Sub2API charge of every degradation run.
-- The values are filled in asynchronously after a run finishes, so the columns
-- must be nullable-free with safe defaults for existing history rows.
BEGIN;
ALTER TABLE group_degradation_results
    ADD COLUMN IF NOT EXISTS used_account_id BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS used_account_name VARCHAR(256) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS base_cost DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cost_multiplier DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS actual_cost DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS usage_synced_at TIMESTAMPTZ;

ALTER TABLE account_degradation_results
    ADD COLUMN IF NOT EXISTS used_account_id BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS used_account_name VARCHAR(256) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS base_cost DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cost_multiplier DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS actual_cost DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS usage_synced_at TIMESTAMPTZ;
COMMIT;
