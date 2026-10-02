package commands

import "nasbot/pkg/model"

// sanitizeErr masks credentials that ride along inside transport errors.
//
// A Telegram failure is a *url.Error carrying the full request URL, token
// included, so an unsanitized error puts bot_token in the journal and, worse, in
// the chat of the very user whose chat is protected by that token.
//
// The pattern itself lives in pkg/model, not here: pkg/commands cannot import
// internal/app (internal/app imports pkg/commands), so keeping a local copy
// would mean a second regex to update every time the pattern grows.
func sanitizeErr(err error) error { return model.SanitizeErr(err) }
