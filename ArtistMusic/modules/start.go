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
	"strings"

	td "github.com/ArtistMusic/Artist"

	"ArtistMusic/config"
	"ArtistMusic/ArtistMusic/core"
	"ArtistMusic/ArtistMusic/database"
	"ArtistMusic/ArtistMusic/locales"
)

func startHandler(c *td.Client, m *td.Message) error {
	if isChannel(c, m) {
		return nil
	}

	if !m.IsPrivate() {
		database.AddServedChat(m.ChatID())
		_, err := m.ReplyText(c, F(m.ChatID(), "start_group"), nil)
		return err
	}

	database.AddServedUser(m.ChatID())
	sender, _ := m.GetUser(c)

	if m.Args() == "pm_help" {
		if err := helpHandler(c, m); err != nil {
			return err
		}
	} else if err := handlePmStart(c, m, sender); err != nil {
		return err
	}

	logStart(c, m, sender)
	return nil
}

func startCaption(c *td.Client, chatID int64, sender *td.User, fallbackID int64) string {
	return F(chatID, "start_private", locales.Arg{
		"user": mentionOf(sender, fallbackID),
		"bot":  td.Mention(c.Me.FirstName, c.Me.Id, true, true),
	})
}

func handlePmStart(c *td.Client, m *td.Message, sender *td.User) error {
	caption := startCaption(c, m.ChatID(), sender, m.SenderID())

	if image := config.StartImage(); image != "" {
		markup := core.GetStartMarkup(m.ChatID())
		var err error
		if isAnimationURL(image) {
			_, err = c.SendAnimation(m.ChatID(), td.InputFileRemote{Id: image}, &td.SendAnimationOpts{
				Caption:     caption,
				ParseMode:   td.ParseModeHTML,
				ReplyMarkup: markup,
			})
		} else {
			_, err = c.SendPhoto(m.ChatID(), td.InputFileRemote{Id: image}, &td.SendPhotoOpts{
				Caption:     caption,
				ReplyMarkup: markup,
			})
		}
		if err == nil {
			return nil
		}
		c.Logger.Error("start image send failed, falling back to text", "error", err)
	}

	_, err := c.SendTextMessage(m.ChatID(), caption, &td.SendTextMessageOpts{
		ReplyMarkup: core.GetStartMarkup(m.ChatID()),
	})
	return err
}

func startCB(c *td.Client, cb *td.UpdateNewCallbackQuery) error {
	cb.Answer(c, 0, false, "", "")

	msg, err := cb.GetMessage(c)
	if err != nil {
		return err
	}
	sender, _ := c.GetUser(cb.SenderUserId)

	caption := startCaption(c, cb.ChatId, sender, cb.SenderUserId)
	markup := core.GetStartMarkup(cb.ChatId)

	if config.StartImage() == "" {
		_, err = cb.EditMessageText(c, caption, &td.EditTextMessageOpts{
			ReplyMarkup: markup,
		})
		return err
	}

	formattedCaption, err := c.GetFormattedText(caption, nil, "HTML")
	if err != nil {
		return err
	}

	content := mediaContent(config.StartImage(), formattedCaption)

	_, err = c.EditMessageMedia(cb.ChatId, content, msg.Id, &td.EditMessageMediaOpts{
		ReplyMarkup: markup,
	})
	return err
}

// isPhotoMessage reports whether m is a media message that carries a caption
// (photo, animation/GIF or video) instead of plain text.
func isPhotoMessage(m *td.Message) bool {
	switch m.Content.(type) {
	case *td.MessagePhoto, *td.MessageAnimation, *td.MessageVideo:
		return true
	}
	return false
}

// isAnimationURL reports whether the URL points to a GIF/MP4-style animation.
func isAnimationURL(u string) bool {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	u = strings.ToLower(u)
	for _, ext := range []string{".mp4", ".gif", ".webm", ".mov"} {
		if strings.HasSuffix(u, ext) {
			return true
		}
	}
	return false
}

// mediaContent builds the right input content (animation or photo) for a
// remote start/ping image URL.
func mediaContent(url string, caption *td.FormattedText) td.InputMessageContent {
	if isAnimationURL(url) {
		return &td.InputMessageAnimation{
			Animation: &td.InputAnimation{Animation: td.InputFileRemote{Id: url}},
			Caption:   caption,
		}
	}
	return &td.InputMessagePhoto{
		Photo:   &td.InputPhoto{Photo: td.InputFileRemote{Id: url}},
		Caption: caption,
	}
}

// isChannelPost reports whether m came from a broadcast channel.
func isChannel(c *td.Client, m *td.Message) bool {
	chat, err := m.GetChat(c)
	if err != nil {
		return false
	}
	sg, ok := chat.Type.(*td.ChatTypeSupergroup)
	return ok && sg.IsChannel
}

// mentionOf builds an HTML mention, falling back to a plain "user" mention
// if sender is nil (e.g. GetUser failed).
func mentionOf(sender *td.User, fallbackId int64) string {
	if sender == nil {
		return mentionOfName("user", fallbackId)
	}
	return mentionOfName(strings.TrimSpace(sender.FirstName+" "+sender.LastName), sender.Id)
}

// mentionOfName builds an HTML mention for a raw name and user ID.
func mentionOfName(name string, id int64) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "user"
	}
	return td.Mention(name, id, true, true)
}

// mentionOfAssistant builds an HTML mention for an assistant account.
func mentionOfAssistant(a *core.Assistant) string {
	if a == nil || a.Self == nil {
		return "Unknown"
	}
	return mentionOfName(strings.TrimSpace(a.Self.FirstName+" "+a.Self.LastName), a.Self.ID)
}

// logStart reports a bot start to the configured logger chat, if enabled.
func logStart(c *td.Client, m *td.Message, sender *td.User) {
	if config.LoggerID == 0 || !isLoggerEnabled() || m.SenderID() == config.OwnerID {
		return
	}

	uName := "N/A"
	if sender != nil && sender.Usernames != nil && len(sender.Usernames.ActiveUsernames) > 0 {
		uName = "@" + sender.Usernames.ActiveUsernames[0]
	}

	msg := F(config.LoggerID, "logger_bot_started", locales.Arg{
		"mention":       mentionOf(sender, m.SenderID()),
		"user_id":       m.SenderID(),
		"user_username": uName,
	})

	if _, err := c.SendTextMessage(config.LoggerID, msg, nil); err != nil {
		c.Logger.Error("Logger send failed", "error", err)
	}
}
