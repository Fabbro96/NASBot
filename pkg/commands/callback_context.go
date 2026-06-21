package commands

import (
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// CallbackHandler is the interface for handling inline keyboard callbacks
type CallbackHandler interface {
	Handle(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool
}

// CallbackFunc is an adapter to allow the use of ordinary functions as callback handlers
type CallbackFunc func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool

// Handle calls f(ctx, bot, chatID, msgID, query, data)
func (f CallbackFunc) Handle(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
	return f(ctx, bot, chatID, msgID, query, data)
}

// CallbackRegistry manages callback handlers
type CallbackRegistry struct {
	exactMatches  map[string]CallbackHandler
	prefixMatches map[string]CallbackHandler
}

// NewCallbackRegistry creates a new registry
func NewCallbackRegistry() *CallbackRegistry {
	return &CallbackRegistry{
		exactMatches:  make(map[string]CallbackHandler),
		prefixMatches: make(map[string]CallbackHandler),
	}
}

// RegisterExact registers a handler for an exact callback data match
func (r *CallbackRegistry) RegisterExact(data string, handler CallbackHandler) {
	r.exactMatches[data] = handler
}

// RegisterPrefix registers a handler for a callback data prefix (e.g. "report_del_time_")
func (r *CallbackRegistry) RegisterPrefix(prefix string, handler CallbackHandler) {
	r.prefixMatches[prefix] = handler
}

// Execute looks up and executes the appropriate handler
func (r *CallbackRegistry) Execute(ctx *AppContext, bot BotAPI, query *tgbotapi.CallbackQuery) bool {
	if query == nil || query.Message == nil {
		return false
	}
	chatID := query.Message.Chat.ID
	msgID := query.Message.MessageID
	data := query.Data

	// 1. Try exact matches
	if handler, ok := r.exactMatches[data]; ok {
		return handler.Handle(ctx, bot, chatID, msgID, query, data)
	}

	// 2. Try prefix matches
	for prefix, handler := range r.prefixMatches {
		if strings.HasPrefix(data, prefix) {
			return handler.Handle(ctx, bot, chatID, msgID, query, data)
		}
	}

	return false
}
