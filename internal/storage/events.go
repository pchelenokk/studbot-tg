package storage

import (
	"database/sql"
	"strings"
)

type Event struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	EventDate   string `json:"event_date"`
	CreatedBy   int64  `json:"created_by"`
	CreatedAt   string `json:"created_at"`
}

func (d *DB) AddEvent(title, description, eventDate string, createdBy int64) (*Event, error) {
	res, err := d.conn.Exec(
		`INSERT INTO events (title, description, event_date, created_by) VALUES (?, ?, ?, ?)`,
		title, description, eventDate, createdBy,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return d.GetEvent(id)
}

func (d *DB) GetEvent(id int64) (*Event, error) {
	row := d.conn.QueryRow(`SELECT id, title, description, event_date, created_by, created_at FROM events WHERE id = ?`, id)
	var e Event
	var eventDate sql.NullString
	if err := row.Scan(&e.ID, &e.Title, &e.Description, &eventDate, &e.CreatedBy, &e.CreatedAt); err != nil {
		return nil, err
	}
	e.EventDate = eventDate.String
	return &e, nil
}

func (d *DB) ListEvents() ([]Event, error) {
	rows, err := d.conn.Query(`SELECT id, title, description, event_date, created_by, created_at FROM events ORDER BY event_date`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var e Event
		var eventDate sql.NullString
		if err := rows.Scan(&e.ID, &e.Title, &e.Description, &eventDate, &e.CreatedBy, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.EventDate = eventDate.String
		events = append(events, e)
	}
	return events, rows.Err()
}

func (d *DB) DeleteEvent(id int64) error {
	_, err := d.conn.Exec(`DELETE FROM events WHERE id = ?`, id)
	return err
}

func (d *DB) SearchEvents(query string) ([]Event, error) {
	q := "%" + strings.ToLower(query) + "%"
	rows, err := d.conn.Query(`SELECT id, title, description, event_date, created_by, created_at FROM events WHERE LOWER(title) LIKE ? OR LOWER(description) LIKE ?`, q, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var e Event
		var eventDate sql.NullString
		if err := rows.Scan(&e.ID, &e.Title, &e.Description, &eventDate, &e.CreatedBy, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.EventDate = eventDate.String
		events = append(events, e)
	}
	return events, rows.Err()
}
