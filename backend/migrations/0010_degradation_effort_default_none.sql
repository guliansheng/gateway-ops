-- Use the model provider's default reasoning effort for newly created degradation configs.
BEGIN;
ALTER TABLE group_degradation_configs ALTER COLUMN effort SET DEFAULT 'none';
ALTER TABLE account_degradation_configs ALTER COLUMN effort SET DEFAULT 'none';
COMMIT;
