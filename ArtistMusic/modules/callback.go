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
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"ArtistMusic/ArtistMusic/core"
	"ArtistMusic/ArtistMusic/database"
	"ArtistMusic/ArtistMusic/locales"
	"ArtistMusic/ArtistMusic/logger"
	"ArtistMusic/ArtistMusic/platforms"
	"ArtistMusic/ArtistMusic/utils"
	"ArtistMusic/config"
	td "github.com/ArtistMusic/Artist"
)

// ───────────── callback ─────────────

func cancelHandler(c *td.Client, u *td.UpdateNewCallbackQuery) error {
	chatID := u.ChatId

	if !checkAdminOrAuth(c, u, chatID) {
		return nil
	}

	if downloads.cancel(chatID) {
		u.Answer(c, 0, true, F(chatID, "download_cancelled"), "")
	} else {
		u.Answer(c, 0, true, F(chatID, "no_download_to_cancel"), "")
	}
	return nil
}

func closeHandler(c *td.Client, u *td.UpdateNewCallbackQuery) error {
	u.Answer(c, 0, false, "", "")
	cbDelete(c, u)
	return nil
}

func emptyCBHandler(c *td.Client, u *td.UpdateNewCallbackQuery) error {
	u.Answer(c, 0, false, "", "")
	return nil
}

func roomHandle(c *td.Client, u *td.UpdateNewCallbackQuery) error {
	chatID := u.ChatId

	parts := strings.SplitN(u.DataString(), ":", 3)
	if len(parts) != 3 || parts[0] != "room" {
		logger.Warnf("Invalid room callback payload: %s", u.DataString())
		u.Answer(c, 0, true, F(chatID, "invalid_request"), "")
		cbDelete(c, u)
		return nil
	}
	roomID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		logger.Warnf("Invalid roomID in callback: %s", parts[1])
		u.Answer(c, 0, true, F(chatID, "invalid_request"), "")
		cbDelete(c, u)
		return nil
	}
	action := parts[2]

	r, ok := core.RoomFor(roomID)
	if !ok || !r.Active() {
		u.Answer(c, 0, true, F(chatID, "room_not_active_cb"), "")
		u.EditMessageText(c, F(chatID, "room_no_active"), nil)
		return nil
	}

	if !checkAdminOrAuth(c, u, chatID) {
		return nil
	}

	key := fmt.Sprintf("room:%d:%d", u.SenderUserId, chatID)
	if remaining := utils.GetFlood(key); remaining > 0 {
		u.Answer(c, 0, true, F(chatID, "flood_seconds", locales.Arg{
			"duration": int(math.Ceil(remaining.Seconds())),
		}), "")
		return nil
	}
	utils.SetFlood(key, 3*time.Second)

	switch {
	case strings.HasPrefix(action, "seek"):
		return handleSeekAction(c, u, r, action)
	case action == "pause":
		return handlePauseAction(c, u, r)
	case action == "resume":
		return handleResumeAction(c, u, r)
	case action == "replay":
		return handleReplayAction(c, u, r)
	case action == "skip":
		return handleSkipAction(c, u, r)
	case action == "stop":
		return handleStopAction(c, u, r)
	case action == "mute":
		return handleMuteAction(c, u, r)
	case action == "unmute":
		return handleUnmuteAction(c, u, r)
	case action == "autoplay_toggle":
		return handleAutoplayToggleAction(c, u, r)
	default:
		logger.Warnf("Unknown callback action: %s", action)
		u.Answer(c, 0, true, F(chatID, "unknown_action"), "")
	}

	return nil
}

func checkAdminOrAuth(c *td.Client, u *td.UpdateNewCallbackQuery, chatID int64) bool {
	if canUseAdminCommand(c, chatID, u.SenderUserId) {
		return true
	}

	mode, err := database.GetAdminMode(chatID)
	if err == nil && mode == database.AdminModeAdminsOnly {
		u.Answer(c, 0, true, F(chatID, "only_admin_cb"), "")
	} else {
		u.Answer(c, 0, true, F(chatID, "only_admin_or_auth_cb"), "")
	}
	return false
}

func cbDelete(c *td.Client, u *td.UpdateNewCallbackQuery) {
	msg, err := u.GetMessage(c)
	if err != nil || msg == nil {
		return
	}
	_ = msg.Delete(c, true)
}

