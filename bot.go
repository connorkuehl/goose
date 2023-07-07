package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/mmcdole/gofeed"
	"golang.org/x/time/rate"
)

const (
	defaultCache = int64(21600)
)

type Bot struct {
	articles      *Articles
	feeds         *Feeds
	subscriptions *Subscriptions

	rateLimiter *rate.Limiter
	session     *discordgo.Session

	httpClient *http.Client
}

func (b *Bot) Subscribe(s *discordgo.Session, i *discordgo.Interaction) {
	opts := optionsToMap(i.ApplicationCommandData().Options)

	feed := opts[optionFeed]
	link, err := url.Parse(feed.StringValue())
	if err != nil || !strings.Contains(link.Scheme, "http") {
		err := b.respondToInteraction(s, i, `🪿 cOnFuSeD hOnK! Is that a valid URL?`)
		if err != nil {
			log.Printf("[ERROR] Failed to respond: %v", err)
		}
		return
	}

	channel := opts[optionChannel].ChannelValue(s)
	collection := opts[optionCollectionName].StringValue()

	var httpErr *ErrHTTP

	err = b.subscribe(link, i.GuildID, channel.ID, collection)
	switch {
	case err == nil:
		response := fmt.Sprintf("🪿 Affirmative HONK! I'll send new items in the %q collection to #%s", collection, channel.Name)
		b.respondToInteraction(s, i, response)
	case errors.Is(err, ErrAlreadyExists):
		b.respondToInteraction(s, i, `🪿 Smug HONK! You're already subscribed to that feed.`)
	case errors.Is(err, ErrNotRSSFeed):
		b.respondToInteraction(s, i, `🪿 cOnFuSeD hOnK! There doesn't seem to be a valid RSS feed at that URL.`)
	case errors.As(err, &httpErr):
		switch {
		case httpErr.StatusCode == http.StatusUnauthorized:
			b.respondToInteraction(s, i, `🪿 rebuked honk. The website requires authorization to view that page.`)
		case httpErr.StatusCode == http.StatusForbidden:
			b.respondToInteraction(s, i, `🪿 rebuked honk. The website said viewing that resource is forbidden.`)
		case httpErr.StatusCode == http.StatusNotFound:
			b.respondToInteraction(s, i, `🪿 lost honk. The website said there's nothing to be found at that URL.`)
		case httpErr.StatusCode >= 500:
			b.respondToInteraction(s, i, `🪿 Advisory honk: that website seems to be having issues, try adding this again later.`)
		default:
			b.respondToInteraction(s, i, `🪿 sad honk. I couldn't fetch that feed but it's my fault, so this could be a bug.`)
		}
	default:
		b.respondInternalError(s, i)
	}
}

func (b *Bot) subscribe(link *url.URL, serverID, channelID, collection string) error {
	now := time.Now().UTC()

	feed, err := b.feeds.GetByLink(link.String())
	if errors.Is(err, ErrNotFound) {
		req, err := http.NewRequest(http.MethodGet, link.String(), strings.NewReader(""))
		if err != nil {
			log.Printf("Form GET [%s]: %v", link.String(), err)
			return err
		}

		rsp, err := b.httpClient.Do(req)
		if err != nil {
			log.Printf("GET [%s]: %v", link.String(), err)
			return err
		}
		defer rsp.Body.Close()

		if rsp.StatusCode < 200 || rsp.StatusCode >= 300 {
			return &ErrHTTP{StatusCode: rsp.StatusCode}
		}

		feedContents, err := gofeed.NewParser().Parse(rsp.Body)
		if errors.Is(err, gofeed.ErrFeedTypeNotDetected) {
			return ErrNotRSSFeed
		}
		sort.Sort(feedContents)

		now := time.Now().UTC()
		notUntil := calculateNotUntil(rsp, now)

		feed, err = b.feeds.Create(link, notUntil)
		if err != nil {
			log.Printf("[ERROR] Create Feed: %v", err)
			return err
		}

		err = b.refreshFeed(feed, feedContents, time.Time{})
		if err != nil {
			log.Printf("[ERROR] Refresh Feed [%d]: %v", feed.ID, err)
		}
	}

	_, err = b.subscriptions.Create(feed.ID, serverID, channelID, collection, now)
	if err != nil && !errors.Is(err, ErrAlreadyExists) {
		log.Printf("[ERROR] subscriptions.Create: %v", err)
		return err
	}

	return nil
}

