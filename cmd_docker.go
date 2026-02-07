package main

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type DockerMenuCmd struct{}

func (c *DockerMenuCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendDockerMenu(ctx, bot, msg.Chat.ID)
}
func (c *DockerMenuCmd) Description() string { return "Show Docker containers menu" }

type DockerStatsCmd struct{}

func (c *DockerStatsCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendWithKeyboard(ctx, bot, msg.Chat.ID, getDockerStatsText(ctx))
}
func (c *DockerStatsCmd) Description() string { return "Show Docker resource usage" }

type ContainerCmd struct{}

func (c *ContainerCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	handleContainerCommand(ctx, bot, msg.Chat.ID, args)
}
func (c *ContainerCmd) Description() string { return "Manage a specific container" }

type KillCmd struct{}

func (c *KillCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	handleKillCommand(ctx, bot, msg.Chat.ID, args)
}
func (c *KillCmd) Description() string { return "Kill a container" }

type RestartDockerCmd struct{}

func (c *RestartDockerCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	askDockerRestartConfirmation(ctx, bot, msg.Chat.ID)
}
func (c *RestartDockerCmd) Description() string { return "Restart Docker service" }
