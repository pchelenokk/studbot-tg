package storage

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
}

func New(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	// A single writer is enough here and it removes "database is locked"
	// errors, which are the usual failure of SQLite under concurrent use.
	conn.SetMaxOpenConns(1)
	conn.SetConnMaxLifetime(0)
	if err := conn.Ping(); err != nil {
		return nil, err
	}
	db := &DB{conn: conn}
	if err := db.pragmas(); err != nil {
		return nil, err
	}
	if err := db.migrate(); err != nil {
		return nil, err
	}
	return db, nil
}

// pragmas configures the connection. Foreign keys are deliberately left off:
// rows created before a user exists store 0 in uploaded_by/created_by, so
// enabling the check would reject perfectly valid inserts.
func (d *DB) pragmas() error {
	stmts := []string{
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = NORMAL`,
	}
	for _, s := range stmts {
		if _, err := d.conn.Exec(s); err != nil {
			return fmt.Errorf("pragma %q: %w", s, err)
		}
	}
	return nil
}

func (d *DB) Close() error { return d.conn.Close() }

func (d *DB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tg_id INTEGER UNIQUE NOT NULL,
			username TEXT,
			full_name TEXT,
			role TEXT NOT NULL DEFAULT 'student',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS books (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			subject TEXT NOT NULL,
			author TEXT,
			description TEXT,
			attachment_id TEXT,
			attachment_type TEXT,
			uploaded_by INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(uploaded_by) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS homework (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			subject TEXT NOT NULL,
			title TEXT NOT NULL,
			description TEXT,
			due_date TEXT,
			attachment_id TEXT,
			attachment_type TEXT,
			created_by INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(created_by) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS attendance (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			date TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'present',
			attachment_id TEXT,
			attachment_type TEXT,
			marked_by INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, date),
			FOREIGN KEY(user_id) REFERENCES users(id),
			FOREIGN KEY(marked_by) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS polls (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			question TEXT NOT NULL,
			options TEXT NOT NULL,
			attachment_id TEXT,
			attachment_type TEXT,
			created_by INTEGER,
			is_active INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(created_by) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS poll_votes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			poll_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			option_index INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(poll_id, user_id),
			FOREIGN KEY(poll_id) REFERENCES polls(id),
			FOREIGN KEY(user_id) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			description TEXT,
			event_date TEXT,
			created_by INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(created_by) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS checkins (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			subject TEXT NOT NULL,
			date TEXT NOT NULL,
			duration_seconds INTEGER NOT NULL DEFAULT 90,
			created_by INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			closes_at INTEGER NOT NULL,
			closed_at DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS checkin_votes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			checkin_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			status TEXT NOT NULL,
			voted_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(checkin_id, user_id),
			FOREIGN KEY(checkin_id) REFERENCES checkins(id),
			FOREIGN KEY(user_id) REFERENCES users(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_books_subject ON books(subject)`,
		`CREATE INDEX IF NOT EXISTS idx_homework_subject ON homework(subject)`,
		`CREATE INDEX IF NOT EXISTS idx_attendance_date ON attendance(date)`,
		`CREATE INDEX IF NOT EXISTS idx_poll_votes_poll ON poll_votes(poll_id)`,
		`CREATE INDEX IF NOT EXISTS idx_polls_active ON polls(is_active, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_checkins_status ON checkins(status, closes_at)`,
		`CREATE INDEX IF NOT EXISTS idx_checkin_votes_checkin ON checkin_votes(checkin_id)`,
	}
	for _, s := range stmts {
		if _, err := d.conn.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

func now() string   { return time.Now().Format("2006-01-02 15:04:05") }
func today() string { return time.Now().Format("2006-01-02") }
