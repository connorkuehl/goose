package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/mmcdole/gofeed"
	"golang.org/x/exp/slog"
	"golang.org/x/time/rate"
)

const (
	defaultCache = int64(21600)
)

type Bot struct {
	articles        *Articles
	feeds           *Feeds
	subscriptions   *Subscriptions
	autocompletions *AutoCompletions

	rateLimiter *rate.Limiter
	session     *discordgo.Session

	httpClient *http.Client
}

func (b *Bot) AutocompleteCollectionName(s *discordgo.Session, i *discordgo.Interaction, option *discordgo.ApplicationCommandInteractionDataOption) {
	value := option.StringValue()

	logger := slog.With(
		slog.String("interaction_id", i.ID),
		slog.String("guild_id", i.GuildID),
		slog.String("channel_id", i.ChannelID),
		slog.String("input", value),
	)

	suggestions, err := b.autocompletions.CollectionNames(i.GuildID, value)
	if err != nil {
		logger.With(slog.Any("err", err)).Error("generate autocompletions")
		return
	}

	var choices []*discordgo.ApplicationCommandOptionChoice
	for _, suggestion := range suggestions {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  suggestion,
			Value: suggestion,
		})
	}

	err = s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
	if err != nil {
		logger.With(slog.Any("err", err)).Error("submit autocompletions")
		return
	}
}

func (b *Bot) Subscribe(s *discordgo.Session, i *discordgo.Interaction) {
	opts := optionsToMap(i.ApplicationCommandData().Options)
	feed := opts[optionFeed].StringValue()

	logger := slog.With(
		slog.String("interaction_id", i.ID),
		slog.String("guild_id", i.GuildID),
		slog.String("channel_id", i.ChannelID),
		slog.String("feed", feed),
	)

	link, err := url.Parse(feed)
	if err != nil || !strings.Contains(link.Scheme, "http") {
		err := b.respondToInteraction(s, i, `🪿 cOnFuSeD hOnK! Is that a valid URL?`)
		if err != nil {
			logger.With(slog.Any("err", err)).Error("respond to interaction")
		}
		return
	}

	channel := opts[optionChannel].ChannelValue(s)
	collection := opts[optionCollectionName].StringValue()

	logger = logger.With(
		slog.String("announce_channel_id", channel.ID),
		slog.String("collection_name", collection),
	)

	var httpErr *ErrHTTP

	respond := func(msg string) {
		if err := b.respondToInteraction(s, i, msg); err != nil {
			logger.With(slog.Any("err", err)).Error("respond to interaction")
			return
		}
	}

	err = b.subscribe(link, i.GuildID, channel.ID, collection)
	switch {
	case err == nil:
		response := fmt.Sprintf("🪿 Affirmative HONK! I'll send new items in the %q collection to %s.", collection, channel.Mention())
		respond(response)
	case errors.Is(err, ErrAlreadyExists):
		respond(`🪿 Smug HONK! You're already subscribed to that feed.`)
	case errors.Is(err, ErrNotRSSFeed):
		respond(`🪿 cOnFuSeD hOnK! There doesn't seem to be a valid RSS feed at that URL.`)
	case errors.As(err, &httpErr):
		switch {
		case httpErr.StatusCode == http.StatusUnauthorized:
			respond(`🪿 rebuked honk. The website requires authorization to view that page.`)
		case httpErr.StatusCode == http.StatusForbidden:
			respond(`🪿 rebuked honk. The website said viewing that resource is forbidden.`)
		case httpErr.StatusCode == http.StatusNotFound:
			respond(`🪿 lost honk. The website said there's nothing to be found at that URL.`)
		case httpErr.StatusCode >= 500:
			respond(`🪿 Advisory honk: that website seems to be having issues, try adding this again later.`)
		default:
			logger.Error("Unexpected HTTP error", slog.Int("http_status_code", httpErr.StatusCode))
			respond(`🪿 sad honk. I couldn't fetch that feed but it's my fault, so this could be a bug.`)
		}
	default:
		logger.With(slog.Any("err", err)).Error("internal error")
		b.respondInternalError(s, i)
	}
}

func (b *Bot) subscribe(link *url.URL, serverID, channelID, collection string) error {
	now := time.Now().UTC()

	feed, err := b.feeds.GetByLink(link.String())
	if errors.Is(err, ErrNotFound) {
		req, err := http.NewRequest(http.MethodGet, link.String(), strings.NewReader(""))
		if err != nil {
			return fmt.Errorf("http new request: %w", err)
		}

		rsp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("http get feed: %w", err)
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
			return fmt.Errorf("create feed: %w", err)
		}

		err = b.refreshFeed(feed, feedContents, time.Time{})
		if err != nil {
			return fmt.Errorf("refresh feed: %w", err)
		}
	}

	_, err = b.subscriptions.Create(feed.ID, serverID, channelID, collection, now)
	if err != nil && !errors.Is(err, ErrAlreadyExists) {
		return fmt.Errorf("create subscription: %w", err)
	}

	return nil
}

