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

package core

import (
	"fmt"
	"math/rand"
	"strings"

	td "github.com/ArtistMusic/Artist"

	"ArtistMusic/config"
	"ArtistMusic/ArtistMusic/locales"
	"ArtistMusic/ArtistMusic/utils"
)

var F func(chatID int64, key string, values ...locales.Arg) string

var Bot *td.Client

func botUsername() string {
	if Bot == nil || Bot.Me == nil || Bot.Me.Usernames == nil {
		return ""
	}
	usernames := Bot.Me.Usernames.ActiveUsernames
	if len(usernames) == 0 {
		return ""
	}
	return usernames[0]
}

const (
	ColourRed   = "red"
	ColourBlue  = "blue"
	ColourGreen = "green"
)

var buttonColours = []string{ColourRed, ColourBlue, ColourGreen}

func randomColour() string {
	return buttonColours[rand.Intn(len(buttonColours))]
}

func urlBtn(text, url string) td.InlineKeyboardButton {
	return td.InlineKeyboardButton{
		Text: text,
		Type: &td.InlineKeyboardButtonTypeUrl{Url: url},
	}
}

func dataBtn(text, cb string) td.InlineKeyboardButton {
	return td.InlineKeyboardButton{
		Text: text,
		Type: &td.InlineKeyboardButtonTypeCallback{Data: []byte(cb)},
	}
}

func styleBtn(text, cb, colour string) td.InlineKeyboardButton {
	b := dataBtn(text, cb)

	if config.DisableColour {
		return b
	}

	switch strings.ToLower(colour) {
	case ColourRed:
		b.Style = td.ButtonStyleDanger{}
	case ColourBlue:
		b.Style = td.ButtonStylePrimary{}
	case ColourGreen:
		b.Style = td.ButtonStyleSuccess{}
	}

	return b
}

func styleURLBtn(text, url, colour string) td.InlineKeyboardButton {
	b := urlBtn(text, url)

	if config.DisableColour {
		return b
	}

	switch strings.ToLower(colour) {
	case ColourRed:
		b.Style = td.ButtonStyleDanger{}
	case ColourBlue:
		b.Style = td.ButtonStylePrimary{}
	case ColourGreen:
		b.Style = td.ButtonStyleSuccess{}
	}

	return b
}

func DataBtn(text, cb string) td.InlineKeyboardButton {
	return styleBtn(text, cb, randomColour())
}

func UrlBtn(text, url string) td.InlineKeyboardButton {
	return styleURLBtn(text, url, randomColour())
}


func GetCancelKeyboard(chatID int64) td.ReplyMarkup {
	return &td.ReplyMarkupInlineKeyboard{
		Rows: [][]td.InlineKeyboardButton{
			{
				DataBtn(F(chatID, "DOWNLOAD_CANCEL_BTN"), "cancel"),
			},
		},
	}
}

func GetBroadcastCancelKeyboard(chatID int64) td.ReplyMarkup {
	return &td.ReplyMarkupInlineKeyboard{
		Rows: [][]td.InlineKeyboardButton{
			{
				DataBtn(F(chatID, "BROADCAST_CANCEL_BTN"), "bcast_cancel"),
			},
		},
	}
}

func SuppMarkup(chatID int64) td.ReplyMarkup {
	return &td.ReplyMarkupInlineKeyboard{
		Rows: [][]td.InlineKeyboardButton{{
			UrlBtn(F(chatID, "SUPPORT_BTN"), config.SupportChat),
		}},
	}
}

func GetStopConfirmMarkup(
	chatID int64,
	r *RoomState,
	isPaused bool,
) td.ReplyMarkup {
	prefix := fmt.Sprintf("room:%d:", r.ID)

	text, cb := "CONFIRM_UNMUTE_BTN", prefix+"unmute"

	if isPaused {
		text, cb = "CONFIRM_RESUME_BTN", prefix+"resume"
	}

	return &td.ReplyMarkupInlineKeyboard{
		Rows: [][]td.InlineKeyboardButton{
			{
				DataBtn(F(chatID, text), cb),
				DataBtn(F(chatID, "CONFIRM_STOP_BTN"), prefix+"stop"),
			},
		},
	}
}

