package commands

import (
	"sync"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// recordingBot counts the Telegram calls a handler makes. Callbacks have no other
// observable effect: the routing decision is what is under test.
type recordingBot struct {
	mu   sync.Mutex
	sent []tgbotapi.Chattable
	reqs []tgbotapi.Chattable
}

func (b *recordingBot) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, c)
	return tgbotapi.Message{MessageID: len(b.sent), Chat: &tgbotapi.Chat{ID: 1}}, nil
}

func (b *recordingBot) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reqs = append(b.reqs, c)
	return &tgbotapi.APIResponse{}, nil
}

func (b *recordingBot) calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.sent) + len(b.reqs)
}

// callbackQuery builds the query handleCallback would receive for a button press.
func callbackQuery(id, data string) *tgbotapi.CallbackQuery {
	return &tgbotapi.CallbackQuery{
		ID:      id,
		Data:    data,
		From:    &tgbotapi.User{ID: 1},
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 4242}, MessageID: 77},
	}
}

// TestCallbackRegistryExactMatchWinsOverPrefix is the first routing rule.
//
// The defect it covers: with the order inverted, a payload that a prefix handler
// claims would never reach the handler registered for it exactly. In production
// that means the specific button stops doing what its label says while a broad
// prefix keeps running on every press.
func TestCallbackRegistryExactMatchWinsOverPrefix(t *testing.T) {
	r := NewCallbackRegistry()
	var exactRan, prefixRan bool
	r.RegisterPrefix("report_", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		prefixRan = true
		return true
	}))
	r.RegisterExact("report_del_time_0800", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		exactRan = true
		return true
	}))

	if !r.Execute(newTestAppContext(), &recordingBot{}, callbackQuery("1", "report_del_time_0800")) {
		t.Fatal("a registered handler must report the press as handled")
	}
	if !exactRan {
		t.Error("the exact handler did not run")
	}
	if prefixRan {
		t.Error("the prefix handler ran as well: the exact match must win")
	}
}

// TestCallbackRegistryLongestPrefixWins pins the specificity rule.
//
// The prefix map is iterated in random order, so "some prefix matched" is not
// enough: a shorter prefix winning would send `proc_kill_` to the `proc_manage_`
// handler, and the process manager would be handed a kill request.
func TestCallbackRegistryLongestPrefixWins(t *testing.T) {
	r := NewCallbackRegistry()
	var manageRan, killRan bool
	r.RegisterPrefix("proc_", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		manageRan = true
		return true
	}))
	r.RegisterPrefix("proc_manage_", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		manageRan = true
		return true
	}))
	r.RegisterPrefix("proc_kill_", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		killRan = true
		return true
	}))

	if !r.Execute(newTestAppContext(), &recordingBot{}, callbackQuery("1", "proc_kill_4242")) {
		t.Fatal("a prefix handler must report the press as handled")
	}
	if !killRan {
		t.Error("the most specific prefix did not run")
	}
	if manageRan {
		t.Error("a shorter prefix ran as well")
	}
}

// TestCallbackRegistryLongestPrefixIsDeterministic repeats the same lookup: the
// map iteration order is randomized per range, so a registry that picks the
// first matching prefix rather than the longest would answer differently on
// consecutive presses of the same button.
func TestCallbackRegistryLongestPrefixIsDeterministic(t *testing.T) {
	r := NewCallbackRegistry()
	var winners []string
	r.RegisterPrefix("adblock_", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		winners = append(winners, "adblock")
		return true
	}))
	r.RegisterPrefix("adblock_toggle_", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		winners = append(winners, "toggle")
		return true
	}))

	for i := 0; i < 50; i++ {
		r.Execute(newTestAppContext(), &recordingBot{}, callbackQuery("1", "adblock_toggle_192.168.1.1"))
	}
	for i, got := range winners {
		if got != "toggle" {
			t.Fatalf("run %d went to %q, want toggle: routing depends on map order", i, got)
		}
	}
}