func (b *Bot) Unsubscribe(s *discordgo.Session, i *discordgo.Interaction) {
	opts := optionsToMap(i.ApplicationCommandData().Options)
	collection := opts[optionCollectionName].StringValue()

	err := b.unsubscribe(i.GuildID, collection)
	if errors.Is(err, ErrNotFound) {
		response := fmt.Sprintf("🪿 lost honk. I couldn't find a subscription with the collection name %q", collection)
		err := b.respondToInteraction(s, i, response)
		if err != nil {
			log.Printf("[ERROR] Responding to response failed: %v", err)
		}
	}
	if err != nil {
		log.Printf("[ERROR] unsubscribe: %v", err)
		b.respondInternalError(s, i)
		return
	}

	response := fmt.Sprintf("🪿 Affirmative HONK! I removed the subscription to %q", collection)
	err = b.respondToInteraction(s, i, response)
	if err != nil {
		log.Printf("[ERROR] Responding to response failed: %v", err)
		return
	}
}

func (b *Bot) unsubscribe(serverID, collectionName string) error {
	sub, err := b.subscriptions.GetByCollectionName(serverID, collectionName)
	if err != nil {
		return err
	}

	return b.subscriptions.Delete(sub.ID)
}

func (b *Bot) Test(s *discordgo.Session, i *discordgo.Interaction) {
	opts := optionsToMap(i.ApplicationCommandData().Options)
	collection := opts[optionCollectionName].StringValue()

	link, err := b.test(i.GuildID, collection)
	switch {
	case err == nil:
		err = b.rateLimiter.Wait(context.Background())

		message := fmt.Sprintf("🪿 TEST HONK! Here's the latest item from the %q collection: %s", collection, link)
		err := b.respondToInteraction(s, i, message)
		if err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
			return
		}
	case errors.Is(err, ErrNotFound):
		message := "🪿 NEGATIVE HONK! Did not find a collection with that name."
		err := b.respondToInteraction(s, i, message)
		if err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
			return
		}
	case errors.Is(err, ErrEmptyFeed):
		message := "🪿 sad honk... There are no items in that RSS feed."
		err := b.respondToInteraction(s, i, message)
		if err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
			return
		}
	default:
		b.respondInternalError(s, i)
	}
}

func (b *Bot) test(serverID, collectionName string) (string, error) {
	sub, err := b.subscriptions.GetByCollectionName(serverID, collectionName)
	if err != nil {
		return "", err
	}

	latest, err := b.articles.Latest(sub.FeedID)
	if errors.Is(err, ErrNotFound) {
		return "", ErrEmptyFeed
	}
	if err != nil {
		return "", err
	}

	return latest.Link, nil
}

func (b *Bot) Update(ctx context.Context) error {
	nots, err := b.subscriptions.PendingNotifications()
	if err != nil {
		log.Printf("[ERROR] subscriptions.PendingNotifications: %v", err)
		return err
	}

	if len(nots) == 0 {
		log.Printf("[INFO] No pending notifications to send out")
		return nil
	}

	for _, n := range nots {
		err := b.rateLimiter.Wait(ctx)
		if err != nil {
			return err
		}

		message := fmt.Sprintf("🪿 HONK! New item from collection %q: %s", n.CollectionName, n.Link)
		_, err = b.session.ChannelMessageSend(n.ChannelID, message)
		if err != nil {
			log.Printf("[ERROR] session.ChannelMessageSend: %v", err)
			continue
		}

		log.Printf("[INFO] Notified Subscription [%d] of new Article [%d]", n.SubscriptionID, n.ArticleID)

		err = b.subscriptions.UpdateLastPubDate(n.SubscriptionID, n.PubDate)
		if err != nil {
			log.Printf("[ERROR] Update pub_date for Subscription [%d]: %v", n.SubscriptionID, err)
			continue
		}
	}

	return nil
}

