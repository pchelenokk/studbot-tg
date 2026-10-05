package storage

import (
	"database/sql"
	"strings"
)

type User struct {
	ID       int64  `json:"id"`
	TgID     int64  `json:"tg_id"`
	Username string `json:"username"`
	FullName string `json:"full_name"`
	Role     string `json:"role"`
}

func (d *DB) UpsertUser(tgID int64, username, fullName string) (*User, error) {
	if fullName == "" {
		fullName = username
	}
	_, err := d.conn.Exec(
		`INSERT INTO users (tg_id, username, full_name) VALUES (?, ?, ?)
		 ON CONFLICT(tg_id) DO UPDATE SET username=excluded.username, full_name=excluded.full_name`,
		tgID, username, fullName,
	)
	if err != nil {
		return nil, err
	}
	return d.GetUserByTgID(tgID)
}

func (d *DB) GetUserByTgID(tgID int64) (*User, error) {
	row := d.conn.QueryRow(`SELECT id, tg_id, username, full_name, role FROM users WHERE tg_id = ?`, tgID)
	var u User
	var username, fullName sql.NullString
	if err := row.Scan(&u.ID, &u.TgID, &username, &fullName, &u.Role); err != nil {
		return nil, err
	}
	u.Username = username.String
	u.FullName = fullName.String
	return &u, nil
}

func (d *DB) GetUserByID(id int64) (*User, error) {
	row := d.conn.QueryRow(`SELECT id, tg_id, username, full_name, role FROM users WHERE id = ?`, id)
	var u User
	var username, fullName sql.NullString
	if err := row.Scan(&u.ID, &u.TgID, &username, &fullName, &u.Role); err != nil {
		return nil, err
	}
	u.Username = username.String
	u.FullName = fullName.String
	return &u, nil
}

func (d *DB) SetRole(tgID int64, role string) error {
	_, err := d.conn.Exec(`UPDATE users SET role = ? WHERE tg_id = ?`, role, tgID)
	return err
}

// GetUserByUsername finds a user by Telegram username, case-insensitively and
// ignoring a leading "@".
func (d *DB) GetUserByUsername(username string) (*User, error) {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	row := d.conn.QueryRow(`SELECT id, tg_id, username, full_name, role FROM users WHERE lower(username) = lower(?)`, username)
	var u User
	var uname, fullName sql.NullString
	if err := row.Scan(&u.ID, &u.TgID, &uname, &fullName, &u.Role); err != nil {
		return nil, err
	}
	u.Username = uname.String
	u.FullName = fullName.String
	return &u, nil
}

func (d *DB) ListUsers() ([]User, error) {
	rows, err := d.conn.Query(`SELECT id, tg_id, username, full_name, role FROM users ORDER BY full_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		var username, fullName sql.NullString
		if err := rows.Scan(&u.ID, &u.TgID, &username, &fullName, &u.Role); err != nil {
			return nil, err
		}
		u.Username = username.String
		u.FullName = fullName.String
		users = append(users, u)
	}
	return users, rows.Err()
}

func (d *DB) ListUsersByRole(role string) ([]User, error) {
	rows, err := d.conn.Query(`SELECT id, tg_id, username, full_name, role FROM users WHERE role = ? ORDER BY full_name`, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		var username, fullName sql.NullString
		if err := rows.Scan(&u.ID, &u.TgID, &username, &fullName, &u.Role); err != nil {
			return nil, err
		}
		u.Username = username.String
		u.FullName = fullName.String
		users = append(users, u)
	}
	return users, rows.Err()
}