func cbRespond(c *td.Client, u *td.UpdateNewCallbackQuery, text string, opts *td.SendTextMessageOpts) *td.Message {
	msg, err := u.GetMessage(c)
	if err != nil || msg == nil {
		logger.Errorf("cbRespond: failed to get callback message: %v", err)
		return nil
	}
	m, err := msg.ReplyText(c, text, opts)
	if err != nil {
		logger.Errorf("cbRespond: %v", err)
		return nil
	}
	return m
}

// mentionOfSender resolves the callback sender and builds an HTML mention.
func mentionOfSender(c *td.Client, userID int64) string {
	user, _ := c.GetUser(userID)
	return mentionOf(user, userID)
}

func handlePauseAction(c *td.Client, u *td.UpdateNewCallbackQuery, r *core.RoomState) error {
	chatID := u.ChatId
	logger.Infof("Callback → pause, chatID=%d", chatID)

	if r.Paused() {
		u.Answer(c, 0, true, F(chatID, "room_already_paused"), "")
		return nil
	}

	if _, err := r.Pause(); err != nil {
		logger.Errorf("Pause failed: %v", err)
		u.Answer(c, 0, true, F(chatID, "room_pause_failed", locales.Arg{
			"error": err.Error(),
		}), "")
		return nil
	}

	if r.Muted() {
		r.Unmute()
	}

	u.Answer(c, 0, true, F(chatID, "cb_pause_success", locales.Arg{
		"position": utils.FormatDuration(r.Position()),
	}), "")
	updatePlaybackMessage(c, u, r, "paused")
	return nil
}

func handleResumeAction(c *td.Client, u *td.UpdateNewCallbackQuery, r *core.RoomState) error {
	chatID := u.ChatId
	logger.Infof("Callback → resume, chatID=%d", chatID)

	if !r.Paused() {
		u.Answer(c, 0, true, F(chatID, "cb_already_playing"), "")
		return nil
	}

	if _, err := r.Resume(); err != nil {
		logger.Errorf("Resume failed: %v", err)
		u.Answer(c, 0, true, F(chatID, "cb_resume_failed"), "")
		return nil
	}

	u.Answer(c, 0, true, F(chatID, "cb_resume_success", locales.Arg{
		"position": utils.FormatDuration(r.Position()),
	}), "")
	updatePlaybackMessage(c, u, r, "playing")
	return nil
}

func handleReplayAction(c *td.Client, u *td.UpdateNewCallbackQuery, r *core.RoomState) error {
	chatID := u.ChatId
	logger.Infof("Callback → replay, chatID=%d", chatID)

	statusMsg := cbRespond(c, u, F(chatID, "cb_replaying"), nil)
	if statusMsg == nil {
		logger.Errorf("Failed to send replay status")
		return nil
	}

	if err := r.Replay(); err != nil {
		logger.Errorf("Replay failed: %v", err)
		utils.EOR(c, statusMsg, F(chatID, "replay_failed", locales.Arg{
			"error": err.Error(),
		}), nil)
		u.Answer(c, 0, true, F(chatID, "cb_replay_failed"), "")
		return nil
	}

	statusMsg = sendNowPlaying(c, statusMsg, chatID, r, r.Track())
	r.SetStatusMsg(statusMsg)

	u.Answer(c, 0, true, F(chatID, "cb_replay_success"), "")
	if _, err := u.EditMessageText(c, F(chatID, "cb_replay_edited", locales.Arg{
		"user": mentionOfSender(c, u.SenderUserId),
	}), &td.EditTextMessageOpts{ParseMode: "HTML"}); err != nil {
		logger.Errorf("Edit error: %v", err)
	}
	return nil
}

