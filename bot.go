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
	logger := slog.With(
		slog.String("request", "autocomplete"),
		slog.Group("discord",
			slog.String("interaction_id", i.ID),
			slog.String("guild_id", i.GuildID),
			slog.String("channel_id", i.ChannelID),
		),
	)

	logger.Info("Incoming Autocomplete request")

	value := option.StringValue()
	suggestions, err := b.autocompletions.CollectionNames(i.GuildID, value)
	if err != nil {
		logger.Error("Generating autocompletions", slog.Any("err", err))
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
		logger.Error("Submitting autocompletions", slog.Any("err", err))
		return
	}
}

func (b *Bot) Subscribe(s *discordgo.Session, i *discordgo.Interaction) {
	logger := slog.With(
		slog.String("request", "subscribe"),
		slog.Group("discord",
			slog.String("interaction_id", i.ID),
			slog.String("guild_id", i.GuildID),
			slog.String("channel_id", i.ChannelID),
		),
	)

	opts := optionsToMap(i.ApplicationCommandData().Options)

	feed := opts[optionFeed]
	link, err := url.Parse(feed.StringValue())
	if err != nil || !strings.Contains(link.Scheme, "http") {
		err := b.respondToInteraction(s, i, `🪿 cOnFuSeD hOnK! Is that a valid URL?`)
		if err != nil {
			logger.Error("Respond to interaction", slog.Any("err", err))
		}
		return
	}

	channel := opts[optionChannel].ChannelValue(s)
	collection := opts[optionCollectionName].StringValue()

	logger = logger.With(slog.Group(
		"args",
		slog.String("channel", channel.ID),
		slog.String("collection", collection),
	))

	logger.Info("Incoming Subscribe request")

	var httpErr *ErrHTTP

	respond := func(msg string) {
		if err := b.respondToInteraction(s, i, msg); err != nil {
			logger.Error("Responding to interaction", slog.Any("err", err))
			return
		}
		logger.Info("Responded to Subscribe request")
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
		logger.Error("Internal error", slog.Any("err", err))
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
	logger := slog.With(
		slog.String("request", "unsubscribe"),
		slog.Group("discord",
			slog.String("interaction_id", i.ID),
			slog.String("guild_id", i.GuildID),
			slog.String("channel_id", i.ChannelID),
		),
	)

	opts := optionsToMap(i.ApplicationCommandData().Options)
	collection := opts[optionCollectionName].StringValue()

	logger = logger.With(slog.Group("args",
		slog.String("collection", collection),
	))

	logger.Info("Incoming Unsubscribe request")

	err := b.unsubscribe(i.GuildID, collection)
	if errors.Is(err, ErrNotFound) {
		response := fmt.Sprintf("🪿 lost honk. I couldn't find a subscription with the collection name %q", collection)
		err := b.respondToInteraction(s, i, response)
		if err != nil {
			logger.Error("Responding to interaction", slog.Any("err", err))
		}

		logger.Info("Responded to Unsubscribe request")
		return
	}
	if err != nil {
		logger.Error("Unsubscribe", slog.Any("err", err))
		b.respondInternalError(s, i)
		return
	}

	response := fmt.Sprintf("🪿 Affirmative HONK! I removed the subscription to %q", collection)
	err = b.respondToInteraction(s, i, response)
	if err != nil {
		logger.Error("Responding to interaction", slog.Any("err", err))
		return
	}
	logger.Info("Responded to Unsubscribe request")
}

func (b *Bot) unsubscribe(serverID, collectionName string) error {
	sub, err := b.subscriptions.GetByCollectionName(serverID, collectionName)
	if err != nil {
		return err
	}

	return b.subscriptions.Delete(sub.ID)
}

func (b *Bot) Test(s *discordgo.Session, i *discordgo.Interaction) {
	logger := slog.With(
		slog.String("request", "test"),
		slog.Group("discord",
			slog.String("interaction_id", i.ID),
			slog.String("guild_id", i.GuildID),
			slog.String("channel_id", i.ChannelID),
		),
	)

	opts := optionsToMap(i.ApplicationCommandData().Options)
	collection := opts[optionCollectionName].StringValue()

	logger = logger.With(slog.Group("args",
		slog.String("collection", collection),
	))

	logger.Info("Incoming Test request")

	link, err := b.test(i.GuildID, collection)
	switch {
	case err == nil:
		err = b.rateLimiter.Wait(context.Background())
		if err != nil {
			logger.Warn("Waiting for rate limit", slog.Any("err", err))
			return
		}

		message := fmt.Sprintf("🪿 TEST HONK! Here's the latest item from the %q collection: %s", collection, link)
		err := b.respondToInteraction(s, i, message)
		if err != nil {
			logger.Error("Responding to interaction", slog.Any("err", err))
			return
		}
		logger.Info("Responded to Test")
	case errors.Is(err, ErrNotFound):
		message := "🪿 NEGATIVE HONK! Did not find a collection with that name."
		err := b.respondToInteraction(s, i, message)
		if err != nil {
			logger.Error("Responding to interaction", slog.Any("err", err))
			return
		}
		logger.Info("Responded to Test")
	case errors.Is(err, ErrEmptyFeed):
		message := "🪿 sad honk... There are no items in that RSS feed."
		err := b.respondToInteraction(s, i, message)
		if err != nil {
			logger.Error("Responding to interaction", slog.Any("err", err))
			return
		}
		logger.Info("Responded to Test")
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
	logger := slog.With(
		slog.String("action", "update"),
	)

	nots, err := b.subscriptions.PendingNotifications()
	if err != nil {
		logger.Error("Fetching notifications", slog.Any("err", err))
		return err
	}

	if len(nots) == 0 {
		logger.Info("No pending notifications to send out")
		return nil
	}

	for _, n := range nots {
		logger = logger.With(
			slog.Group("subscription",
				slog.Int64("id", n.SubscriptionID),
				slog.String("server_id", n.ServerID),
				slog.String("channel_id", n.ChannelID),
				slog.String("collection", n.CollectionName),
			),
			slog.Group("article",
				slog.Int("id", int(n.ArticleID)),
			),
		)

		err := b.rateLimiter.Wait(ctx)
		if err != nil {
			return err
		}

		message := fmt.Sprintf("🪿 HONK! New item from collection %q: %s", n.CollectionName, n.Link)
		_, err = b.session.ChannelMessageSend(n.ChannelID, message)
		if err != nil {
			logger.Error("Sending message to channel", slog.Any("err", err))
			continue
		}

		logger.Info("Notification sent")

		err = b.subscriptions.UpdateLastPubDate(n.SubscriptionID, n.PubDate)
		if err != nil {
			logger.Error("Updating last publish date", slog.Any("err", err))
			continue
		}
	}

	return nil
}

func (b *Bot) RefreshFeeds(ctx context.Context) error {
	logger := slog.With(
		slog.String("action", "refresh"),
	)

	now := time.Now().UTC()

	feeds, err := b.feeds.ListReady(now)
	if err != nil {
		return fmt.Errorf("feeds.ListReady: %w", err)
	}

	if len(feeds) == 0 {
		logger.Info("No eligible feeds to refresh")
		return nil
	}

	logger.With("num_feeds", len(feeds)).Info("Refreshing eligible feeds")

	for _, feed := range feeds {
		logger = logger.With(
			slog.String("request_url", feed.Link),
			slog.Group("feed",
				slog.Int64("id", feed.ID)),
		)

		req, err := http.NewRequest(http.MethodGet, feed.Link, strings.NewReader(""))
		if err != nil {
			logger.Error("http.NewRequest", slog.Any("err", err))
			continue
		}

		rsp, err := b.httpClient.Do(req)
		if err != nil {
			logger.Error("Fetching remote feed", slog.Any("err", err))
			continue
		}
		defer rsp.Body.Close()

		notUntil := calculateNotUntil(rsp, now)

		feed.NotUntil = notUntil
		err = b.feeds.Update(&feed)
		if err != nil {
			logger.With(
				slog.Time("not_until", feed.NotUntil),
			).Error("Updating not_until for feed", slog.Any("err", err))
			continue
		}

		feedContents, err := gofeed.NewParser().Parse(rsp.Body)
		if err != nil {
			logger.Error("Parsing feed", slog.Any("err", err))
			continue
		}

		latestPub := time.Time{}
		if article, err := b.articles.Latest(feed.ID); err == nil {
			latestPub = article.Published
		} else if !errors.Is(err, ErrNotFound) {
			logger.Error("Getting latest article for feed", slog.Any("err", err))
		}

		err = b.refreshFeed(&feed, feedContents, latestPub)
		if err != nil {
			logger.Info("Refreshing feed", slog.Any("err", err))
			continue
		}
	}

	return nil
}

func (b *Bot) refreshFeed(feed *Feed, feedContents *gofeed.Feed, since time.Time) error {
	logger := slog.With(
		slog.String("action", "refresh"),
		slog.Group("feed",
			slog.Int64("id", feed.ID),
			slog.String("link", feed.Link),
		),
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
			logger.Warn("Parsing URL", slog.Any("err", err))
			continue
		}

		article, err := b.articles.Create(feed.ID, item.Title, u, item.PublishedParsed.UTC())
		if errors.Is(err, ErrAlreadyExists) {
			continue
		}
		if err != nil {
			logger.Error("Adding new article for feed", slog.Any("err", err))
			continue
		}

		logger.With(
			slog.Group("article",
				slog.Int64("id", article.ID),
			),
		).Info("Added article for feed")
	}

	return nil
}

func (b *Bot) respondInternalError(s *discordgo.Session, i *discordgo.Interaction) {
	logger := slog.With(
		slog.Group("discord",
			slog.String("interaction_id", i.ID),
			slog.String("guild_id", i.GuildID),
			slog.String("channel_id", i.ChannelID),
		))
	err := b.respondToInteraction(s, i, `🪿 ashamed honk. I ran into an issue processing this request. I have failed you. This might be a bug.`)
	if err != nil {
		logger.Error("Responding to interaction with internal error", slog.Any("err", err))
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
