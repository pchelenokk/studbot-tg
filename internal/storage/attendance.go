package storage

import (
	"database/sql"
	"fmt"
)

type Attendance struct {
	ID       int64  `json:"id"`
	UserID   int64  `json:"user_id"`
	Date     string `json:"date"`
	Status   string `json:"status"`
	MarkedBy int64  `json:"marked_by"`
}

func (d *DB) MarkAttendance(userID int64, date, status string, markedBy int64) error {
	_, err := d.conn.Exec(
		`INSERT INTO attendance (user_id, date, status, marked_by) VALUES (?, ?, ?, ?)
		 ON CONFLICT(user_id, date) DO UPDATE SET status=excluded.status, marked_by=excluded.marked_by`,
		userID, date, status, markedBy,
	)
	return err
}

func (d *DB) GetAttendance(userID int64, date string) (*Attendance, error) {
	row := d.conn.QueryRow(`SELECT id, user_id, date, status, marked_by FROM attendance WHERE user_id = ? AND date = ?`, userID, date)
	var a Attendance
	if err := row.Scan(&a.ID, &a.UserID, &a.Date, &a.Status, &a.MarkedBy); err != nil {
		return nil, err
	}
	return &a, nil
}

func (d *DB) ListAttendanceByDate(date string) ([]Attendance, error) {
	rows, err := d.conn.Query(`SELECT id, user_id, date, status, marked_by FROM attendance WHERE date = ?`, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Attendance
	for rows.Next() {
		var a Attendance
		if err := rows.Scan(&a.ID, &a.UserID, &a.Date, &a.Status, &a.MarkedBy); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

func (d *DB) ListAttendanceByUser(userID int64) ([]Attendance, error) {
	rows, err := d.conn.Query(`SELECT id, user_id, date, status, marked_by FROM attendance WHERE user_id = ? ORDER BY date DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Attendance
	for rows.Next() {
		var a Attendance
		if err := rows.Scan(&a.ID, &a.UserID, &a.Date, &a.Status, &a.MarkedBy); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

func (d *DB) DeleteAttendance(userID int64, date string) error {
	_, err := d.conn.Exec(`DELETE FROM attendance WHERE user_id = ? AND date = ?`, userID, date)
	return err
}

func (d *DB) GetAttendanceStats(userID int64) (int, int, error) {
	row := d.conn.QueryRow(`SELECT COUNT(*), SUM(CASE WHEN status = 'present' THEN 1 ELSE 0 END) FROM attendance WHERE user_id = ?`, userID)
	var total, present sql.NullInt64
	if err := row.Scan(&total, &present); err != nil {
		return 0, 0, err
	}
	return int(total.Int64), int(present.Int64), nil
}

func (d *DB) GetAttendanceByDate(date string) ([]struct {
	UserID   int64
	FullName string
	Status   string
}, error) {
	rows, err := d.conn.Query(`
		SELECT u.id, u.full_name, a.status
		FROM users u
		LEFT JOIN attendance a ON a.user_id = u.id AND a.date = ?
		WHERE u.role = 'student'
		ORDER BY u.full_name`, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []struct {
		UserID   int64
		FullName string
		Status   string
	}
	for rows.Next() {
		var item struct {
			UserID   int64
			FullName string
			Status   string
		}
		var status sql.NullString
		if err := rows.Scan(&item.UserID, &item.FullName, &status); err != nil {
			return nil, err
		}
		item.Status = status.String
		items = append(items, item)
	}
	return items, rows.Err()
}

func (d *DB) GetAttendanceFile(attID int64) (string, string, error) {
	row := d.conn.QueryRow(`SELECT attachment_id, attachment_type FROM attendance WHERE id = ?`, attID)
	var attIDVal, attType string
	if err := row.Scan(&attIDVal, &attType); err != nil {
		return "", "", err
	}
	if attIDVal == "" {
		return "", "", fmt.Errorf("no attachment")
	}
	return attIDVal, attType, nil
}