func handleSkipAction(c *td.Client, u *td.UpdateNewCallbackQuery, r *core.RoomState) error {
	chatID := u.ChatId
	logger.Infof("Callback → skip, chatID=%d", chatID)

	if len(r.Queue()) == 0 {
		core.DropRoom(r.ID)
		if _, err := u.EditMessageText(c, F(chatID, "skip_stopped", locales.Arg{
			"user": mentionOfSender(c, u.SenderUserId),
		}), &td.EditTextMessageOpts{ParseMode: "HTML"}); err != nil {
			logger.Errorf("Edit error: %v", err)
		}
		u.Answer(c, 0, true, F(chatID, "cb_skip_queue_empty"), "")
		return nil
	}

	r.SetLoop(0)
	t := r.NextTrack()

	statusMsg := cbRespond(c, u, F(chatID, "stream_downloading_next"), nil)
	if statusMsg == nil {
		logger.Errorf("Failed to send status message")
	}

	path, err := platforms.Download(context.Background(), t, statusMsg)
	if err != nil {
		logger.Errorf("Download failed for %s: %v", t.URL, err)
		utils.EOR(c, statusMsg, F(chatID, "stream_download_fail", locales.Arg{
			"error": err.Error(),
		}), nil)
		u.Answer(c, 0, true, F(chatID, "cb_skip_download_failed"), "")
		core.DropRoom(r.ID)
		return nil
	}

	if err := r.Play(t, path); err != nil {
		logger.Errorf("Play error: %v", err)
		utils.EOR(c, statusMsg, F(chatID, "stream_play_fail"), nil)
		u.Answer(c, 0, true, F(chatID, "cb_skip_play_failed"), "")
		core.DropRoom(r.ID)
		return nil
	}

	u.Answer(c, 0, true, F(chatID, "cb_skip_success"), "")
	cbDelete(c, u)

	statusMsg = sendNowPlaying(c, statusMsg, chatID, r, t)
	r.SetStatusMsg(statusMsg)

	_, err = statusMsg.ReplyText(c, F(chatID, "cb_skip_edited", locales.Arg{
		"user": mentionOfSender(c, u.SenderUserId),
	}), &td.SendTextMessageOpts{ParseMode: "HTML"})

	return err
}

func handleSeekAction(c *td.Client, u *td.UpdateNewCallbackQuery, r *core.RoomState, action string) error {
	chatID := u.ChatId

	parts := strings.SplitN(action, "_", 2)
	if len(parts) != 2 {
		u.Answer(c, 0, true, F(chatID, "invalid_request"), "")
		return nil
	}

	// action is either "seek_<N>" or "seekback_<N>"
	// Strip the direction prefix to get the numeric suffix.
	numStr := parts[1]
	isBackward := strings.HasPrefix(action, "seekback_")

	seconds, err := strconv.Atoi(numStr)
	if err != nil {
		u.Answer(c, 0, true, F(chatID, "invalid_request"), "")
		return nil
	}

	track := r.Track()
	if !r.Active() || track == nil {
		u.Answer(c, 0, true, F(chatID, "room_not_active_cb"), "")
		return nil
	}

	logger.Infof("Callback → seek %s %ds, chatID=%d", action, seconds, chatID)

	if isBackward {
		if r.Position() <= seconds {
			r.Seek(-r.Position())
		} else {
			r.Seek(-seconds)
		}
		u.Answer(c, 0, true, F(chatID, "cb_seekback_success", locales.Arg{"seconds": seconds}), "")
		replyToCallback(c, u, F(chatID, "cb_seekback_edited", locales.Arg{
			"seconds": seconds,
			"user":    mentionOfSender(c, u.SenderUserId),
		}))
	} else {
		if (track.Duration - r.Position()) <= seconds {
			u.Answer(c, 0, true, F(chatID, "cb_seek_near_end", locales.Arg{"seconds": seconds}), "")
			return nil
		}
		r.Seek(seconds)
		u.Answer(c, 0, true, F(chatID, "cb_seek_success", locales.Arg{"seconds": seconds}), "")
		replyToCallback(c, u, F(chatID, "cb_seek_edited", locales.Arg{
			"seconds": seconds,
			"user":    mentionOfSender(c, u.SenderUserId),
		}))
	}

	return nil
}

func replyToCallback(c *td.Client, u *td.UpdateNewCallbackQuery, text string) {
	msg, err := u.GetMessage(c)
	if err != nil || msg == nil {
		return
	}
	if _, err := msg.ReplyText(c, text, &td.SendTextMessageOpts{ParseMode: "HTML"}); err != nil {
		logger.Errorf("Reply error: %v", err)
	}
}

func handleStopAction(c *td.Client, u *td.UpdateNewCallbackQuery, r *core.RoomState) error {
	chatID := u.ChatId
	logger.Infof("Callback → stop, chatID=%d", chatID)

	core.DropRoom(r.ID)

	u.Answer(c, 0, true, F(chatID, "cb_stop_success"), "")
	if _, err := u.EditMessageText(c, F(chatID, "stopped", locales.Arg{
		"user": mentionOfSender(c, u.SenderUserId),
	}), &td.EditTextMessageOpts{ParseMode: "HTML"}); err != nil {
		logger.Errorf("Edit error: %v", err)
	}
	return nil
}

func handleMuteAction(c *td.Client, u *td.UpdateNewCallbackQuery, r *core.RoomState) error {
	chatID := u.ChatId

	if r.Muted() {
		u.Answer(c, 0, true, F(chatID, "mute_already_muted"), "")
		return nil
	}

	if _, err := r.Mute(); err != nil {
		u.Answer(c, 0, true, F(chatID, "mute_failed", locales.Arg{
			"error": err.Error(),
		}), "")
		return nil
	}

	u.Answer(c, 0, true, F(chatID, "cb_mute_success"), "")
	updatePlaybackMessage(c, u, r, "muted")
	return nil
}

