package main

import (
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
)

type Notification struct {
	SubscriptionID int64
	ServerID       string
	ChannelID      string
	CollectionName string
	ArticleID      int64
	Title          string
	Link           string
	PubDate        time.Time
}

type Subscription struct {
	ID             int64
	FeedID         int64
	ServerID       string
	ChannelID      string
	CollectionName string
	LastPubDate    time.Time
}

type Subscriptions struct {
	db *sql.DB
}

func (s *Subscriptions) Create(feedID int64, serverID, channelID, collection string, lastPubDate time.Time) (*Subscription, error) {
	stmt := `INSERT INTO subscriptions (feed_id, server_id, channel_id, collection_name, last_pub_date) VALUES ($1, $2, $3, $4, $5) RETURNING id, feed_id, server_id, channel_id, collection_name, last_pub_date`
	args := []any{feedID, serverID, channelID, collection, lastPubDate}

	var sub Subscription
	var pqerr *pq.Error

	err := s.db.QueryRow(stmt, args...).Scan(&sub.ID, &sub.FeedID, &sub.ServerID, &sub.ChannelID, &sub.CollectionName, &sub.LastPubDate)
	if errors.As(err, &pqerr) && pqerr.Code == uniqueViolation {
		return nil, ErrAlreadyExists
	}
	if err != nil {
		return nil, err
	}

	return &sub, nil
}

func (s *Subscriptions) UpdateLastPubDate(id int64, lastPubDate time.Time) error {
	stmt := `UPDATE subscriptions SET last_pub_date = $2 WHERE id = $1`
	args := []any{id, lastPubDate}

	_, err := s.db.Exec(stmt, args...)

	return err
}

func (s *Subscriptions) GetByCollectionName(serverID, collectionName string) (*Subscription, error) {
	stmt := `SELECT id, feed_id, server_id, channel_id, collection_name, last_pub_date FROM subscriptions WHERE server_id = $1 AND collection_name = $2`
	args := []any{serverID, collectionName}

	var sub Subscription

	err := s.db.QueryRow(stmt, args...).Scan(&sub.ID, &sub.FeedID, &sub.ServerID, &sub.ChannelID, &sub.CollectionName, &sub.LastPubDate)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	return &sub, nil
}

func (s *Subscriptions) GetCollectionNames(serverID string) ([]string, error) {
	stmt := `SELECT collection_name FROM subscriptions WHERE server_id = $1`
	args := []any{serverID}

	rows, err := s.db.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var collections []string
	for rows.Next() {
		var collection string
		if err := rows.Scan(&collection); err != nil {
			return nil, err
		}
		collections = append(collections, collection)
	}

	return collections, nil
}

func (s *Subscriptions) Delete(id int64) error {
	stmt := `DELETE FROM subscriptions WHERE id = $1`
	args := []any{id}

	_, err := s.db.Exec(stmt, args...)

	return err
}

func (s *Subscriptions) PendingNotifications() ([]Notification, error) {
	stmt := `SELECT
			subscriptions.id,
			subscriptions.server_id,
			subscriptions.channel_id,
			subscriptions.collection_name,
			articles.id,
			articles.title,
			articles.link,
			articles.pub_date
		FROM subscriptions
		INNER JOIN articles ON subscriptions.feed_id=articles.feed_id
		WHERE articles.pub_date > subscriptions.last_pub_date
		ORDER BY articles.pub_date ASC`

	var notifications []Notification

	rows, err := s.db.Query(stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var n Notification
		err := rows.Scan(&n.SubscriptionID, &n.ServerID, &n.ChannelID, &n.CollectionName, &n.ArticleID, &n.Title, &n.Link, &n.PubDate)
		if err != nil {
			return nil, err
		}

		notifications = append(notifications, n)
	}

	return notifications, nil
}
