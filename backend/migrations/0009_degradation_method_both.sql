-- Allow one group/account schedule to run pelican, candy, or both.
BEGIN;
ALTER TABLE group_degradation_configs DROP CONSTRAINT IF EXISTS chk_group_degradation_method;
ALTER TABLE group_degradation_configs ADD CONSTRAINT chk_group_degradation_method CHECK (method IN ('pelican','candy','both'));
COMMIT;