func handleUnmuteAction(c *td.Client, u *td.UpdateNewCallbackQuery, r *core.RoomState) error {
	chatID := u.ChatId

	if !r.Muted() {
		u.Answer(c, 0, true, F(chatID, "unmute_already"), "")
		return nil
	}

	if _, err := r.Unmute(); err != nil {
		u.Answer(c, 0, true, F(chatID, "unmute_failed", locales.Arg{
			"error": err.Error(),
		}), "")
		return nil
	}

	u.Answer(c, 0, true, F(chatID, "cb_unmute_success"), "")
	updatePlaybackMessage(c, u, r, "playing")
	return nil
}

func handleAutoplayToggleAction(c *td.Client, u *td.UpdateNewCallbackQuery, r *core.RoomState) error {
	chatID := u.ChatId

	enabled := !r.Autoplay()
	r.SetAutoplay(enabled)

	statusKey := "autoplay_disabled"
	if enabled {
		statusKey = "autoplay_enabled"
	}
	u.Answer(c, 0, false, F(chatID, statusKey), "")

	markup := core.GetPlayMarkup(chatID, r, false)
	if _, err := u.EditMessageReplyMarkup(c, markup); err != nil {
		logger.Errorf("Autoplay toggle markup edit error: %v", err)
	}
	return nil
}

func updatePlaybackMessage(c *td.Client, u *td.UpdateNewCallbackQuery, r *core.RoomState, state string) {
	track := r.Track()
	if track == nil {
		return
	}

	chatID := u.ChatId
	safeTitle := utils.EscapeHTML(utils.ShortTitle(track.Title, 25))
	mention := mentionOfSender(c, u.SenderUserId)

	var msgText string
	switch state {
	case "paused":
		msgText = F(chatID, "cb_pause_message", locales.Arg{
			"url":      track.URL,
			"title":    safeTitle,
			"position": utils.FormatDuration(r.Position()),
			"duration": utils.FormatDuration(track.Duration),
			"user":     mention,
		})
	case "playing":
		msgText = F(chatID, "cb_resume_message", locales.Arg{
			"url":      track.URL,
			"title":    safeTitle,
			"duration": utils.FormatDuration(track.Duration),
			"user":     mention,
		})
	case "muted":
		msgText = F(chatID, "cb_mute_message", locales.Arg{
			"url":   track.URL,
			"title": safeTitle,
			"user":  mention,
		})
	}

	markup := core.GetPlayMarkup(chatID, r, false)

	msg, err := u.GetMessage(c)
	if err != nil || msg == nil {
		return
	}

	switch {
	case isPhotoMessage(msg):
		_, err = u.EditMessageCaption(c, msgText, &td.EditCaptionOpts{
			ParseMode:   "HTML",
			ReplyMarkup: markup,
		})
	default:
		_, err = u.EditMessageText(c, msgText, &td.EditTextMessageOpts{
			ParseMode:             "HTML",
			ReplyMarkup:           markup,
			DisableWebPagePreview: true,
		})
	}
	if err != nil {
		logger.Errorf("Edit error: %v", err)
	}
}

// ───────────── actions ─────────────

func actionFilter(m *td.Message) bool {
	switch m.Content.(type) {
	case *td.MessageChatAddMembers,
		*td.MessageVideoChatStarted,
		*td.MessageVideoChatEnded:
		return true
	}
	return false
}

func handleActions(c *td.Client, m *td.Message) error {
	chat, err := m.GetChat(c)
	if err != nil {
		return nil
	}
	switch t := chat.Type.(type) {
	case *td.ChatTypeSupergroup:
		if t.IsChannel {
			return nil
		}
	case *td.ChatTypePrivate, *td.ChatTypeSecret:
		return nil
	default:
		text := F(m.ChatID(), "supergroup_needed", locales.Arg{
			"chat_id":       m.ChatID(),
			"support_group": config.SupportChat,
		})
		if _, err := c.SendTextMessage(m.ChatID(), text, &td.SendTextMessageOpts{DisableWebPagePreview: true}); err != nil {
			logger.Errorf("failed to send supergroup conversion message to chat %d: %v", m.ChatID(), err)
			return nil
		}
		go func() {
			leaveChat(c, m.ChatID())
		}()
		return nil
	}

	switch m.Content.(type) {
	case *td.MessageChatAddMembers:
		return handleChatMemberAdd(c, m)
	case *td.MessageVideoChatStarted, *td.MessageVideoChatEnded:
		return handleVoiceChatAction(c, m)
	}

	return nil
}

