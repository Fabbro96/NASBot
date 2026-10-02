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

// CallbackRegistry manages callback handlers.
//
// The registry is built once at bootstrap and only read afterwards, so it needs
// no lock of its own: concurrent Execute calls race on nothing.
type CallbackRegistry struct {
	exactMatches  map[string]CallbackHandler
	prefixMatches map[string]CallbackHandler
	// fallback is the catch-all handler, evaluated only when neither an exact
	// match nor any prefix matched. It is kept out of prefixMatches on purpose:
	// an empty prefix is true for every payload, so inside prefixMatches it wins
	// (or loses) at random, because Go randomizes map iteration order.
	fallback CallbackHandler
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
//
// An empty prefix is not a prefix match: it is registered as the fallback, so
// it can only run after every other handler has declined the payload.
func (r *CallbackRegistry) RegisterPrefix(prefix string, handler CallbackHandler) {
	if prefix == "" {
		r.fallback = handler
		return
	}
	r.prefixMatches[prefix] = handler
}

// RegisterFallback registers the catch-all handler, evaluated only after exact
// and prefix matches have all declined.
func (r *CallbackRegistry) RegisterFallback(handler CallbackHandler) {
	r.fallback = handler
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

	// 2. Try prefix matches, most specific first: among the prefixes that match
	//    the payload the longest one wins. The result is deterministic even
	//    though the map iteration order is not, because a payload of length n has
	//    exactly one prefix of each length.
	var best CallbackHandler
	bestLen := -1
	for prefix, handler := range r.prefixMatches {
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(data, prefix) && len(prefix) > bestLen {
			best, bestLen = handler, len(prefix)
		}
	}
	if best != nil {
		return best.Handle(ctx, bot, chatID, msgID, query, data)
	}

	// 3. Last resort: the catch-all.
	if r.fallback != nil {
		return r.fallback.Handle(ctx, bot, chatID, msgID, query, data)
	}

	return false
}
