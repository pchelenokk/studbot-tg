package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

type Poll struct {
	ID        int64    `json:"id"`
	Question  string   `json:"question"`
	Options   []string `json:"options"`
	CreatedBy int64    `json:"created_by"`
	IsActive  bool     `json:"is_active"`
	CreatedAt string   `json:"created_at"`
}

type PollVote struct {
	ID          int64 `json:"id"`
	PollID      int64 `json:"poll_id"`
	UserID      int64 `json:"user_id"`
	OptionIndex int   `json:"option_index"`
}

func (d *DB) CreatePoll(question string, options []string, createdBy int64) (*Poll, error) {
	optsJSON, _ := json.Marshal(options)
	res, err := d.conn.Exec(
		`INSERT INTO polls (question, options, created_by, is_active) VALUES (?, ?, ?, 1)`,
		question, string(optsJSON), createdBy,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return d.GetPoll(id)
}

func (d *DB) GetPoll(id int64) (*Poll, error) {
	row := d.conn.QueryRow(`SELECT id, question, options, created_by, is_active, created_at FROM polls WHERE id = ?`, id)
	var p Poll
	var optsJSON string
	var isActive int
	if err := row.Scan(&p.ID, &p.Question, &optsJSON, &p.CreatedBy, &isActive, &p.CreatedAt); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(optsJSON), &p.Options)
	p.IsActive = isActive == 1
	return &p, nil
}

func (d *DB) ListPolls(activeOnly bool) ([]Poll, error) {
	var rows *sql.Rows
	var err error
	if activeOnly {
		rows, err = d.conn.Query(`SELECT id, question, options, created_by, is_active, created_at FROM polls WHERE is_active = 1 ORDER BY created_at DESC`)
	} else {
		rows, err = d.conn.Query(`SELECT id, question, options, created_by, is_active, created_at FROM polls ORDER BY created_at DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var polls []Poll
	for rows.Next() {
		var p Poll
		var optsJSON string
		var isActive int
		if err := rows.Scan(&p.ID, &p.Question, &optsJSON, &p.CreatedBy, &isActive, &p.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(optsJSON), &p.Options)
		p.IsActive = isActive == 1
		polls = append(polls, p)
	}
	return polls, rows.Err()
}

func (d *DB) ClosePoll(id int64) error {
	_, err := d.conn.Exec(`UPDATE polls SET is_active = 0 WHERE id = ?`, id)
	return err
}

func (d *DB) DeletePoll(id int64) error {
	_, err := d.conn.Exec(`DELETE FROM polls WHERE id = ?`, id)
	return err
}

func (d *DB) Vote(pollID, userID int64, optionIndex int) error {
	_, err := d.conn.Exec(
		`INSERT INTO poll_votes (poll_id, user_id, option_index) VALUES (?, ?, ?)
		 ON CONFLICT(poll_id, user_id) DO UPDATE SET option_index=excluded.option_index`,
		pollID, userID, optionIndex,
	)
	return err
}

func (d *DB) GetVote(pollID, userID int64) (*PollVote, error) {
	row := d.conn.QueryRow(`SELECT id, poll_id, user_id, option_index FROM poll_votes WHERE poll_id = ? AND user_id = ?`, pollID, userID)
	var v PollVote
	if err := row.Scan(&v.ID, &v.PollID, &v.UserID, &v.OptionIndex); err != nil {
		return nil, err
	}
	return &v, nil
}

func (d *DB) GetPollResults(pollID int64) ([]struct {
	OptionIndex int
	Count       int
}, error) {
	rows, err := d.conn.Query(`SELECT option_index, COUNT(*) FROM poll_votes WHERE poll_id = ? GROUP BY option_index`, pollID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []struct {
		OptionIndex int
		Count       int
	}
	for rows.Next() {
		var r struct {
			OptionIndex int
			Count       int
		}
		if err := rows.Scan(&r.OptionIndex, &r.Count); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (d *DB) GetPollFile(pollID int64) (string, string, error) {
	row := d.conn.QueryRow(`SELECT attachment_id, attachment_type FROM polls WHERE id = ?`, pollID)
	var attID, attType string
	if err := row.Scan(&attID, &attType); err != nil {
		return "", "", err
	}
	if attID == "" {
		return "", "", fmt.Errorf("no attachment")
	}
	return attID, attType, nil
}

func (d *DB) GetPollsByCreator(creatorID int64) ([]Poll, error) {
	rows, err := d.conn.Query(`SELECT id, question, options, created_by, is_active, created_at FROM polls WHERE created_by = ? ORDER BY created_at DESC`, creatorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var polls []Poll
	for rows.Next() {
		var p Poll
		var optsJSON string
		var isActive int
		if err := rows.Scan(&p.ID, &p.Question, &optsJSON, &p.CreatedBy, &isActive, &p.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(optsJSON), &p.Options)
		p.IsActive = isActive == 1
		polls = append(polls, p)
	}
	return polls, rows.Err()
}

func (d *DB) SearchPolls(query string) ([]Poll, error) {
	q := "%" + strings.ToLower(query) + "%"
	rows, err := d.conn.Query(`SELECT id, question, options, created_by, is_active, created_at FROM polls WHERE LOWER(question) LIKE ?`, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var polls []Poll
	for rows.Next() {
		var p Poll
		var optsJSON string
		var isActive int
		if err := rows.Scan(&p.ID, &p.Question, &optsJSON, &p.CreatedBy, &isActive, &p.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(optsJSON), &p.Options)
		p.IsActive = isActive == 1
		polls = append(polls, p)
	}
	return polls, rows.Err()
}
