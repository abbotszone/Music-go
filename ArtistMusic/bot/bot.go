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

package bot

import (
	"ArtistMusic/ArtistMusic/voice"
	td "github.com/ArtistMusic/Artist"
	"github.com/ArtistMusic/Artist/filters/callbackquery"
)

// ───────────── bot ─────────────

func Init(bot *td.Client, assistants *voice.AssistantManager) {
	assistants.ForEach(func(a *voice.Assistant) {
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

	assistants.ForEach(func(a *voice.Assistant) {
		a.Ntg.OnStreamEnd(streamEndHandler)
		a.Ntg.OnCallDiscarded(callDiscardedHandler)
	})

	go MonitorRooms()
}
