//go:build integration
// +build integration

package main

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func TestIntegrationPostgres(t *testing.T) {
	dsn := os.Getenv("GOOSE_INTEGRATION_POSTGRES_DSN")

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("Open database [%q]: %v", dsn, err)
	}
	defer db.Close()

	err = db.Ping()
	if err != nil {
		t.Errorf("Ping database: %v", err)
		return
	}

	t.Run("Feeds", func(t *testing.T) {
		feeds := &Feeds{DB: db}

		// There shouldn't be any ready feeds
		ready, err := feeds.ListReady(time.Date(5000, 0, 0, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Errorf("ListReady: %v", err)
			return
		}

		if len(ready) != 0 {
			t.Errorf("There should be zero ready feeds in a brand-new DB [%v]", ready)
			return
		}

		link, err := url.Parse("http://example.com?rss")
		if err != nil {
			t.Errorf("Failed to parse test URL: %v", err)
			return
		}

		notUntil1 := time.Date(2023, 2, 2, 2, 2, 2, 2, time.UTC)

		// Create a well-known good feed for testing.

		feed1, err := feeds.Create(link, notUntil1)
		if err != nil {
			t.Errorf("Unexpected err when creating first feed: %v", err)
			return
		}

		if feed1.Link != "http://example.com?rss" {
			t.Errorf("Want Link=%q, got Link=%q", "http://example.com?rss", feed1.Link)
			return
		}

		// Test that we can't add a duplicate feed.
		_, err = feeds.Create(link, notUntil1)
		if !errors.Is(err, ErrAlreadyExists) {
			t.Errorf("Want err=%v, got err=%v", ErrAlreadyExists, err)
			return
		}

		// OK, now that the database has a feed, punch in a date that is *after*
		// the feed's NotUntil time with the expectation that it is returned in
		// the list of ready feeds.
		ready, err = feeds.ListReady(time.Date(2023, 3, 3, 3, 3, 3, 3, time.UTC))
		if err != nil {
			t.Errorf("ListReady after inserting one: %v", err)
			return
		}

		if len(ready) != 1 {
			t.Errorf("Expected slice of len=1, got len=%d", len(ready))
			return
		}

		if *feed1 != ready[0] {
			t.Errorf("Want feed [%+v], got [%+v]", *feed1, ready[0])
			return
		}

		// Test the negative case for fetching a feed that does not
		// exist.
		_, err = feeds.GetByLink("http://does-not-exist.test")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Want err=%v, got err=%v", ErrNotFound, err)
			return
		}

		// Now let's fetch the feed that was previously created.
		fetched1, err := feeds.GetByLink(feed1.Link)
		if err != nil {
			t.Errorf("Want err=%v, got err=%v when fetching pre-existing feed", nil, err)
			return
		}

		if *feed1 != *fetched1 {
			t.Errorf("Want feed [%+v], got feed [%+v]", *feed1, *fetched1)
			return
		}

		// And test that we can update it.
		updated := &Feed{
			ID:       feed1.ID,
			Link:     "http://a-brand-new-link.test",
			NotUntil: feed1.NotUntil,
		}

		err = feeds.Update(updated)
		if err != nil {
			t.Errorf("Want err=%v, got err=%v when updating feed", nil, err)
			return
		}

		// Fetch it from the database to confirm we get the updated values.

		fetchedUpdated, err := feeds.GetByLink(updated.Link)
		if err != nil {
			t.Errorf("Want err=%v, got err=%v when fetching updated feed", nil, err)
			return
		}

		if *fetchedUpdated != *updated {
			t.Errorf("Want updated feed [%+v], got [%+v]", *updated, *fetchedUpdated)
			return
		}

		// Assert that the updated feed is returned in the list of ready feeds.
		ready, err = feeds.ListReady(time.Date(2023, 3, 3, 3, 3, 3, 3, time.UTC))
		if err != nil {
			t.Errorf("Want err=%v, got err=%v when listing ready feeds", nil, err)
			return
		}

		if len(ready) != 1 {
			t.Errorf("Expected slice of len=1, got len=%d", len(ready))
			return
		}

		if *updated != ready[0] {
			t.Errorf("Want feed [%+v], got [%+v]", *feed1, ready[0])
			return
		}

		// Now let's test deleting the feeds.

		err = feeds.Delete(ready[0].ID)
		if err != nil {
			t.Errorf("Want err=%v, got err=%v when deleting the only feed", nil, err)
			return
		}

		// And assert that there are no more feeds because we've just
		// deleted the only feed that was added.

		ready, err = feeds.ListReady(time.Date(2023, 3, 3, 3, 3, 3, 3, time.UTC))
		if err != nil {
			t.Errorf("Want err=%v, got err=%v when listing ready feeds", nil, err)
			return
		}

		if len(ready) != 0 {
			t.Errorf("Expected slice of len=0, got len=%d", len(ready))
			return
		}
	})
}