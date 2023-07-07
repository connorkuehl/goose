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

