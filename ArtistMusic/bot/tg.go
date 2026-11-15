// ==========================================================
// Copyright (c) 2026 ArtistBots
// All Rights Reserved.
//
// Project      : ArtistBots API Telegram Music Bot
// Powered By   : Artist
// Type         : API Based Telegram Music Bot
//
// Bot          : @ArtistApibot
// Channel      : https://t.me/artistbots
// GitHub       : https://github.com/elevenyts/ArtistMusic
//
// Unauthorized copying, modification, or redistribution
// of this source code without permission is prohibited.
// ==========================================================

package bot

// Every direct call into the gogram library lives in this file, so if your
// gogram version names something differently you only fix it here.

import (
	"strings"

	tg "github.com/amarnathcjd/gogram/telegram"

	"github.com/elevenyts/ArtistMusic/ArtistMusic/config"
)

type button struct{ Text, Data, URL string }

func keyboard(rows ...[]button) *tg.ReplyInlineMarkup {
	kb := tg.NewKeyboard()
	for _, r := range rows {
		var bs []tg.KeyboardButton
		for _, b := range r {
			if b.URL != "" {
				bs = append(bs, tg.Button.URL(b.Text, b.URL))
			} else {
				bs = append(bs, tg.Button.Data(b.Text, b.Data))
			}
		}
		kb.AddRow(bs...)
	}
	return kb.Build()
}

func (b *Bot) reply(m *tg.NewMessage, text string, rows ...[]button) *tg.NewMessage {
	opts := &tg.SendOptions{}
	if len(rows) > 0 {
		opts.ReplyMarkup = keyboard(rows...)
	}
	msg, _ := m.Reply(text, opts)
	return msg
}

func (b *Bot) send(chatID int64, text string, rows ...[]button) *tg.NewMessage {
	opts := &tg.SendOptions{}
	if len(rows) > 0 {
		opts.ReplyMarkup = keyboard(rows...)
	}
	msg, _ := b.c.SendMessage(chatID, text, opts)
	return msg
}

func (b *Bot) edit(m *tg.NewMessage, text string, rows ...[]button) {
	opts := &tg.SendOptions{}
	if len(rows) > 0 {
		opts.ReplyMarkup = keyboard(rows...)
	}
	_, _ = m.Edit(text, opts)
}

func (b *Bot) isAdmin(chatID, userID int64) bool {
	if userID == config.C.OwnerID {
		return true
	}
	p, err := b.c.GetChatMember(chatID, userID)
	if err != nil {
		return false
	}
	return p.Status == tg.Admin || p.Status == tg.Creator
}

// copyTo re-sends the replied message to a target (hide-author = no "Forwarded" tag).
func (b *Bot) copyTo(target int64, src *tg.NewMessage, hideAuthor bool) error {
	_, err := b.c.Forward(target, src.ChatID(), []int32{src.ID}, &tg.ForwardOptions{HideAuthor: hideAuthor})
	return err
}

func isGroup(id int64) bool { return id < 0 }

func argsOf(m *tg.NewMessage) string { return strings.TrimSpace(m.Args()) }

// sendPhoto sends the track artwork with the Now Streaming caption and buttons.
func (b *Bot) sendPhoto(chatID int64, photoURL, caption string, rows ...[]button) bool {
	_, err := b.c.SendMedia(chatID, photoURL, &tg.MediaOptions{
		Caption:     caption,
		ReplyMarkup: keyboard(rows...),
	})
	return err == nil
}
