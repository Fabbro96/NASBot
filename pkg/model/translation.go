package model

// Translate resolves a key for a specific language.
//
// It is a function variable on purpose: pkg/model and pkg/commands cannot
// import internal/app (import cycle), so the main package installs the real
// resolver during bootstrap with an init() in
// internal/app/model_translation_bridge.go.
//
// The default below is deliberately the identity function so that a call made
// before bootstrap — or in a test that does not link the app package — yields
// the key instead of a panic or an empty string.
var Translate = func(_ string, key string) string {
	return key
}
