package main

import (
	"database/sql"
	"errors"
	"net/url"
	"time"

	"github.com/lib/pq"
)

type Feed struct {
	ID       int64
	Link     string
	NotUntil time.Time
}

type Feeds struct {
	DB *sql.DB
}

func (f *Feeds) Create(link *url.URL, notUntil time.Time) (*Feed, error) {
	stmt := `INSERT INTO feeds (link, not_until) VALUES ($1, $2) RETURNING id, link, not_until`
	args := []any{link.String(), notUntil}

	var (
		created = new(Feed)
		pqerr   *pq.Error
	)
	err := f.DB.QueryRow(stmt, args...).Scan(&created.ID, &created.Link, &created.NotUntil)
	if errors.As(err, &pqerr) && pqerr.Code == uniqueViolation {
		err = ErrAlreadyExists
	}
	if err != nil {
		return nil, err
	}

	return created, nil
}

func (f *Feeds) ListReady(readyAfter time.Time) ([]Feed, error) {
	stmt := `SELECT id, link, not_until FROM feeds WHERE not_until <= $1`
	args := []any{readyAfter}

	rows, err := f.DB.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Feed

	for rows.Next() {
		var f Feed

		err := rows.Scan(&f.ID, &f.Link, &f.NotUntil)
		if err != nil {
			return nil, err
		}

		list = append(list, f)
	}

	return list, nil
}

func (f *Feeds) GetByLink(link string) (*Feed, error) {
	stmt := `SELECT id, link, not_until FROM feeds WHERE link = $1`
	args := []any{link}

	fetched := new(Feed)

	err := f.DB.QueryRow(stmt, args...).Scan(&fetched.ID, &fetched.Link, &fetched.NotUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	return fetched, nil
}

func (f *Feeds) Update(feed *Feed) error {
	stmt := `UPDATE feeds SET link = $1, not_until = $2 WHERE id = $3`
	args := []any{feed.Link, feed.NotUntil, feed.ID}

	_, err := f.DB.Exec(stmt, args...)

	return err
}

func (f *Feeds) Delete(id int64) error {
	stmt := `DELETE FROM feeds WHERE id = $1`
	args := []any{id}

	_, err := f.DB.Exec(stmt, args...)

	return err
}
