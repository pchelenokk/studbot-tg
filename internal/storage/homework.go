package storage

import (
	"database/sql"
	"fmt"
	"strings"
)

type Homework struct {
	ID          int64  `json:"id"`
	Subject     string `json:"subject"`
	Title       string `json:"title"`
	Description string `json:"description"`
	DueDate     string `json:"due_date"`
	CreatedBy   int64  `json:"created_by"`
	CreatedAt   string `json:"created_at"`
}

func (d *DB) AddHomework(subject, title, description, dueDate string, createdBy int64) (*Homework, error) {
	res, err := d.conn.Exec(
		`INSERT INTO homework (subject, title, description, due_date, created_by) VALUES (?, ?, ?, ?, ?)`,
		subject, title, description, dueDate, createdBy,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return d.GetHomework(id)
}

func (d *DB) GetHomework(id int64) (*Homework, error) {
	row := d.conn.QueryRow(`SELECT id, subject, title, description, due_date, created_by, created_at FROM homework WHERE id = ?`, id)
	var h Homework
	var dueDate sql.NullString
	if err := row.Scan(&h.ID, &h.Subject, &h.Title, &h.Description, &dueDate, &h.CreatedBy, &h.CreatedAt); err != nil {
		return nil, err
	}
	h.DueDate = dueDate.String
	return &h, nil
}

func (d *DB) ListHomework(subject string) ([]Homework, error) {
	var rows *sql.Rows
	var err error
	if subject != "" {
		rows, err = d.conn.Query(`SELECT id, subject, title, description, due_date, created_by, created_at FROM homework WHERE subject = ? ORDER BY due_date`, subject)
	} else {
		rows, err = d.conn.Query(`SELECT id, subject, title, description, due_date, created_by, created_at FROM homework ORDER BY due_date`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Homework
	for rows.Next() {
		var h Homework
		var dueDate sql.NullString
		if err := rows.Scan(&h.ID, &h.Subject, &h.Title, &h.Description, &dueDate, &h.CreatedBy, &h.CreatedAt); err != nil {
			return nil, err
		}
		h.DueDate = dueDate.String
		items = append(items, h)
	}
	return items, rows.Err()
}

func (d *DB) DeleteHomework(id int64) error {
	_, err := d.conn.Exec(`DELETE FROM homework WHERE id = ?`, id)
	return err
}

func (d *DB) ListHomeworkSubjects() ([]string, error) {
	rows, err := d.conn.Query(`SELECT DISTINCT subject FROM homework ORDER BY subject`)
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

func (d *DB) SearchHomework(query string) ([]Homework, error) {
	q := "%" + strings.ToLower(query) + "%"
	rows, err := d.conn.Query(`SELECT id, subject, title, description, due_date, created_by, created_at FROM homework WHERE LOWER(title) LIKE ? OR LOWER(subject) LIKE ? OR LOWER(description) LIKE ?`, q, q, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Homework
	for rows.Next() {
		var h Homework
		var dueDate sql.NullString
		if err := rows.Scan(&h.ID, &h.Subject, &h.Title, &h.Description, &dueDate, &h.CreatedBy, &h.CreatedAt); err != nil {
			return nil, err
		}
		h.DueDate = dueDate.String
		items = append(items, h)
	}
	return items, rows.Err()
}

func (d *DB) GetHomeworkByID(id int64) (*Homework, error) {
	row := d.conn.QueryRow(`SELECT id, subject, title, description, due_date, created_by, created_at FROM homework WHERE id = ?`, id)
	var h Homework
	var dueDate sql.NullString
	if err := row.Scan(&h.ID, &h.Subject, &h.Title, &h.Description, &dueDate, &h.CreatedBy, &h.CreatedAt); err != nil {
		return nil, err
	}
	h.DueDate = dueDate.String
	return &h, nil
}

func (d *DB) GetHomeworkFile(hwID int64) (string, string, error) {
	row := d.conn.QueryRow(`SELECT attachment_id, attachment_type FROM homework WHERE id = ?`, hwID)
	var attID, attType string
	if err := row.Scan(&attID, &attType); err != nil {
		return "", "", err
	}
	if attID == "" {
		return "", "", fmt.Errorf("no attachment")
	}
	return attID, attType, nil
}
