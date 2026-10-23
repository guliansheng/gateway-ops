-- Keep 20 records per monitoring target by default, and make sure the group
-- method CHECK accepts the combined "pelican + candy" schedule on databases
-- created before that option existed.
BEGIN;
ALTER TABLE group_degradation_configs ALTER COLUMN history_limit SET DEFAULT 20;
ALTER TABLE account_degradation_configs ALTER COLUMN history_limit SET DEFAULT 20;

ALTER TABLE group_degradation_configs DROP CONSTRAINT IF EXISTS chk_group_degradation_method;
ALTER TABLE group_degradation_configs ADD CONSTRAINT chk_group_degradation_method CHECK (method IN ('pelican','candy','both'));
COMMIT;
