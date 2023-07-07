package main

import "github.com/bwmarrin/discordgo"

const (
	commandSubscribe   = "subscribe"
	commandUnsubscribe = "unsubscribe"
	commandTest        = "test"

	optionChannel        = "channel"
	optionFeed           = "feed"
	optionCollectionName = "collection"
)

var (
	dmPermission            = false
	memberPermissions int64 = 0

	commands = []*discordgo.ApplicationCommand{
		{
			Name:                     commandSubscribe,
			Description:              "Subscribe to an RSS feed",
			DMPermission:             &dmPermission,
			DefaultMemberPermissions: &memberPermissions,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        optionChannel,
					Description: "Channel where new items will be announced to",
					Type:        discordgo.ApplicationCommandOptionChannel,
					ChannelTypes: []discordgo.ChannelType{
						discordgo.ChannelTypeGuildText,
					},
					Required: true,
				},
				{
					Name:        optionFeed,
					Description: "URL to feed",
					Type:        discordgo.ApplicationCommandOptionString,
					Required:    true,
				},
				{
					Name:        optionCollectionName,
					Description: "Name for this feed",
					Type:        discordgo.ApplicationCommandOptionString,
					Required:    true,
				},
			},
		},
		{
			Name:                     commandUnsubscribe,
			Description:              "Unsubscribe from an RSS feed",
			DMPermission:             &dmPermission,
			DefaultMemberPermissions: &memberPermissions,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        optionCollectionName,
					Description: "Collection to unsubscribe from",
					Type:        discordgo.ApplicationCommandOptionString,
					Required:    true,
				},
			},
		},
		{
			Name:                     commandTest,
			Description:              "Test the connection to an RSS feed (it will show a random item in the collection)",
			DMPermission:             &dmPermission,
			DefaultMemberPermissions: &memberPermissions,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        optionCollectionName,
					Description: "Collection to test",
					Type:        discordgo.ApplicationCommandOptionString,
					Required:    true,
				},
			},
		},
	}
)
