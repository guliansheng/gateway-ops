-- Keep the pelican review standard separate from the generation prompt so
-- stations can tune the checklist without touching the prompt sent to the
-- answering model. Existing rows start empty and fall back to the built-in
-- checklist until they are saved again.
BEGIN;
ALTER TABLE degradation_prompt_settings ADD COLUMN IF NOT EXISTS pelican_reference text NOT NULL DEFAULT '';
COMMIT;
