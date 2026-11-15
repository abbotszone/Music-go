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

package modules

import (
	"fmt"
	"strings"

	td "github.com/ArtistMusic/Artist"

	"ArtistMusic/ArtistMusic/locales"
	"ArtistMusic/ArtistMusic/utils"
)

func queueHandler(c *td.Client, m *td.Message) error {
	return handleQueue(c, m, false)
}








func handleQueue(c *td.Client, m *td.Message, cplay bool) error {
	if !isSuperGroup(c, m) {
		return nil
	}

	if cplay && !filterAuthUsers(c, m) {
		return nil
	}

	chatID := m.ChatID()

	r, err := getEffectiveRoom(m.ChatID(), cplay)
	if err != nil {
		_, err := m.ReplyText(c, err.Error(), nil)
		return err
	}

	t := r.Track()
	if !r.Active() || t == nil {
		_, err := m.ReplyText(c, F(chatID, "queue_no_active"), nil)
		return err
	}

	var b strings.Builder

	b.WriteString(F(chatID, "queue_header"))
	b.WriteString("\n\n")

	b.WriteString(F(chatID, "queue_now_playing"))
	b.WriteString("\n")

	fmt.Fprintf(
		&b,
		"♬ <a href=\"%s\">%s</a> — %s [%s]\n\n",
		t.URL,
		utils.EscapeHTML(utils.ShortTitle(t.Title, 35)),
		t.Requester,
		utils.FormatDuration(t.Duration),
	)

	queue := r.Queue()
	q := len(queue)
	useQuote := q >= 3

	if q > 0 {
		b.WriteString(F(chatID, "queue_up_next"))
		n := "\n"
		if !useQuote {
			n += "\n"
		}
		b.WriteString(n)

		if useQuote {
			b.WriteString("<blockquote>")
		}

		for i, track := range queue {
			if i >= 10 {
				break
			}

			fmt.Fprintf(
				&b,
				"%d. ♪ <a href=\"%s\">%s</a> — %s [%s]\n",
				i+1,
				track.URL,
				utils.EscapeHTML(utils.ShortTitle(track.Title, 35)),
				track.Requester,
				utils.FormatDuration(track.Duration),
			)
		}

		if useQuote {
			b.WriteString("</blockquote>")
		}

		if q > 10 {
			var full strings.Builder

			full.WriteString(F(chatID, "queue_header"))
			full.WriteString("\n\n")

			full.WriteString(F(chatID, "queue_now_playing"))
			full.WriteString("\n")

			fmt.Fprintf(
				&full,
				"♬ %s — %s [%s]\n\n",
				t.Title,
				t.Requester,
				utils.FormatDuration(t.Duration),
			)

			full.WriteString(F(chatID, "queue_up_next"))
			full.WriteString("\n\n")

			for i, track := range queue {
				fmt.Fprintf(
					&full,
					"%d. %s — %s [%s]\n",
					i+1,
					track.Title,
					track.Requester,
					utils.FormatDuration(track.Duration),
				)
			}

			link, err := utils.CreatePaste(full.String())
			remaining := q - 10

			if err == nil && link != "" {
				more := fmt.Sprintf("<a href=\"%s\">%d</a>", link, remaining)

				b.WriteString(F(chatID, "queue_more_line", locales.Arg{
					"remaining": more,
				}))
			} else {
				b.WriteString(F(chatID, "queue_more_line", locales.Arg{
					"remaining": remaining,
				}))
			}
		}
	} else {
		b.WriteString(F(chatID, "queue_empty_tail"))
	}
	_, rerr := m.ReplyText(c, b.String(), &td.SendTextMessageOpts{DisableWebPagePreview: true})
	return rerr
}