// TestCallbackRegistryEmptyPrefixBecomesFallback is the subtlety the fallback
// comment describes: "" is a prefix of every payload, so registering it inside
// prefixMatches would make it win (or lose) at random against the real handlers.
//
// Defect covered: a version that stored it in prefixMatches would route half the
// button presses to the settings catch-all and half to the intended handler, so
// the bot behaves differently on every other press.
func TestCallbackRegistryEmptyPrefixBecomesFallback(t *testing.T) {
	r := NewCallbackRegistry()
	var fallbackRan, exactRan bool
	r.RegisterPrefix("", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		fallbackRan = true
		return true
	}))
	r.RegisterExact("show_status", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		exactRan = true
		return true
	}))

	r.Execute(newTestAppContext(), &recordingBot{}, callbackQuery("1", "show_status"))
	if !exactRan {
		t.Error("the exact handler did not run")
	}
	if fallbackRan {
		t.Error("an empty prefix shadowed an exact handler")
	}

	r.Execute(newTestAppContext(), &recordingBot{}, callbackQuery("2", "anything_else"))
	if !fallbackRan {
		t.Error("the empty prefix must act as the fallback for unmatched payloads")
	}
}

// TestCallbackRegistryFallbackRunsLast is the property RegisterFallback exists for.
//
// Settings owns a long tail of payloads (settings_*, report_*, thresh_*, prune_*,
// quiet_*, set_lang_*) and is a fallback, not a prefix: running it before the
// specific handlers would let the catch-all claim `report_del_time_0800` and the
// button would silently do the wrong thing.
func TestCallbackRegistryFallbackRunsLast(t *testing.T) {
	r := NewCallbackRegistry()
	var order []string
	r.RegisterFallback(CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		order = append(order, "fallback")
		return true
	}))
	r.RegisterExact("pre_confirm_reboot", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		order = append(order, "exact")
		return true
	}))
	r.RegisterPrefix("health_", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		order = append(order, "prefix")
		return true
	}))

	for _, payload := range []string{"pre_confirm_reboot", "health_cpu", "settings_quiet"} {
		order = order[:0]
		r.Execute(newTestAppContext(), &recordingBot{}, callbackQuery("1", payload))

		switch payload {
		case "pre_confirm_reboot":
			if len(order) != 1 || order[0] != "exact" {
				t.Errorf("%q ran %v, want [exact]", payload, order)
			}
		case "health_cpu":
			if len(order) != 1 || order[0] != "prefix" {
				t.Errorf("%q ran %v, want [prefix]", payload, order)
			}
		default:
			if len(order) != 1 || order[0] != "fallback" {
				t.Errorf("%q ran %v, want [fallback]", payload, order)
			}
		}
	}
}

// TestCallbackRegistryFallbackCanBeReplaced: the bootstrap registers it once, so
// a second registration must win instead of the first silently staying.
func TestCallbackRegistryFallbackCanBeReplaced(t *testing.T) {
	r := NewCallbackRegistry()
	var first, second bool
	r.RegisterFallback(CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		first = true
		return true
	}))
	r.RegisterFallback(CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		second = true
		return true
	}))

	r.Execute(newTestAppContext(), &recordingBot{}, callbackQuery("1", "unknown_payload"))

	if first {
		t.Error("the replaced fallback still ran")
	}
	if !second {
		t.Error("the replacement fallback did not run")
	}
}

// TestCallbackRegistryPassesQueryCoordinates: the handler has to be able to edit
// the message that was pressed and to answer in the right chat. A dispatch that
// lost msgID or chatID would make every button a no-op on a real chat.
func TestCallbackRegistryPassesQueryCoordinates(t *testing.T) {
	r := NewCallbackRegistry()
	var gotChat int64
	var gotMsgID int
	var gotData string
	r.RegisterExact("probe", CallbackFunc(func(_ *AppContext, _ BotAPI, chatID int64, msgID int, _ *tgbotapi.CallbackQuery, data string) bool {
		gotChat, gotMsgID, gotData = chatID, msgID, data
		return true
	}))

	r.Execute(newTestAppContext(), &recordingBot{}, callbackQuery("42", "probe"))

	if gotChat != 4242 {
		t.Errorf("chatID = %d, want 4242", gotChat)
	}
	if gotMsgID != 77 {
		t.Errorf("msgID = %d, want 77", gotMsgID)
	}
	if gotData != "probe" {
		t.Errorf("data = %q, want %q", gotData, "probe")
	}
}

