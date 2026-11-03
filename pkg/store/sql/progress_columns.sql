-- The progress row's columns, in the order scanStatus reads them. Every
-- statement that returns the row ends with them.
  ingestion_id, state, source_url, source_size, source_version,
  mail_files, messages, senders, messages_without_sender,
  bytes_read, resumes, revision,
  started_at, updated_at, finished_at,
  error_code, error_message
