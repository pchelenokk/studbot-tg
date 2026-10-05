package storage

import (
	"path/filepath"
	"testing"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestUserFullNameRoundTrip(t *testing.T) {
	db := newTestDB(t)

	created, err := db.UpsertUser(111, "misha", "Михаил Макаров")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if created.FullName != "Михаил Макаров" {
		t.Fatalf("UpsertUser returned FullName %q, want %q", created.FullName, "Михаил Макаров")
	}
	if created.Username != "misha" {
		t.Fatalf("UpsertUser returned Username %q, want %q", created.Username, "misha")
	}

	byTg, err := db.GetUserByTgID(111)
	if err != nil {
		t.Fatalf("GetUserByTgID: %v", err)
	}
	if byTg.FullName != "Михаил Макаров" {
		t.Errorf("GetUserByTgID FullName = %q, want %q", byTg.FullName, "Михаил Макаров")
	}

	byID, err := db.GetUserByID(created.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if byID.FullName != "Михаил Макаров" {
		t.Errorf("GetUserByID FullName = %q, want %q", byID.FullName, "Михаил Макаров")
	}

	list, err := db.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(list) != 1 || list[0].FullName != "Михаил Макаров" {
		t.Errorf("ListUsers = %+v, want one user with a full name", list)
	}

	byRole, err := db.ListUsersByRole("student")
	if err != nil {
		t.Fatalf("ListUsersByRole: %v", err)
	}
	if len(byRole) != 1 || byRole[0].FullName != "Михаил Макаров" {
		t.Errorf("ListUsersByRole = %+v, want one user with a full name", byRole)
	}
}

// Upsert must refresh the profile without touching the role.
func TestUpsertUserKeepsRole(t *testing.T) {
	db := newTestDB(t)

	if _, err := db.UpsertUser(222, "old", "Старое Имя"); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if err := db.SetRole(222, "proforg"); err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	updated, err := db.UpsertUser(222, "new", "Новое Имя")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if updated.Role != "proforg" {
		t.Errorf("Role = %q, want proforg", updated.Role)
	}
	if updated.FullName != "Новое Имя" || updated.Username != "new" {
		t.Errorf("profile not refreshed: %+v", updated)
	}
}

func TestPragmasAndIndexes(t *testing.T) {
	db := newTestDB(t)

	var journal string
	if err := db.conn.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if journal != "wal" {
		t.Errorf("journal_mode = %q, want wal", journal)
	}

	var timeout int
	if err := db.conn.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if timeout == 0 {
		t.Error("busy_timeout must be set")
	}

	want := []string{
		"idx_books_subject", "idx_homework_subject",
		"idx_attendance_date", "idx_poll_votes_poll", "idx_polls_active",
	}
	for _, name := range want {
		var got string
		err := db.conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name = ?`, name).Scan(&got)
		if err != nil || got != name {
			t.Errorf("index %s is missing (err %v)", name, err)
		}
	}
}

func TestBookAttachmentRoundTrip(t *testing.T) {
	db := newTestDB(t)

	book, err := db.AddBook("Матан", "Математика", "Лунгу", "", "local:file.pdf", "document", 0)
	if err != nil {
		t.Fatalf("AddBook: %v", err)
	}
	attID, attType, err := db.GetBookFile(book.ID)
	if err != nil {
		t.Fatalf("GetBookFile: %v", err)
	}
	if attID != "local:file.pdf" || attType != "document" {
		t.Errorf("GetBookFile = (%q, %q)", attID, attType)
	}
}
