package storage

import (
	"database/sql"
	"fmt"
	"strings"
)

type Book struct {
	ID             int64  `json:"id"`
	Title          string `json:"title"`
	Subject        string `json:"subject"`
	Author         string `json:"author"`
	Description    string `json:"description"`
	AttachmentID   string `json:"attachment_id"`
	AttachmentType string `json:"attachment_type"`
	UploadedBy     int64  `json:"uploaded_by"`
	CreatedAt      string `json:"created_at"`
}

func (d *DB) AddBook(title, subject, author, description, attachmentID, attachmentType string, uploadedBy int64) (*Book, error) {
	res, err := d.conn.Exec(
		`INSERT INTO books (title, subject, author, description, attachment_id, attachment_type, uploaded_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		title, subject, author, description, attachmentID, attachmentType, uploadedBy,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return d.GetBook(id)
}

func (d *DB) GetBook(id int64) (*Book, error) {
	row := d.conn.QueryRow(`SELECT id, title, subject, author, description, attachment_id, attachment_type, uploaded_by, created_at FROM books WHERE id = ?`, id)
	var b Book
	var author, description, attachmentID, attachmentType sql.NullString
	if err := row.Scan(&b.ID, &b.Title, &b.Subject, &author, &description, &attachmentID, &attachmentType, &b.UploadedBy, &b.CreatedAt); err != nil {
		return nil, err
	}
	b.Author = author.String
	b.Description = description.String
	b.AttachmentID = attachmentID.String
	b.AttachmentType = attachmentType.String
	return &b, nil
}

func (d *DB) ListBooks(subject string) ([]Book, error) {
	var rows *sql.Rows
	var err error
	if subject != "" {
		rows, err = d.conn.Query(`SELECT id, title, subject, author, description, attachment_id, attachment_type, uploaded_by, created_at FROM books WHERE subject = ? ORDER BY title`, subject)
	} else {
		rows, err = d.conn.Query(`SELECT id, title, subject, author, description, attachment_id, attachment_type, uploaded_by, created_at FROM books ORDER BY subject, title`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var books []Book
	for rows.Next() {
		var b Book
		var author, description, attachmentID, attachmentType sql.NullString
		if err := rows.Scan(&b.ID, &b.Title, &b.Subject, &author, &description, &attachmentID, &attachmentType, &b.UploadedBy, &b.CreatedAt); err != nil {
			return nil, err
		}
		b.Author = author.String
		b.Description = description.String
		b.AttachmentID = attachmentID.String
		b.AttachmentType = attachmentType.String
		books = append(books, b)
	}
	return books, rows.Err()
}

func (d *DB) DeleteBook(id int64) error {
	_, err := d.conn.Exec(`DELETE FROM books WHERE id = ?`, id)
	return err
}

func (d *DB) ListSubjects() ([]string, error) {
	rows, err := d.conn.Query(`SELECT DISTINCT subject FROM books ORDER BY subject`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subjects []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		subjects = append(subjects, s)
	}
	return subjects, rows.Err()
}

func (d *DB) SearchBooks(query string) ([]Book, error) {
	q := "%" + strings.ToLower(query) + "%"
	rows, err := d.conn.Query(`SELECT id, title, subject, author, description, attachment_id, attachment_type, uploaded_by, created_at FROM books WHERE LOWER(title) LIKE ? OR LOWER(subject) LIKE ? OR LOWER(author) LIKE ?`, q, q, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var books []Book
	for rows.Next() {
		var b Book
		var author, description, attachmentID, attachmentType sql.NullString
		if err := rows.Scan(&b.ID, &b.Title, &b.Subject, &author, &description, &attachmentID, &attachmentType, &b.UploadedBy, &b.CreatedAt); err != nil {
			return nil, err
		}
		b.Author = author.String
		b.Description = description.String
		b.AttachmentID = attachmentID.String
		b.AttachmentType = attachmentType.String
		books = append(books, b)
	}
	return books, rows.Err()
}

func (d *DB) GetBookFile(bookID int64) (string, string, error) {
	row := d.conn.QueryRow(`SELECT attachment_id, attachment_type FROM books WHERE id = ?`, bookID)
	var attID, attType string
	if err := row.Scan(&attID, &attType); err != nil {
		return "", "", err
	}
	if attID == "" {
		return "", "", fmt.Errorf("no attachment")
	}
	return attID, attType, nil
}
