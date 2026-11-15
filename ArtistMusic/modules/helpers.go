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
	"errors"
	"fmt"
	"strings"
	"time"

	"ArtistMusic/ArtistMusic/logger"

	td "github.com/ArtistMusic/Artist"

	"ArtistMusic/config"
	"ArtistMusic/ArtistMusic/core"
	state "ArtistMusic/ArtistMusic/core/models"
	"ArtistMusic/ArtistMusic/database"
	"ArtistMusic/ArtistMusic/locales"
	"ArtistMusic/ArtistMusic/utils"
)

func getEffectiveRoom(chatID int64, cplay bool) (*core.RoomState, error) {
	origChatID := chatID

	if cplay {
		cplayID, err := database.LinkedChannel(chatID)
		if err != nil || cplayID == 0 {
			return nil, errors.New(F(chatID, "cplay_id_not_set"))
		}
		chatID = cplayID
	}
	ass, err := core.Assistants.ForChat(chatID)
	if err != nil {
		return nil, fmt.Errorf("failed to get assistant for your chat: %w", err)
	}
	// The room is shared with every other command that uses this chat (and,
	// for cplay, with the linked channel's own /play), so its ChatID must not
	// be rewritten here. origChatID is kept only for the caller's context.
	_ = origChatID
	r := core.CreateRoom(chatID, ass)
	return r, nil
}

func shouldShowThumb(chatID int64) bool {
	noThumb, err := database.ThumbnailsDisabled(chatID)
	if err != nil {
		// On error, default to showing thumbnails
		return true
	}
	// ThumbnailsDisabled = true means DON'T show thumb
	// So we return the inverse
	return !noThumb
}

// nowPlayingText builds the "now playing" status text for a track.
func nowPlayingText(chatID int64, t *state.Track) string {
	return F(chatID, "stream_now_playing", locales.Arg{
		"url":      t.URL,
		"title":    utils.EscapeHTML(utils.ShortTitle(t.Title, 45)),
		"duration": utils.FormatDuration(t.Duration),
		"by":       t.Requester,
	})
}

// sendNowPlaying sends or edits the "now playing" playback message for the
// given track, honoring the thumbnail/artwork setting, and returns the
// resulting message.
func sendNowPlaying(
	c *td.Client,
	statusMsg *td.Message,
	chatID int64,
	r *core.RoomState,
	t *state.Track,
) *td.Message {
	return showTrackMessage(c, statusMsg, chatID, r, t, nowPlayingText(chatID, t), false)
}

// showTrackMessage sends or edits a track message with optional artwork,
// honoring the thumbnail/artwork setting for the chat, and returns the
// resulting message.
func showTrackMessage(
	c *td.Client,
	statusMsg *td.Message,
	chatID int64,
	r *core.RoomState,
	t *state.Track,
	msgText string,
	queued bool,
) *td.Message {
	markup := core.GetPlayMarkup(chatID, r, queued)

	opts := &td.SendTextMessageOpts{
		ParseMode:             "HTML",
		ReplyMarkup:           markup,
		DisableWebPagePreview: true,
	}

	if t.Artwork != "" && shouldShowThumb(chatID) {
		// Every thumbnail is sent in 16:9 (see thumb169.go).
		photoFile := thumb169(t.Artwork)
		content := &td.InputMessagePhoto{
			Photo: &td.InputPhoto{
				Photo: photoFile,
			},
		}
		if statusMsg != nil {
			if caption, err := c.GetFormattedText(msgText, nil, "HTML"); err == nil {
				content.Caption = caption
				m, err := c.EditMessageMedia(
					chatID,
					content,
					statusMsg.Id,
					&td.EditMessageMediaOpts{ReplyMarkup: markup},
				)
				if err == nil {
					return m
				}
				logger.Errorf("EditMessageMedia (track message) failed: %v", err)
			}
		}
		if statusMsg != nil {
			_ = statusMsg.Delete(c, true)
		}
		photo, err := c.SendPhoto(
			chatID,
			photoFile,
			&td.SendPhotoOpts{
				Caption:     msgText,
				ParseMode:   "HTML",
				ReplyMarkup: markup,
			},
		)
		if err != nil {
			logger.Errorf("SendPhoto (track message) failed: %v", err)
			m, _ := c.SendTextMessage(chatID, msgText, opts)
			return m
		}
		return photo
	}

	if statusMsg != nil {
		m, err := utils.EOR(c, statusMsg, msgText, &td.EditTextMessageOpts{
			ParseMode:             "HTML",
			ReplyMarkup:           markup,
			DisableWebPagePreview: true,
		})
		if err == nil && m != nil {
			return m
		}
		logger.Errorf("edit track message failed, sending a new one: %v", err)
	}
	m, err := c.SendTextMessage(chatID, msgText, opts)
	if err != nil {
		logger.Errorf("SendTextMessage (track message) failed: %v", err)
	}
	return m
}

func F(chatID int64, key string, values ...locales.Arg) string {
	lang, err := database.Language(chatID)
	if err != nil {
		logger.Errorf("failed to get language for %d: %v", chatID, err)
		lang = config.DefaultLang
	}
	var val locales.Arg
	if len(values) > 0 {
		val = values[0]
	}
	return locales.Get(lang, key, val)
}

