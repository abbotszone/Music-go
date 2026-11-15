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

	"ArtistMusic/ArtistMusic/core"
	"ArtistMusic/ArtistMusic/database"
	"ArtistMusic/ArtistMusic/logger"
	"ArtistMusic/ArtistMusic/utils"
	"ArtistMusic/config"
	td "github.com/ArtistMusic/Artist"
	"github.com/ArtistMusic/Artist/filters/callbackquery"
)

// ───────────── handlers ─────────────

func Init(bot *td.Client, assistants *core.AssistantManager) {
	assistants.ForEach(func(a *core.Assistant) {
		a.Client.UpdatesGetState()
	})

	// General
	bot.OnCommand("start", WithBlacklistMessage(startHandler))
	bot.OnCommand("help", WithBlacklistMessage(helpHandler))
	bot.OnCommand("ping", WithBlacklistMessage(pingHandler))
	bot.OnCommand("queue", WithBlacklistMessage(queueHandler))
	bot.OnCommand("loop", WithBlacklistMessage(loopHandler))
	bot.OnCommand("setloop", WithBlacklistMessage(loopHandler))
	bot.OnCommand("nothumb", WithBlacklistMessage(nothumbHandler))
	bot.OnCommand("nothumbs", WithBlacklistMessage(nothumbHandler))
	bot.OnCommand("autoplay", WithBlacklistMessage(autoplayHandler))

	// Play
	bot.OnCommand("play", WithBlacklistMessage(playHandler))
	bot.OnCommand("fplay", WithBlacklistMessage(fplayHandler))
	bot.OnCommand("playforce", WithBlacklistMessage(fplayHandler))
	bot.OnCommand("vplay", WithBlacklistMessage(vplayHandler))
	bot.OnCommand("fvplay", WithBlacklistMessage(fvplayHandler))
	bot.OnCommand("vfplay", WithBlacklistMessage(fvplayHandler))
	bot.OnCommand("vplayforce", WithBlacklistMessage(fvplayHandler))

	// Playback control
	bot.OnCommand("stop", WithBlacklistMessage(stopHandler))
	bot.OnCommand("end", WithBlacklistMessage(stopHandler))
	bot.OnCommand("skip", WithBlacklistMessage(skipHandler))
	bot.OnCommand("pause", WithBlacklistMessage(pauseHandler))
	bot.OnCommand("resume", WithBlacklistMessage(resumeHandler))
	bot.OnCommand("replay", WithBlacklistMessage(replayHandler))
	bot.OnCommand("mute", WithBlacklistMessage(muteHandler))
	bot.OnCommand("unmute", WithBlacklistMessage(unmuteHandler))
	bot.OnCommand("seek", WithBlacklistMessage(seekHandler))
	bot.OnCommand("seekback", WithBlacklistMessage(seekbackHandler))

	// Owner / maintenance
	bot.OnCommand("broadcast", WithBlacklistMessage(broadcastHandler))
	bot.OnCommand("gcast", WithBlacklistMessage(broadcastHandler))
	bot.OnCommand("bcast", WithBlacklistMessage(broadcastHandler))
	bot.OnCommand("reload", WithBlacklistMessage(reloadHandler))
	bot.OnCommand("restart", WithBlacklistMessage(handleRestart))

	// Callbacks
	bot.OnUpdateNewCallbackQuery(WithBlacklistCallback(broadcastCancelCB), callbackquery.Regex("^bcast_cancel$"))
	bot.OnUpdateNewCallbackQuery(WithBlacklistCallback(restartConfirmHandler), callbackquery.Regex("^restart:(bot|replay)$"))
	bot.OnUpdateNewCallbackQuery(WithBlacklistCallback(startCB), callbackquery.Equal("start"))
	bot.OnUpdateNewCallbackQuery(WithBlacklistCallback(helpCB), callbackquery.Equal("help_cb"))
	bot.OnUpdateNewCallbackQuery(WithBlacklistCallback(helpCallbackHandler), callbackquery.Regex("^help:(.+)$"))
	bot.OnUpdateNewCallbackQuery(WithBlacklistCallback(cancelHandler), callbackquery.Equal("cancel"))
	bot.OnUpdateNewCallbackQuery(WithBlacklistCallback(closeHandler), callbackquery.Equal("close"))
	bot.OnUpdateNewCallbackQuery(WithBlacklistCallback(roomHandle), callbackquery.Regex("^room:-?\\d+:\\w+$"))
	bot.OnUpdateNewCallbackQuery(WithBlacklistCallback(emptyCBHandler), callbackquery.Regex("progress"))
	bot.OnUpdateNewCallbackQuery(WithBlacklistCallback(autoplayCallbackHandler), callbackquery.Equal("autoplay:toggle"))

	// Updates
	bot.OnUpdateChatMember(handleParticipantUpdate, nil)
	bot.OnMessage(WithBlacklistMessage(handleActions), actionFilter)
	bot.OnUpdateFile(downloadUpdateHandler, nil)

	assistants.ForEach(func(a *core.Assistant) {
		a.Ntg.OnStreamEnd(streamEndHandler)
		a.Ntg.OnCallDiscarded(callDiscardedHandler)
	})

	go MonitorRooms()
}

