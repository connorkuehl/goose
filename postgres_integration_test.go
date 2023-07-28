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

	t.Run("Subscriptions", func(t *testing.T) {
		feeds := &Feeds{DB: db}
		subscriptions := &Subscriptions{db: db}

		u, err := url.Parse("http://another.example.com?rss")
		if err != nil {
			t.Fatalf("url.Parse [%q]: %v", "http://another.example.com?rss", err)
		}

		feed1, err := feeds.Create(u, time.Date(5, 5, 5, 5, 5, 5, 5, time.UTC))
		if err != nil {
			t.Fatalf("feeds.Create: %v", err)
		}
		defer feeds.Delete(feed1.ID)

		sub1, err := subscriptions.Create(feed1.ID, "server1", "channel1", "collection1", time.Date(2, 2, 2, 2, 2, 2, 2, time.UTC))
		if err != nil {
			t.Fatalf("subscriptions.Create: %v", err)
		}
		defer subscriptions.Delete(sub1.ID)

		if sub1.FeedID != feed1.ID {
			t.Fatalf("want FeedID=%d, got FeedID=%d", feed1.ID, sub1.FeedID)
		}

		if sub1.ServerID != "server1" {
			t.Fatalf("want ServerID=%q, got ServerID=%q", "server1", sub1.ServerID)
		}

		if sub1.ChannelID != "channel1" {
			t.Fatalf("want ChannelID=%q, got ChannelID=%q", "channel1", sub1.ChannelID)
		}

		if sub1.CollectionName != "collection1" {
			t.Fatalf("want CollectionName=%q, got CollectionName=%q", "collection1", sub1.CollectionName)
		}

		_, err = subscriptions.Create(feed1.ID, "server1", "channel1", "collection1", time.Date(2, 2, 2, 2, 2, 2, 2, time.UTC))
		if !errors.Is(err, ErrAlreadyExists) {
			t.Fatalf("want err=%v, got err=%v when creating duplicate subscription", nil, err)
		}

		fetch1, err := subscriptions.GetByCollectionName("server1", "collection1")
		if err != nil {
			t.Fatalf("want err=%v, got err=%v when fetching subscription by collection name", nil, err)
		}

		if *fetch1 != *sub1 {
			t.Fatalf("want Subscription [%+v], got Subscription [%+v]", *sub1, *fetch1)
		}

		_, err = subscriptions.GetByCollectionName("server1", "does not exist")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("want err=%v, got err=%v when fetching non-existent subscription", nil, err)
		}

		err = subscriptions.Delete(sub1.ID)
		if err != nil {
			t.Fatalf("want err=<nil> got err=%v when deleting Subscription", err)
		}

		_, err = subscriptions.GetByCollectionName("server1", "collection1")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("want err=<nil>, got err=%v when fetching deleted subscription", err)
		}
	})

	t.Run("Articles", func(t *testing.T) {
		feeds := &Feeds{DB: db}
		articles := Articles{db: db}

		u, err := url.Parse("http://another.example.com?rss")
		if err != nil {
			t.Fatalf("url.Parse [%q]: %v", "http://another.example.com?rss", err)
		}

		feed1, err := feeds.Create(u, time.Date(5, 5, 5, 5, 5, 5, 5, time.UTC))
		if err != nil {
			t.Fatalf("feeds.Create: %v", err)
		}

		u1, err := url.Parse("http://another.example.com/article?id=1")
		if err != nil {
			t.Fatalf("url.Parse [%q]: %v", "http://another.example.com/article?id=1", err)
		}

		art1, err := articles.Create(feed1.ID, "The First Amazing Article", u1, time.Date(4, 4, 4, 4, 4, 4, 4, time.UTC))
		if err != nil {
			t.Fatalf("want err=<nil>, got err=%v when creating first article", err)
		}

		_, err = articles.Create(feed1.ID, "The First Amazing Article", u1, time.Date(4, 4, 4, 4, 4, 4, 4, time.UTC))
		if !errors.Is(err, ErrAlreadyExists) {
			t.Fatalf("want err=%v, got err=%v when creating duplicate article", err, ErrAlreadyExists)
		}

		latest, err := articles.Latest(feed1.ID)
		if err != nil {
			t.Fatalf("want err=<nil>, got err=%v when getting latest article for feed", err)
		}

		if *latest != *art1 {
			t.Fatalf("want latest article [%+v], got article [%+v]", *art1, *latest)
		}

		u2, err := url.Parse("http://another.example.com/article?id=12")
		if err != nil {
			t.Fatalf("url.Parse [%q]: %v", "http://another.example.com/article?id=12", err)
		}

		art2, err := articles.Create(feed1.ID, "The next best article", u2, time.Date(5, 5, 5, 5, 5, 5, 5, time.UTC))
		if err != nil {
			t.Fatalf("want err=<nil>, got err=%v when creating the second article", err)
		}

		latest, err = articles.Latest(feed1.ID)
		if err != nil {
			t.Fatalf("want err=<nil>, got err=%v when getting the latest article agains", err)
		}

		if *latest != *art2 {
			t.Fatalf("want latest Article [%+v], got Article [%+v]", *art2, *latest)
		}
	})
}
