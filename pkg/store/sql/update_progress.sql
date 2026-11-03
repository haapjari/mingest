-- A chunk's counts, in the same transaction as its Messages and Senders.
UPDATE ingestion SET
  mail_files              = mail_files + ?,
  messages                = messages + ?,
  senders                 = senders + ?,
  messages_without_sender = messages_without_sender + ?,
  bytes_read              = ?,
  updated_at              = ?,
  revision                = revision + 1