func isLoggerEnabled() bool {
	l, err := database.IsLoggerEnabled()
	if err != nil {
		logger.Errorf("failed to check if logger is enabled: %v", err)
		return false
	}
	return l
}

func sendPlayLogs(c *td.Client, m *td.Message, track *state.Track, queued bool) {
	if config.LoggerID == 0 || config.LoggerID == m.ChatID() ||
		m.SenderID() == config.OwnerID || !isLoggerEnabled() {
		return
	}

	header := F(m.ChatID(), "logger_playback_started")
	if queued {
		header = F(m.ChatID(), "logger_playback_queued")
	}

	chat, _ := m.GetChat(c)
	groupName := "N/A"
	groupLink := ""
	if chat != nil {
		groupName = chat.Title
		if l, err := m.GetLink(c); err == nil && l != nil && l.IsPublic {
			groupLink = l.Link
		}
	}

	var sb strings.Builder
	sb.WriteString("♪ ")
	if groupLink != "" {
		fmt.Fprintf(&sb, "<b><a href=\"%s\">%s</a></b>\n\n", groupLink, header)
	} else {
		fmt.Fprintf(&sb, "<b><u>%s</u></b>\n\n", header)
	}

	sender, _ := m.GetUser(c)
	requestedBy := mentionOf(sender, m.SenderID())

	sb.WriteString(F(m.ChatID(), "logger_playback_template", locales.Arg{
		"track_url":       track.URL,
		"track":           utils.EscapeHTML(utils.ShortTitle(track.Title)),
		"source":          string(track.Source),
		"group":           groupName,
		"group_id":        m.ChatID(),
		"requested_by":    requestedBy,
		"requested_by_id": m.SenderID(),
	}))

	_, err := core.Bot.SendTextMessage(
		config.LoggerID,
		sb.String(),
		&td.SendTextMessageOpts{ParseMode: "HTML", DisableWebPagePreview: true},
	)
	if err != nil {
		logger.Error("failed to send logger msg: " + err.Error())
	}
}

// sendAutoplayLogs posts the same "playback started" log as sendPlayLogs, but
// for tracks started by autoplay, where there is no user command message.
// statusMsg is the bot's own message in the chat (used only for a public link).
func sendAutoplayLogs(c *td.Client, chatID int64, track *state.Track, statusMsg *td.Message) {
	if track == nil || config.LoggerID == 0 || config.LoggerID == chatID || !isLoggerEnabled() {
		return
	}

	groupName := "N/A"
	groupLink := ""
	if chat, err := c.GetChat(chatID); err == nil && chat != nil {
		groupName = chat.Title
		if statusMsg != nil {
			if l, lerr := statusMsg.GetLink(c); lerr == nil && l != nil && l.IsPublic {
				groupLink = l.Link
			}
		}
	}

	header := F(chatID, "logger_playback_started")

	var sb strings.Builder
	sb.WriteString("♪ ")
	if groupLink != "" {
		fmt.Fprintf(&sb, "<b><a href=\"%s\">%s</a></b>\n\n", groupLink, header)
	} else {
		fmt.Fprintf(&sb, "<b><u>%s</u></b>\n\n", header)
	}

	sb.WriteString(F(chatID, "logger_playback_template", locales.Arg{
		"track_url":       track.URL,
		"track":           utils.EscapeHTML(utils.ShortTitle(track.Title)),
		"source":          string(track.Source),
		"group":           groupName,
		"group_id":        chatID,
		"requested_by":    track.Requester,
		"requested_by_id": "autoplay",
	}))

	if _, err := core.Bot.SendTextMessage(
		config.LoggerID,
		sb.String(),
		&td.SendTextMessageOpts{ParseMode: "HTML", DisableWebPagePreview: true},
	); err != nil {
		logger.Error("failed to send autoplay logger msg: " + err.Error())
	}
}

// WithBlacklistCallback is kept as a thin wrapper so handler registration
// stays uniform. Blacklist and maintenance checks were removed.
func WithBlacklistCallback(
	handler func(*td.Client, *td.UpdateNewCallbackQuery) error,
) func(*td.Client, *td.UpdateNewCallbackQuery) error {
	return handler
}

// WithBlacklistMessage runs the handler and, for private chats, deletes the
// command message afterwards.
func WithBlacklistMessage(
	handler func(*td.Client, *td.Message) error,
) func(*td.Client, *td.Message) error {
	return func(c *td.Client, m *td.Message) error {
		err := handler(c, m)

		if m.IsCommand() && m.IsPrivate() {
			if derr := m.Delete(c, true); derr != nil {
				logger.Debugf("failed to delete command message: %v", derr)
			}
		}

		return err
	}
}

func leaveChat(c *td.Client, chatID int64) {
	go func() {
		time.Sleep(1 * time.Second)
		if err := c.LeaveChat(chatID); err != nil {
			logger.Errorf("failed to leave blacklisted chatID=%d: %v", chatID, err)
		}
		core.Assistants.WithAssistant(
			chatID,
			func(ass *core.Assistant) { ass.Client.LeaveChannel(chatID) },
		)
	}()
}
