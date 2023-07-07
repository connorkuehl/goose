# Goose

Discord bot that announces new items on RSS feeds 🪿 _honk!_

⚠️ Not appropriate for production use ⚠️

This application is not designed to scale horizontally:

* Discord bot sharding is not implemented
* Notification persisted state is stored in a table and not a queue,
so if there was another goose process running there's a big chance
they would race and potentially duplicate notifications.

## Usage

When goose is first added to the server, it will install the following
slash commands such that only the server administrator may invoke them.

The server administrator can add overrides in the server settings to
allow individual members or members with a given role access to the
slash commands.

| Command | Arguments | Description |
| - | - | - |
| `/subscribe` | channel, URL to feed, collection name | Subscribes the server to the feed at the given _URL_ identified by the given _collection name_. New items are announced on the supplied _channel_. |
| `/unsubscribe` | collection name | Unsubscribes the server from the feed identified by _collection name_. |
| `/test` | collection name | Emits the last published item on the feed identified by _collection name_. |

Outside of that, goose will automatically announce new items on feeds
that the server is subscribed to.

## Building

Prerequisites:

1. A Go toolchain

Simply run `go build`.

## Running

Prerequisites:

1. Valid Discord bot token with the "bot" and "application.commands"
scopes
1. PostgreSQL database
1. goose binary (see "Building")
1. [golang-migrate](https://github.com/golang-migrate/migrate)

Once you have a valid Postgres DSN, make sure you run the migrations
with [golang-migrate](https://github.com/golang-migrate/migrate) so that
all of the tables are set up:

```console
$ migrate -database=$GOOSE_POSTGRES_DSN -path ./migrations/postgres up
```

goose needs to be configured to use the Discord token and a valid
Postgres DSN. You can supply these at the command line:

```console
$ goose -discord-token <SECRET_BOT_DISCORD_TOKEN> \
        -postgres-dsn <SECRET_POSTGRES_DSN>
```

Or you can supply them as environment variables. These override any values
set at the command line:

```console
$ export GOOSE_DISCORD_TOKEN="SECRET_BOT_DISCORD_TOKEN"
$ export GOOSE_POSTGRES_DSN="postgres://goose:goose@localhost:55432/goose?sslmode=disable"
$ goose
```