func (b *Bot) RefreshFeeds(ctx context.Context) error {
	now := time.Now().UTC()

	feeds, err := b.feeds.ListReady(now)
	if err != nil {
		return fmt.Errorf("feeds.ListReady: %w", err)
	}

	if len(feeds) == 0 {
		log.Printf("[INFO] No eligible feeds to refresh")
		return nil
	}

	log.Printf("[INFO] Refreshing %d eligible feeds", len(feeds))

	for _, feed := range feeds {
		req, err := http.NewRequest(http.MethodGet, feed.Link, strings.NewReader(""))
		if err != nil {
			log.Printf("[WARN] Form GET [%s]: %v", feed.Link)
			continue
		}

		rsp, err := b.httpClient.Do(req)
		if err != nil {
			log.Printf("[WARN] GET [%s]: %v", err)
			continue
		}
		defer rsp.Body.Close()

		notUntil := calculateNotUntil(rsp, now)

		feed.NotUntil = notUntil
		err = b.feeds.Update(&feed)
		if err != nil {
			log.Printf("[ERROR] Update not_until [%v] for Feed [%d]: %v", feed.NotUntil, feed.ID, err)
			continue
		}

		feedContents, err := gofeed.NewParser().Parse(rsp.Body)
		if err != nil {
			log.Printf("[ERROR] Parse Feed [%d] at %s: %v", feed.ID, feed.Link, err)
			continue
		}

		latestPub := time.Time{}
		if article, err := b.articles.Latest(feed.ID); err == nil {
			latestPub = article.Published
		} else if !errors.Is(err, ErrNotFound) {
			log.Printf("[ERROR] Get latest article for Feed [%d]: %v", feed.ID, err)
		} else {
			log.Printf("[ERROR] articles.Latest: %v", err)
			continue
		}

		err = b.refreshFeed(&feed, feedContents, latestPub)
		if err != nil {
			log.Printf("[ERROR] Refresh Feed [%d]: %v", feed.ID, err)
			continue
		}
	}

	return nil
}

func (b *Bot) refreshFeed(feed *Feed, feedContents *gofeed.Feed, since time.Time) error {
	sort.Sort(feedContents)

	for _, item := range feedContents.Items {
		if item.PublishedParsed == nil {
			continue
		}

		if item.PublishedParsed.UTC().Before(since.UTC()) {
			continue
		}

		u, err := url.Parse(item.Link)
		if err != nil {
			log.Printf("[WARN] Parse URL for item in Feed [%d]: %v", feed.ID, err)
			continue
		}

		article, err := b.articles.Create(feed.ID, item.Title, u, item.PublishedParsed.UTC())
		if errors.Is(err, ErrAlreadyExists) {
			continue
		}
		if err != nil {
			log.Printf("[ERROR] Add new Article for Feed [%d]: %v", feed.ID, err)
			continue
		}

		log.Printf("[INFO] Added Article [%d] for Feed [%d]", article.ID, feed.ID)
	}

	return nil
}

func (b *Bot) respondInternalError(s *discordgo.Session, i *discordgo.Interaction) {
	err := b.respondToInteraction(s, i, `🪿 ashamed honk. I ran into an issue processing this request. I have failed you. This might be a bug.`)
	if err != nil {
		log.Printf("Responding with internal error failed: %v", err)
	}
}

func (b *Bot) respondToInteraction(s *discordgo.Session, i *discordgo.Interaction, message string) error {
	return s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
		},
	})
}

func optionsToMap(opts []*discordgo.ApplicationCommandInteractionDataOption) map[string]*discordgo.ApplicationCommandInteractionDataOption {
	options := make(map[string]*discordgo.ApplicationCommandInteractionDataOption, len(opts))
	for _, opt := range opts {
		options[opt.Name] = opt
	}
	return options
}

func calculateNotUntil(r *http.Response, now time.Time) time.Time {
	min := func(a, b int64) int64 {
		if a > b {
			return b
		}
		return a
	}

	parseInt64OrDefault := func(s string, defaultValue int64) int64 {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i
		}
		return defaultValue
	}

	var maxAge, sMaxAge int64

	cacheControl := r.Header.Values("Cache-Control")
	for _, value := range cacheControl {
		parts := strings.Split(value, "=")
		if len(parts) != 2 {
			continue
		}

		k, v := parts[0], parts[1]
		switch strings.ToLower(k) {
		case "max-age":
			maxAge = parseInt64OrDefault(v, defaultCache)
		case "s-max-age":
			sMaxAge = parseInt64OrDefault(v, defaultCache)
		default:
			// no-op
		}
	}

	notUntilSeconds := defaultCache
	if maxAge > 0 {
		notUntilSeconds = min(notUntilSeconds, maxAge)
	} else if sMaxAge > 0 {
		notUntilSeconds = min(notUntilSeconds, sMaxAge)
	}

	notUntil := now.Add(time.Duration(notUntilSeconds) * time.Second)

	return notUntil
}
