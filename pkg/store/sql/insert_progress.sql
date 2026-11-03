-- Begin: the new Ingestion's progress row, in state running.
INSERT INTO ingestion (
  id, ingestion_id, state, source_url,
  mail_files, messages, senders, messages_without_sender,
  bytes_read, resumes, revision,
  started_at, updated_at
) VALUES (
  1, ?, 'running', ?,
  0, 0, 0, 0,
  0, 0, 1,
  ?, ?
)