// TestCallbackRegistryPropagatesHandlerVerdict: Execute's result decides whether
// handleCallback logs "Unknown callback data". A handler that declines (false)
// must be reported as unhandled, otherwise a payload the handler rejected is
// silently swallowed with no log line to follow.
func TestCallbackRegistryPropagatesHandlerVerdict(t *testing.T) {
	for _, verdict := range []bool{true, false} {
		r := NewCallbackRegistry()
		r.RegisterExact("probe", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
			return verdict
		}))
		got := r.Execute(newTestAppContext(), &recordingBot{}, callbackQuery("1", "probe"))
		if got != verdict {
			t.Errorf("Execute = %v, want the handler's %v", got, verdict)
		}
	}
}

// TestCallbackRegistryUnroutablePayloads covers the shapes Execute must refuse
// instead of dispatching with zeroed coordinates: a nil query or a query with no
// message would otherwise reach a handler with chatID 0 and msgID 0, and
// tgbotapi silently fails on chat 0.
func TestCallbackRegistryUnroutablePayloads(t *testing.T) {
	r := NewCallbackRegistry()
	r.RegisterFallback(CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		t.Error("the fallback ran for an unusable query")
		return true
	}))
	r.RegisterExact("probe", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		t.Error("a handler ran for an unusable query")
		return true
	}))

	// An empty data string with a valid message is deliberately absent: it is a
	// well-formed query and legitimately reaches the fallback.
	cases := map[string]*tgbotapi.CallbackQuery{
		"nil query":   nil,
		"nil message": {ID: "1", Data: "probe"},
		"zero query":  {},
	}
	for name, q := range cases {
		if r.Execute(newTestAppContext(), &recordingBot{}, q) {
			t.Errorf("%s: Execute = true, want false", name)
		}
	}
}

// TestCallbackRegistryWithoutFallbackRefusesUnknownPayload: no handler, no
// fallback. Reporting false is what makes handleCallback log the payload instead
// of the press disappearing.
func TestCallbackRegistryWithoutFallbackRefusesUnknownPayload(t *testing.T) {
	r := NewCallbackRegistry()
	r.RegisterExact("known", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		return true
	}))

	if r.Execute(newTestAppContext(), &recordingBot{}, callbackQuery("1", "unknown")) {
		t.Error("Execute = true for a payload no handler claims")
	}
	if !r.Execute(newTestAppContext(), &recordingBot{}, callbackQuery("1", "known")) {
		t.Error("Execute = false for a registered payload")
	}
}

// TestCallbackRegistryExecuteIsConcurrencySafe: the registry is built once at
// bootstrap and only read afterwards, so several Telegram update goroutines may
// execute against it at the same time. Run under -race (and the deadlock build
// tag) this fails if a read touches a map that something else is writing.
func TestCallbackRegistryExecuteIsConcurrencySafe(t *testing.T) {
	r := NewCallbackRegistry()
	r.RegisterExact("show_status", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		return true
	}))
	r.RegisterPrefix("health_", CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		return true
	}))
	r.RegisterFallback(CallbackFunc(func(*AppContext, BotAPI, int64, int, *tgbotapi.CallbackQuery, string) bool {
		return true
	}))

	ctx := newTestAppContext()
	payloads := []string{"show_status", "health_cpu", "settings_lang", "container_select"}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			bot := &recordingBot{}
			for j := 0; j < 50; j++ {
				if !r.Execute(ctx, bot, callbackQuery("1", payloads[(i+j)%len(payloads)])) {
					t.Errorf("payload %q was not routed", payloads[(i+j)%len(payloads)])
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

// TestCallbackFuncHandleAdapterIsTheHandler: CallbackFunc must forward both the
// arguments and the return value; a version that dropped the arguments would make
// every registered closure see an empty payload.
func TestCallbackFuncHandleAdapterIsTheHandler(t *testing.T) {
	var seenData string
	var h CallbackHandler = CallbackFunc(func(_ *AppContext, _ BotAPI, chatID int64, msgID int, _ *tgbotapi.CallbackQuery, data string) bool {
		seenData = data
		return chatID == 1 && msgID == 2
	})

	if !h.Handle(newTestAppContext(), &recordingBot{}, 1, 2, nil, "payload") {
		t.Error("Handle dropped the coordinates")
	}
	if seenData != "payload" {
		t.Errorf("Handle dropped the data: %q", seenData)
	}
	if h.Handle(newTestAppContext(), &recordingBot{}, 9, 9, nil, "payload") {
		t.Error("Handle ignored the handler's verdict")
	}
}
