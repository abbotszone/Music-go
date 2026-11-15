/*
 * ● ArtistMusic
 * ○ A high-performance engine for streaming music in Telegram voicechats.
 *
 * Copyright (C) 2026 ArtistMusic
 *
 * This program is free software: you can redistribute it and/or modify it under the
 * terms of the GNU General Public License as published by the Free Software Foundation,
 * either version 3 of the License, or (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT ANY
 * WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR A
 * PARTICULAR PURPOSE. See the GNU General Public License for more details.
 *
 * Repository: https://github.com/elevenyts/ArtistMusic
 */

package locales

import "regexp"

var emojiRegex = regexp.MustCompile(`\{emoji:([^}]+)\}`)

// expandEmojis unwraps legacy {emoji:X} tokens into the bare symbol X.
// Emojis were replaced by plain text symbols, so no custom <tg-emoji>
// entities are generated any more.
func expandEmojis(s string) string {
	return emojiRegex.ReplaceAllString(s, "$1")
}