func (b *Bot) Unsubscribe(s *discordgo.Session, i *discordgo.Interaction) {
	opts := optionsToMap(i.ApplicationCommandData().Options)
	collection := opts[optionCollectionName].StringValue()

	logger := slog.With(
		slog.String("interaction_id", i.ID),
		slog.String("guild_id", i.GuildID),
		slog.String("channel_id", i.ChannelID),
		slog.String("collection_name", collection),
	)

	err := b.unsubscribe(i.GuildID, collection)
	if errors.Is(err, ErrNotFound) {
		response := fmt.Sprintf("🪿 lost honk. I couldn't find a subscription with the collection name %q", collection)
		err := b.respondToInteraction(s, i, response)
		if err != nil {
			logger.With(slog.Any("err", err)).Error("respond to interaction")
		}
		return
	}
	if err != nil {
		logger.With(slog.Any("err", err)).Error("unsubscribe")
		b.respondInternalError(s, i)
		return
	}

	response := fmt.Sprintf("🪿 Affirmative HONK! I removed the subscription to %q", collection)
	err = b.respondToInteraction(s, i, response)
	if err != nil {
		logger.With(slog.Any("err", err)).Error("respond to interaction")
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

	logger := slog.With(
		slog.String("interaction_id", i.ID),
		slog.String("guild_id", i.GuildID),
		slog.String("channel_id", i.ChannelID),
		slog.String("collection_name", collection),
	)

	link, err := b.test(i.GuildID, collection)
	switch {
	case err == nil:
		err = b.rateLimiter.Wait(context.Background())
		if err != nil {
			logger.With(slog.Any("err", err)).Warn("wait for rate limit")
			return
		}

		message := fmt.Sprintf("🪿 TEST HONK! Here's the latest item from the %q collection: %s", collection, link)
		err := b.respondToInteraction(s, i, message)
		if err != nil {
			logger.With(slog.Any("err", err)).Error("respond to interaction")
			return
		}
	case errors.Is(err, ErrNotFound):
		message := "🪿 NEGATIVE HONK! Did not find a collection with that name."
		err := b.respondToInteraction(s, i, message)
		if err != nil {
			logger.With(slog.Any("err", err)).Error("respond to interaction")
			return
		}
	case errors.Is(err, ErrEmptyFeed):
		message := "🪿 sad honk... There are no items in that RSS feed."
		err := b.respondToInteraction(s, i, message)
		if err != nil {
			logger.With(slog.Any("err", err)).Error("respond to interaction")
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
		slog.With(slog.Any("err", err)).Error("fetch notifications")
		return err
	}

	if len(nots) == 0 {
		slog.Info("No pending notifications to send out")
		return nil
	}

	for _, n := range nots {
		logger := slog.With(
			slog.Int64("subscription_id", n.SubscriptionID),
			slog.Int64("article_id", n.ArticleID),
			slog.String("guild_id", n.ServerID),
			slog.String("channel_id", n.ChannelID),
			slog.String("collection_name", n.CollectionName),
		)

		err := b.rateLimiter.Wait(ctx)
		if err != nil {
			return err
		}

		message := fmt.Sprintf("🪿 HONK! New item from collection %q: %s", n.CollectionName, n.Link)
		_, err = b.session.ChannelMessageSend(n.ChannelID, message)
		if err != nil {
			logger.With(slog.Any("err", err)).Error("send message to channel")
			continue
		}

		err = b.subscriptions.UpdateLastPubDate(n.SubscriptionID, n.PubDate)
		if err != nil {
			logger.With(slog.Any("err", err)).Error("update last pub date")
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
		slog.Info("no eligible feeds to refresh")
		return nil
	}

	slog.With(slog.Int("num_feeds", len(feeds))).Info("Refreshing eligible feeds")

	for _, feed := range feeds {
		logger := slog.With(
			slog.String("request_url", feed.Link),
			slog.Int64("feed_id", feed.ID),
		)

		req, err := http.NewRequest(http.MethodGet, feed.Link, strings.NewReader(""))
		if err != nil {
			logger.With(slog.Any("err", err)).Error("form GET")
			continue
		}

		rsp, err := b.httpClient.Do(req)
		if err != nil {
			logger.With(slog.Any("err", err)).Error("HTTP GET")
			continue
		}
		defer rsp.Body.Close()

		notUntil := calculateNotUntil(rsp, now)

		feed.NotUntil = notUntil
		err = b.feeds.Update(&feed)
		if err != nil {
			logger.With(slog.Any("err", err)).Error("update not until")
			continue
		}

		feedContents, err := gofeed.NewParser().Parse(rsp.Body)
		if err != nil {
			logger.With(slog.Any("err", err)).Error("parse feed")
			continue
		}

		latestPub := time.Time{}
		if article, err := b.articles.Latest(feed.ID); err == nil {
			latestPub = article.Published
		} else if !errors.Is(err, ErrNotFound) {
			logger.With(slog.Any("err", err)).Error("get latest article")
		}

		err = b.refreshFeed(&feed, feedContents, latestPub)
		if err != nil {
			logger.With(slog.Any("err", err)).Error("refresh feed")
			continue
		}
	}

	return nil
}

func (b *Bot) refreshFeed(feed *Feed, feedContents *gofeed.Feed, since time.Time) error {
	logger := slog.With(
		slog.String("request_url", feed.Link),
		slog.Int64("feed_id", feed.ID),
	)

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
			logger.With(slog.Any("err", err)).Warn("parse URL")
			continue
		}

		article, err := b.articles.Create(feed.ID, item.Title, u, item.PublishedParsed.UTC())
		if errors.Is(err, ErrAlreadyExists) {
			continue
		}
		if err != nil {
			logger.With(slog.Any("err", err)).Error("add new article")
			continue
		}

		logger.With(slog.Int64("article_id", article.ID)).Info("Added new article")
	}

	return nil
}

func (b *Bot) respondInternalError(s *discordgo.Session, i *discordgo.Interaction) {
	logger := slog.With(
		slog.String("interaction_id", i.ID),
		slog.String("guild_id", i.GuildID),
		slog.String("channel_id", i.ChannelID),
	)
	err := b.respondToInteraction(s, i, `🪿 ashamed honk. I ran into an issue processing this request. I have failed you. This might be a bug.`)
	if err != nil {
		logger.With(slog.Any("err", err)).Error("respond with internal error")
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
		case "s-maxage":
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
