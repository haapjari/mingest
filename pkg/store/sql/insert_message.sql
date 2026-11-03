-- One Mail file's Message, if it is absent. One affected row means a new
-- Message.
INSERT INTO message (fingerprint) VALUES (?)
ON CONFLICT DO NOTHING;
