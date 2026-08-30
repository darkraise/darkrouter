-- Saved Chat-mode conversations, and the turns in them.
--
-- This is the first place darkrouter retains prompt text automatically and in
-- bulk. Spec section 8.2 argues the case at length and the short form is: this
-- table holds the operator's own typing rather than traffic passing through the
-- gateway, it grows only when a person types into it, and it is emptied by a
-- purge the settings screen offers rather than by a retention sweep.
--
-- It is not the first prompt text on disk: playground_presets.config already
-- holds a saved preset's system prompt. That one is governed by neither
-- playground.save_conversations nor the purge, and is removed by deleting the
-- preset in the preset picker.
--
-- config is the same opaque JSON object playground_presets carries. Without it
-- a conversation reopened tomorrow loses the system prompt that produced its
-- transcript, and "open this configuration in Lab" has nothing to open.
CREATE TABLE playground_conversations (
  id         TEXT PRIMARY KEY,
  title      TEXT    NOT NULL,
  model      TEXT    NOT NULL,
  dialect    TEXT    NOT NULL,
  config     TEXT    NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_playground_conversations_updated
  ON playground_conversations(updated_at DESC);

CREATE TABLE playground_messages (
  id              TEXT PRIMARY KEY,
  conversation_id TEXT    NOT NULL
                    REFERENCES playground_conversations(id) ON DELETE CASCADE,
  seq             INTEGER NOT NULL,
  role            TEXT    NOT NULL,
  content         TEXT    NOT NULL,
  -- The request whose trace explains this turn. Nullable: a turn can be
  -- stored before its trace is written, and a trace is swept on the log's
  -- retention schedule long before the conversation is deleted.
  request_id      TEXT,
  created_at      INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX idx_playground_messages_seq
  ON playground_messages(conversation_id, seq);
