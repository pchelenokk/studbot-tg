package storage

import (
	"database/sql"
	"time"
)

// Checkin is a vote-based attendance session created by a староста/профорг.
type Checkin struct {
	ID              int64  `json:"id"`
	Subject         string `json:"subject"`
	Date            string `json:"date"`
	DurationSeconds int64  `json:"duration_seconds"`
	CreatedBy       int64  `json:"created_by"`
	Status          string `json:"status"`
	ClosesAt        int64  `json:"closes_at"` // unix seconds
	CreatedAt       string `json:"created_at"`
}

// CheckinVoteRow joins a vote with the user's profile for the Excel export.
type CheckinVoteRow struct {
	CheckinID int64
	Subject   string
	Date      string
	UserID    int64
	FullName  string
	Username  string
	Status    string
	VotedAt   string
}

func (d *DB) CreateCheckin(subject, date string, durationSeconds, createdBy int64) (*Checkin, error) {
	closesAt := time.Now().Add(time.Duration(durationSeconds) * time.Second).Unix()
	res, err := d.conn.Exec(
		`INSERT INTO checkins (subject, date, duration_seconds, created_by, status, closes_at)
		 VALUES (?, ?, ?, ?, 'active', ?)`,
		subject, date, durationSeconds, createdBy, closesAt,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return d.GetCheckin(id)
}

func (d *DB) GetCheckin(id int64) (*Checkin, error) {
	row := d.conn.QueryRow(
		`SELECT id, subject, date, duration_seconds, created_by, status, closes_at, created_at
		 FROM checkins WHERE id = ?`, id)
	var c Checkin
	if err := row.Scan(&c.ID, &c.Subject, &c.Date, &c.DurationSeconds, &c.CreatedBy, &c.Status, &c.ClosesAt, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (d *DB) ListCheckins(limit int) ([]Checkin, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.conn.Query(
		`SELECT id, subject, date, duration_seconds, created_by, status, closes_at, created_at
		 FROM checkins ORDER BY date DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Checkin
	for rows.Next() {
		var c Checkin
		if err := rows.Scan(&c.ID, &c.Subject, &c.Date, &c.DurationSeconds,
			&c.CreatedBy, &c.Status, &c.ClosesAt, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CloseActiveCheckinsByCreator closes every open check-in of a creator. Used on
// startup so timers lost to a restart do not leave sessions open forever.
func (d *DB) CloseActiveCheckinsByCreator(creatorID int64) ([]int64, error) {
	rows, err := d.conn.Query(`SELECT id FROM checkins WHERE created_by = ? AND status = 'active'`, creatorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := d.CloseCheckin(id); err != nil {
			return ids, err
		}
	}
	return ids, nil
}
func (d *DB) CloseCheckin(id int64) (bool, error) {
	res, err := d.conn.Exec(
		`UPDATE checkins SET status = 'closed', closed_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = 'active'`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ListExpiredActiveCheckins returns active check-ins whose deadline passed.
func (d *DB) ListExpiredActiveCheckins(nowUnix int64) ([]Checkin, error) {
	rows, err := d.conn.Query(
		`SELECT id, subject, date, duration_seconds, created_by, status, closes_at, created_at
		 FROM checkins WHERE status = 'active' AND closes_at <= ? ORDER BY closes_at`, nowUnix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Checkin
	for rows.Next() {
		var c Checkin
		if err := rows.Scan(&c.ID, &c.Subject, &c.Date, &c.DurationSeconds, &c.CreatedBy, &c.Status, &c.ClosesAt, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// VoteCheckin records or updates a vote. The bot checks the check-in is active.
func (d *DB) VoteCheckin(checkinID, userID int64, status string) error {
	_, err := d.conn.Exec(
		`INSERT INTO checkin_votes (checkin_id, user_id, status) VALUES (?, ?, ?)
		 ON CONFLICT(checkin_id, user_id) DO UPDATE SET status = excluded.status, voted_at = CURRENT_TIMESTAMP`,
		checkinID, userID, status,
	)
	return err
}

// GetCheckinVotes returns all votes of a check-in joined with user profiles.
func (d *DB) GetCheckinVotes(checkinID int64) ([]CheckinVoteRow, error) {
	rows, err := d.conn.Query(`
		SELECT c.id, c.subject, c.date, v.user_id, u.full_name, u.username, v.status, v.voted_at
		FROM checkin_votes v
		JOIN checkins c ON c.id = v.checkin_id
		JOIN users u ON u.id = v.user_id
		WHERE v.checkin_id = ?
		ORDER BY u.full_name`, checkinID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CheckinVoteRow
	for rows.Next() {
		var r CheckinVoteRow
		var fullName, username sql.NullString
		if err := rows.Scan(&r.CheckinID, &r.Subject, &r.Date, &r.UserID, &fullName, &username, &r.Status, &r.VotedAt); err != nil {
			return nil, err
		}
		r.FullName = fullName.String
		r.Username = username.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// AllClosedCheckinVotes returns every vote of every closed check-in, ordered
// by date, for the cumulative Excel export.
func (d *DB) AllClosedCheckinVotes() ([]CheckinVoteRow, error) {
	rows, err := d.conn.Query(`
		SELECT c.id, c.subject, c.date, v.user_id, u.full_name, u.username, v.status, v.voted_at
		FROM checkin_votes v
		JOIN checkins c ON c.id = v.checkin_id
		JOIN users u ON u.id = v.user_id
		WHERE c.status = 'closed'
		ORDER BY c.date, c.id, u.full_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CheckinVoteRow
	for rows.Next() {
		var r CheckinVoteRow
		var fullName, username sql.NullString
		if err := rows.Scan(&r.CheckinID, &r.Subject, &r.Date, &r.UserID, &fullName, &username, &r.Status, &r.VotedAt); err != nil {
			return nil, err
		}
		r.FullName = fullName.String
		r.Username = username.String
		out = append(out, r)
	}
	return out, rows.Err()
}
