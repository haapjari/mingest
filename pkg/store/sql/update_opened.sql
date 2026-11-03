-- Opened: the Source's size and version on its first open, which leaves
-- source_version NULL until then, and another resume on every later one.
-- Every expression reads the row as it was before the update.
UPDATE ingestion SET
  source_size    = CASE WHEN source_version IS NULL THEN ? ELSE source_size END,
  resumes        = resumes + (source_version IS NOT NULL),
  source_version = COALESCE(source_version, ?),
  updated_at     = ?,
  revision       = revision + 1
