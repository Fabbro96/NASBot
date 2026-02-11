package main

import (
	"encoding/json"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type ConfigCmd struct{}

func (c *ConfigCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendMarkdown(bot, msg.Chat.ID, getConfigText(ctx))
}
func (c *ConfigCmd) Description() string { return "Show current configuration" }

type ConfigJSONCmd struct{}

func (c *ConfigJSONCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	jsonText, err := getConfigJSONSafe()
	if err != nil {
		sendMarkdown(bot, msg.Chat.ID, fmt.Sprintf(ctx.Tr("configset_error"), err))
		return
	}
	sendMarkdown(bot, msg.Chat.ID, fmt.Sprintf("%s\n```json\n%s\n```", ctx.Tr("configjson_title"), jsonText))
}
func (c *ConfigJSONCmd) Description() string { return "Show configuration in JSON format" }

type ConfigSetCmd struct{}

func (c *ConfigSetCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	if strings.TrimSpace(args) == "" {
		sendMarkdown(bot, msg.Chat.ID, ctx.Tr("configset_usage"))
		return
	}
	var patch map[string]interface{}
	if err := json.Unmarshal([]byte(args), &patch); err != nil {
		sendMarkdown(bot, msg.Chat.ID, fmt.Sprintf(ctx.Tr("configset_error"), err))
		return
	}
	result, err := applyConfigPatch(patch)
	if err != nil {
		sendMarkdown(bot, msg.Chat.ID, fmt.Sprintf(ctx.Tr("configset_error"), err))
		return
	}
	response := ctx.Tr("configset_success")
	if len(result.Ignored) > 0 {
		response += fmt.Sprintf("\n"+ctx.Tr("configset_ignored"), strings.Join(result.Ignored, ", "))
	}
	if len(result.Corrected) > 0 {
		response += fmt.Sprintf("\n"+ctx.Tr("configset_corrected"), strings.Join(result.Corrected, ", "))
	}
	sendMarkdown(bot, msg.Chat.ID, response)
}
func (c *ConfigSetCmd) Description() string { return "Update configuration via JSON patch" }

type LanguageCmd struct{}

func (c *LanguageCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendLanguageSelection(ctx, bot, msg.Chat.ID)
}
func (c *LanguageCmd) Description() string { return "Change bot language" }

type SettingsCmd struct{}

func (c *SettingsCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendSettingsMenu(ctx, bot, msg.Chat.ID)
}
func (c *SettingsCmd) Description() string { return "Open settings menu" }
