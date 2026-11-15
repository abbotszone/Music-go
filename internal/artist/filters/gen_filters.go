// Trimmed: only the filter types this bot uses.

package filters

import "github.com/ArtistMusic/Artist"

type (
	// UpdateNewCallbackQuery A new incoming callback query; for bots only
	UpdateNewCallbackQuery func(u *gotdbot.UpdateNewCallbackQuery) bool
	// Message is a filter for Message type
	Message func(msg *gotdbot.Message) bool
)
