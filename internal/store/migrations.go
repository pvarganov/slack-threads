package store

// migration is a single forward-only schema step. Steps are applied in slice
// order and the applied count is recorded in SQLite's `user_version` pragma,
// so re-opening an up-to-date database is a no-op.
type migration struct {
	// name is a human-readable label used in error messages only.
	name string
	// stmts are executed in order inside one transaction.
	stmts []string
}

// migrations holds every schema step ever shipped. Never edit or reorder an
// existing entry: append a new one instead.
var migrations = []migration{
	{
		name: "initial schema",
		stmts: []string{
			`CREATE TABLE threads (
				id                INTEGER PRIMARY KEY AUTOINCREMENT,
				channel_id        TEXT    NOT NULL,
				thread_ts         TEXT    NOT NULL,
				workspace         TEXT    NOT NULL DEFAULT '',
				title             TEXT    NOT NULL DEFAULT '',
				added_at          INTEGER NOT NULL,
				last_fetched_at   INTEGER,
				archived          INTEGER NOT NULL DEFAULT 0,
				claude_session_id TEXT    NOT NULL DEFAULT ''
			)`,
			`CREATE UNIQUE INDEX idx_threads_channel_ts ON threads (channel_id, thread_ts)`,
			`CREATE INDEX idx_threads_archived ON threads (archived, added_at)`,

			`CREATE TABLE messages (
				id        INTEGER PRIMARY KEY AUTOINCREMENT,
				thread_id INTEGER NOT NULL REFERENCES threads (id) ON DELETE CASCADE,
				ts        TEXT    NOT NULL,
				user_id   TEXT    NOT NULL DEFAULT '',
				text      TEXT    NOT NULL DEFAULT '',
				raw_json  TEXT    NOT NULL DEFAULT '',
				edited_ts TEXT    NOT NULL DEFAULT '',
				text_hash TEXT    NOT NULL DEFAULT ''
			)`,
			`CREATE UNIQUE INDEX idx_messages_thread_ts ON messages (thread_id, ts)`,

			`CREATE TABLE translations (
				message_id INTEGER PRIMARY KEY REFERENCES messages (id) ON DELETE CASCADE,
				text_ru    TEXT    NOT NULL,
				model      TEXT    NOT NULL DEFAULT '',
				created_at INTEGER NOT NULL
			)`,

			`CREATE TABLE summaries (
				thread_id   INTEGER PRIMARY KEY REFERENCES threads (id) ON DELETE CASCADE,
				text_ru     TEXT    NOT NULL,
				based_on_ts TEXT    NOT NULL DEFAULT '',
				updated_at  INTEGER NOT NULL
			)`,

			`CREATE TABLE users (
				id           TEXT    PRIMARY KEY,
				display_name TEXT    NOT NULL DEFAULT '',
				real_name    TEXT    NOT NULL DEFAULT '',
				is_bot       INTEGER NOT NULL DEFAULT 0,
				updated_at   INTEGER NOT NULL
			)`,

			`CREATE TABLE drafts (
				thread_id  INTEGER PRIMARY KEY REFERENCES threads (id) ON DELETE CASCADE,
				text_ru    TEXT    NOT NULL DEFAULT '',
				text_en    TEXT    NOT NULL DEFAULT '',
				back_ru    TEXT    NOT NULL DEFAULT '',
				updated_at INTEGER NOT NULL
			)`,
		},
	},
}

// SchemaVersion is the schema version a freshly migrated database carries.
var SchemaVersion = len(migrations)
