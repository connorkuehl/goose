package main

import (
	"database/sql"
	"errors"
	"net/url"
	"time"

	"github.com/lib/pq"
)

type Article struct {
	ID        int64
	FeedID    int64
	Title     string
	Link      string
	Published time.Time
}

type Articles struct {
	db *sql.DB
}

func (a *Articles) Create(feedID int64, title string, link *url.URL, published time.Time) (*Article, error) {
	stmt := `INSERT INTO articles (feed_id, title, link, pub_date) VALUES ($1, $2, $3, $4) RETURNING id, feed_id, title, link, pub_date`
	args := []any{feedID, title, link.String(), published}

	art := &Article{}
	var pqerr *pq.Error

	err := a.db.QueryRow(stmt, args...).Scan(&art.ID, &art.FeedID, &art.Title, &art.Link, &art.Published)
	if errors.As(err, &pqerr) && pqerr.Code == uniqueViolation {
		return nil, ErrAlreadyExists
	}
	if err != nil {
		return nil, err
	}

	return art, nil
}

func (a *Articles) Latest(feedID int64) (*Article, error) {
	stmt := `SELECT id, feed_id, title, link, pub_date FROM articles WHERE feed_id = $1 ORDER BY pub_date DESC`
	args := []any{feedID}

	art := &Article{}
	err := a.db.QueryRow(stmt, args...).Scan(&art.ID, &art.FeedID, &art.Title, &art.Link, &art.Published)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	return art, nil
}
