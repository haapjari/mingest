-- Finish: done, or failed with an error code and message.
UPDATE ingestion SET
  state         = ?,
  error_code    = ?,
  error_message = ?,
  finished_at   = ?,
  updated_at    = ?,
  revision      = revision + 1
