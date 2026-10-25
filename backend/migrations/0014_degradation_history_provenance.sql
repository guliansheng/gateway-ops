-- Manual review and account-test imports keep their provenance next to the
-- verdict: a manually overridden row remembers the superseded verdict, and an
-- imported account test remembers which account result it copies. Existing rows
-- keep the zero/empty defaults, so old history reads exactly as before.
-- The server also AutoMigrates this model on start; this file covers deployments
-- that apply migrations/ by hand before restarting.
BEGIN;
ALTER TABLE group_degradation_results
    ADD COLUMN IF NOT EXISTS source_account_id BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS source_account_name VARCHAR(256) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_result_id BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS original_status VARCHAR(16) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS adjusted_at TIMESTAMPTZ;
COMMIT;
