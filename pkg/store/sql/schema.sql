-- One file per Ingestion. Both data tables are WITHOUT ROWID, so each key
-- is stored once.
CREATE TABLE message (fingerprint BLOB PRIMARY KEY) WITHOUT ROWID;

CREATE TABLE sender (
  address  TEXT    PRIMARY KEY,
  messages INTEGER NOT NULL
) WITHOUT ROWID;

CREATE INDEX sender_top ON sender (messages DESC, address);

-- The progress row: exactly one.
CREATE TABLE ingestion (
  id                      INTEGER PRIMARY KEY CHECK (id = 1),
  ingestion_id            TEXT    NOT NULL,
  state                   TEXT    NOT NULL
                          CHECK (state IN ('running', 'done', 'failed')),
  source_url              TEXT    NOT NULL,
  source_size             INTEGER,
  source_version          TEXT,
  mail_files              INTEGER NOT NULL,
  messages                INTEGER NOT NULL,
  senders                 INTEGER NOT NULL,
  messages_without_sender INTEGER NOT NULL,
  bytes_read              INTEGER NOT NULL,
  resumes                 INTEGER NOT NULL,
  revision                INTEGER NOT NULL,
  started_at              TEXT    NOT NULL,
  updated_at              TEXT    NOT NULL,
  finished_at             TEXT,
  error_code              TEXT,
  error_message           TEXT
);