// ───────────── filters ─────────────

func getCommand(m *td.Message) string {
	parts := strings.Fields(m.Text())
	if len(parts) == 0 {
		return ""
	}
	cmd, _, _ := strings.Cut(parts[0], "@")
	return cmd
}

func checkOwner(c *td.Client, m *td.Message) bool {
	if config.OwnerID == 0 || m.SenderID() != config.OwnerID {
		if _, err := m.ReplyText(c, F(m.ChatID(), "only_owner"), nil); err != nil {
			logger.Error(err)
		}
		return false
	}
	return true
}

func isSuperGroup(c *td.Client, m *td.Message) bool {
	if m.IsPrivate() {
		if _, err := m.ReplyText(c, F(m.ChatID(), "only_supergroup"), nil); err != nil {
			logger.Error(err)
		}
		database.AddServedUser(m.ChatID())
		return false
	}

	chat, err := m.GetChat(c)
	if err != nil {
		return false
	}

	sg, ok := chat.Type.(*td.ChatTypeSupergroup)
	if !ok || sg.IsChannel {
		return false
	}

	database.AddServedChat(m.ChatID())
	return true
}

func filterAuthUsers(c *td.Client, m *td.Message) bool {
	if canUseAdminCommand(c, m.ChatID(), m.SenderID()) {
		return true
	}

	mode, err := database.GetAdminMode(m.ChatID())
	if err == nil && mode == database.AdminModeAdminsOnly {
		if _, err := m.ReplyText(c, F(m.ChatID(), "only_admin"), nil); err != nil {
			logger.Error(err)
		}
	} else {
		if _, err := m.ReplyText(c, F(m.ChatID(), "only_admin_or_auth"), nil); err != nil {
			logger.Error(err)
		}
	}
	return false
}

func canUseAdminCommand(c *td.Client, chatID, userID int64) bool {
	if isOwnerOrSudo(userID) {
		return true
	}

	mode, err := database.GetAdminMode(chatID)
	if err != nil {
		mode = database.AdminModeAdminAuth
	}

	if mode == database.AdminModeEveryone {
		return true
	}

	isAdmin, err := utils.IsChatAdmin(c, chatID, userID)
	if err == nil && isAdmin {
		return true
	}

	if mode == database.AdminModeAdminsOnly {
		return false
	}

	isAuth, err := database.IsAuthorized(chatID, userID)
	return err == nil && isAuth
}

func isOwnerOrSudo(userID int64) bool {
	if config.OwnerID != 0 && userID == config.OwnerID {
		return true
	}
	isSudo, err := database.IsSudo(userID)
	return err == nil && isSudo
}
