package model

// Translate resolves a key for a specific language.
// It is set by the main package during bootstrap.
var Translate = func(_ string, key string) string {
	return key
}
