package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
	_ "github.com/lib/pq"
	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
	"golang.org/x/exp/slog"
	"golang.org/x/time/rate"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if isatty.IsTerminal(os.Stdout.Fd()) {
		slog.SetDefault(slog.New(tint.NewHandler(os.Stdout, &tint.Options{
			TimeFormat: time.Kitchen,
		})))
	} else {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	}

	if err := run(ctx); err != nil {
		slog.Error("Exiting", "err", err)
	}
}

func run(ctx context.Context) error {
	var (
		discordToken              string
		postgresDSN               string
		crawlerDelayIntervalSecs  int
		announceDelayIntervalSecs int
	)

	flag.StringVar(&discordToken, "discord-token", "", "Discord Bot token")
	flag.StringVar(&postgresDSN, "postgres-dsn", "", "PostgreSQL DSN")
	flag.IntVar(&crawlerDelayIntervalSecs, "crawler-interval-secs", 3600, "How long to wait (in seconds) before checking RSS feeds")
	flag.IntVar(&announceDelayIntervalSecs, "announce-interval-secs", 300, "How long to wait (in seconds) before checking for new items to announce")
	flag.Parse()

	discordToken = func(defaultValue string) string {
		if value, ok := os.LookupEnv("GOOSE_DISCORD_TOKEN"); ok {
			return value
		}
		return defaultValue
	}(discordToken)

	postgresDSN = func(defaultValue string) string {
		if value, ok := os.LookupEnv("GOOSE_POSTGRES_DSN"); ok {
			return value
		}
		return defaultValue
	}(postgresDSN)

	crawlerDelayIntervalSecs = func(defaultValue int) int {
		if strvalue, ok := os.LookupEnv("GOOSE_CRAWLER_INTERVAL_SECS"); ok {
			if value, err := strconv.ParseInt(strvalue, 10, 64); err == nil {
				return int(value)
			}
		}
		return defaultValue
	}(crawlerDelayIntervalSecs)

	announceDelayIntervalSecs = func(defaultValue int) int {
		if strvalue, ok := os.LookupEnv("GOOSE_ANNOUNCE_INTERVAL_SECS"); ok {
			if value, err := strconv.ParseInt(strvalue, 10, 64); err == nil {
				return int(value)
			}
		}
		return defaultValue
	}(announceDelayIntervalSecs)

	if discordToken == "" {
		return errors.New("missing required Discord token")
	}

	if postgresDSN == "" {
		return errors.New("missing required PostgreSQL DSN")
	}

	db, err := sql.Open("postgres", postgresDSN)
	if err != nil {
		return err
	}
	defer db.Close()

	err = func() error {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return db.PingContext(ctx)
	}()
	if err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	slog.Info("Connected to database")

	articles := &Articles{
		db: db,
	}

	feeds := &Feeds{
		DB: db,
	}

	subscriptions := &Subscriptions{
		db: db,
	}

	session, err := discordgo.New("Bot " + discordToken)
	if err != nil {
		return err
	}
	defer session.Close()

	rateLimiter := rate.NewLimiter(rate.Every(time.Second), 1)

	bot := &Bot{
		articles:        articles,
		feeds:           feeds,
		subscriptions:   subscriptions,
		autocompletions: &AutoCompletions{subscriptions: subscriptions},
		rateLimiter:     rateLimiter,
		session:         session,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
	}

	session.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		data := i.ApplicationCommandData()

		if i.Type == discordgo.InteractionApplicationCommandAutocomplete {
			for _, option := range data.Options {
				if !option.Focused || option.Name != optionCollectionName {
					continue
				}

				switch data.Name {
				case commandUnsubscribe, commandTest:
					bot.AutocompleteCollectionName(s, i.Interaction, option)
					return
				default:
					return
				}
			}
		}

		switch data.Name {
		case commandSubscribe:
			bot.Subscribe(s, i.Interaction)
		case commandUnsubscribe:
			bot.Unsubscribe(s, i.Interaction)
		case commandTest:
			bot.Test(s, i.Interaction)
		}
	})

	err = session.Open()
	if err != nil {
		return err
	}

	for _, cmd := range commands {
		_, err := session.ApplicationCommandCreate(session.State.User.ID, "", cmd)
		if err != nil {
			return fmt.Errorf("register command: %w", err)
		}
	}

	slog.Info("Connected to Discord")

	updateTicker := time.NewTicker(time.Duration(announceDelayIntervalSecs) * time.Second)
	defer updateTicker.Stop()

	refreshTicker := time.NewTicker(time.Duration(crawlerDelayIntervalSecs) * time.Second)
	defer refreshTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-updateTicker.C:
			_ = bot.Update(ctx)
		case <-refreshTicker.C:
			_ = bot.RefreshFeeds(ctx)
		}
	}
}
