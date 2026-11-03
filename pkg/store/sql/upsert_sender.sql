-- One Sender's new Messages in a chunk. The returned count equals the
-- chunk's only for a new Sender.
INSERT INTO sender (address, messages) VALUES (?, ?)
ON CONFLICT (address) DO UPDATE SET messages = messages + excluded.messages
RETURNING messages;
