-- The kernel keeps a process name to fifteen characters, so every training
-- job on a card read as "python". The command line says which job it is. It
-- arrives with secret values already masked by the collector that read it.
--
-- Nullable: rows written before this migration have none, and neither do the
-- rows an older agent pushes.
ALTER TABLE gpu_processes ADD COLUMN cmdline TEXT;

INSERT INTO schema_version (version) VALUES (14);