func GetPlayMarkup(chatID int64, r *RoomState, queued bool) td.ReplyMarkup {
	prefix := fmt.Sprintf("room:%d:", r.ID)
	track := r.Track()
	duration := 0
	if track != nil {
		duration = track.Duration
	}

	progress := utils.GetProgressBar(r.Position(), duration)
	progress = utils.FormatTime(
		r.Position(),
	) + " " + progress + " " + utils.FormatTime(
		duration,
	)

	rows := make([][]td.InlineKeyboardButton, 0, 5)

	if !queued {
		rows = append(rows, []td.InlineKeyboardButton{
			DataBtn(progress, "progress"),
		})
	}

	// Main controls (random colours, re-rolled on every refresh)
	rows = append(rows, []td.InlineKeyboardButton{
		DataBtn("▶️", prefix+"resume"),
		DataBtn("⏸", prefix+"pause"),
		DataBtn("🔁", prefix+"replay"),
		DataBtn("⏭", prefix+"skip"),
		DataBtn("⏹", prefix+"stop"),
	})

	// Seek controls
	rows = append(rows, []td.InlineKeyboardButton{
		DataBtn("« 15s", prefix+"seekback_15"),
		DataBtn("15s »", prefix+"seek_15"),
	})

	// Autoplay (ON/OFF) + Close in a single row
	apText, apColour := "◉ ᴧᴜᴛσᴘʟᴧʏ : σғғ", ColourRed
	if r.Autoplay() {
		apText, apColour = "◉ ᴧᴜᴛσᴘʟᴧʏ : ση", ColourGreen
	}
	rows = append(rows, []td.InlineKeyboardButton{
		styleBtn(apText, prefix+"autoplay_toggle", apColour),
		DataBtn("× "+F(chatID, "CLOSE_BTN"), "close"),
	})

	return &td.ReplyMarkupInlineKeyboard{Rows: rows}
}

func GetGroupHelpKeyboard(chatID int64) td.ReplyMarkup {
	return &td.ReplyMarkupInlineKeyboard{
		Rows: [][]td.InlineKeyboardButton{
			{
				UrlBtn(
					F(chatID, "GC_HELP_BTN"),
					"https://t.me/"+botUsername()+"?start=pm_help",
				),
			},
		},
	}
}

func GetStartMarkup(chatID int64) td.ReplyMarkup {
	return &td.ReplyMarkupInlineKeyboard{
		Rows: [][]td.InlineKeyboardButton{
			{
				UrlBtn(
					F(chatID, "ADD_ME_BTN"),
					"https://t.me/"+botUsername()+"?startgroup&admin=invite_users",
				),
			},
			{
				DataBtn(F(chatID, "START_HELP_BTN"), "help_cb"),
			},
			{
				UrlBtn(F(chatID, "UPDATES_BTN"), config.SupportChannel),
				UrlBtn(F(chatID, "SUPPORT_BTN"), config.SupportChat),
			},
			{
				UrlBtn(F(chatID, "SOURCE_BTN"), "https://github.com/elevenyts/ArtistMusic"),
			},
		},
	}
}

func GetHelpKeyboard(chatID int64) td.ReplyMarkup {
	return &td.ReplyMarkupInlineKeyboard{
		Rows: [][]td.InlineKeyboardButton{
			{
				DataBtn(F(chatID, "HELP_ADMIN_BTN"), "help:admin"),
				DataBtn(F(chatID, "HELP_BCAST_BTN"), "help:bcast"),
			},
			{
				DataBtn(F(chatID, "HELP_PLAY_BTN"), "help:play"),
				DataBtn(F(chatID, "HELP_THUMB_BTN"), "help:thumb"),
			},
			{
				DataBtn(F(chatID, "HELP_START_BTN"), "help:start"),
				DataBtn(F(chatID, "HELP_AUTOPLAY_BTN"), "help:autoplay"),
			},
			{
				DataBtn(F(chatID, "HELP_INLINE_BTN"), "help:inline"),
			},
			{
				DataBtn(F(chatID, "BACK_BTN"), "start"),
			},
		},
	}
}

func GetBackKeyboard(chatID int64) td.ReplyMarkup {
	return &td.ReplyMarkupInlineKeyboard{
		Rows: [][]td.InlineKeyboardButton{
			{
				DataBtn(F(chatID, "BACK_BTN"), "help:main"),
			},
		},
	}
}

func GetRestartConfirmMarkup(chatID int64) td.ReplyMarkup {
	return &td.ReplyMarkupInlineKeyboard{
		Rows: [][]td.InlineKeyboardButton{
			{
				DataBtn(F(chatID, "restart_btn_bot"), "restart:bot"),
				DataBtn(F(chatID, "restart_btn_replay"), "restart:replay"),
			},
		},
	}
}