func handleChatMemberAdd(c *td.Client, m *td.Message) error {
	chatID := m.ChatID()

	if c.Me == nil {
		return nil
	}
	content := m.Content.(*td.MessageChatAddMembers)

	for _, uid := range content.MemberUserIds {
		if uid != c.Me.Id {
			continue
		}

		logger.Debug("Bot added to " + utils.IntToStr(chatID))
		if _, rerr := m.ReplyText(c, F(chatID, "bot_added_normal"), nil); rerr != nil {
			return rerr
		}
		database.AddServedChat(chatID)

		if config.LoggerID != 0 {
			if _, rerr := c.SendTextMessage(config.LoggerID, F(config.LoggerID, "logger_bot_added", buildLogArgs(c, m, chatID, "added")), nil); rerr != nil {
				return rerr
			}
		}

		return nil
	}

	return nil
}

func handleBotRemoved(c *td.Client, u *td.UpdateChatMember, chatID int64) {
	logger.Debugf("Bot removed from chat %d", chatID)

	core.DropRoom(chatID)
	core.DropChatState(chatID)
	database.RemoveServedChat(chatID)

	if config.LoggerID == 0 || config.LoggerID == chatID {
		return
	}

	groupName := "N/A"
	if chat, err := c.GetChat(chatID); err == nil && chat != nil {
		groupName = chat.Title
	}

	actorID := u.ActorUserId
	actorName := "N/A"
	actorMention := "N/A"
	if actor, err := c.GetUser(actorID); err == nil && actor != nil {
		actorName = strings.TrimSpace(actor.FirstName + " " + actor.LastName)
		if actorName == "" {
			actorName = "N/A"
		}
		actorMention = mentionOf(actor, actorID)
	}

	msg := F(config.LoggerID, "logger_bot_removed", locales.Arg{
		"group_name":          groupName,
		"group_id":            chatID,
		"group_username":      "N/A",
		"removed_by_name":     actorName,
		"removed_by_id":       actorID,
		"removed_by_username": actorMention,
		"date_time":           time.Now().Format("02 Jan 2006 • 15:04"),
	})

	if _, err := c.SendTextMessage(config.LoggerID, msg, nil); err != nil {
		logger.Errorf("failed to send bot-removed log: %v", err)
	}
}

func handleVoiceChatAction(c *td.Client, m *td.Message) error {
	if isMaint, _ := database.IsMaintenanceEnabled(); isMaint {
		return nil
	}

	chatID := m.ChatID()
	isActive := true
	var duration int32

	if ended, ok := m.Content.(*td.MessageVideoChatEnded); ok {
		isActive = false
		duration = ended.Duration
	}

	s, err := core.ChatStateFor(chatID)
	if err != nil {
		logger.Errorf("Failed to get chat state for %d: %v", chatID, err)
		return nil
	}

	s.SetVoiceChat(isActive)

	msgKey := utils.IfElse(isActive, "voicechat_started", "voicechat_ended")
	if _, rerr := c.SendTextMessage(chatID, F(chatID, msgKey, locales.Arg{"duration": utils.FormatDuration(int(duration))}), nil); rerr != nil {
		logger.Error(rerr)
	}
	logger.Debugf("Voice chat %s in %d", msgKey, chatID)

	if !isActive {
		go func() {
			time.Sleep(500 * time.Millisecond)
			core.DropRoom(chatID)
			logger.Debugf("Room destroyed for ended voice chat in %d", chatID)
		}()
	}

	return nil
}

func buildLogArgs(c *td.Client, m *td.Message, chatID int64, action string) locales.Arg {
	groupName := "N/A"
	if chat, err := m.GetChat(c); err == nil && chat != nil {
		groupName = chat.Title
	}

	sender, _ := m.GetUser(c)
	actorID := m.SenderID()
	actorName := "N/A"
	if sender != nil {
		if full := strings.TrimSpace(sender.FirstName + " " + sender.LastName); full != "" {
			actorName = full
		}
	}

	return locales.Arg{
		"group_name":            groupName,
		"group_id":              chatID,
		"group_username":        "N/A",
		action + "_by_name":     actorName,
		action + "_by_id":       actorID,
		action + "_by_username": mentionOf(sender, actorID),
		"date_time":             time.Now().Format("02 Jan 2006 • 15:04"),
	}
}
