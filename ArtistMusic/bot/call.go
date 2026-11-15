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
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"ArtistMusic/ArtistMusic/config"
	state "ArtistMusic/ArtistMusic/models"
	"ArtistMusic/ArtistMusic/platforms"
	"ArtistMusic/ArtistMusic/queue"
	"ArtistMusic/ArtistMusic/store"
	"ArtistMusic/ArtistMusic/voice"
	"ArtistMusic/internal/artistcalls"
	"ArtistMusic/internal/locales"
	"ArtistMusic/internal/logger"
	"ArtistMusic/internal/utils"
	td "github.com/ArtistMusic/Artist"
)

// ───────────── call ─────────────

var autoplayHistory sync.Map

func getChatHistoryMap(chatID int64) *sync.Map {
	val, ok := autoplayHistory.Load(chatID)
	if !ok {
		m := &sync.Map{}
		autoplayHistory.Store(chatID, m)
		return m
	}
	return val.(*sync.Map)
}

func apMarkPlayed(chatID int64, videoID string) {
	if videoID == "" {
		return
	}
	m := getChatHistoryMap(chatID)
	m.Store(videoID, time.Now())
}

func apIsPlayed(chatID int64, videoID string) bool {
	if videoID == "" {
		return false
	}
	m := getChatHistoryMap(chatID)
	_, exists := m.Load(videoID)
	return exists
}

func apClearChat(chatID int64) {
	autoplayHistory.Delete(chatID)
}

func callDiscardedHandler(chatID int64) {
	r, ok := queue.RoomFor(chatID)
	if !ok {
		return
	}

	logger.Debugf("[callDiscardedHandler] Group call discarded in chat %d", chatID)

	cid := r.ChatID
	apClearChat(chatID)
	queue.DropRoom(chatID)

	if _, err := voice.Bot.SendTextMessage(cid, F(cid, "call_discarded"), nil); err != nil {
		logger.Error(err)
	}
}

func streamEndHandler(
	chatID int64,
	streamType artistcalls.StreamType,
	_ artistcalls.StreamDevice,
) {
	if streamType == artistcalls.VideoStream {
		logger.Debug("[onStreamEndHandler] Video stream ended, returning")
		return
	}

	logger.Debugf("[onStreamEndHandler] Stream ended in chat %d", chatID)
	r, ok := queue.RoomFor(chatID)
	if !ok {
		return
	}

	c := voice.Bot
	cid := r.ChatID
	r.Parse()

	prevTrack := r.Track()

	var t *state.Track
	var wasLooping bool
	var isAutoplay bool

	t = r.NextTrack()
	if t == nil {
		t = tryAutoplay(chatID, r)
		if t == nil {
			apClearChat(chatID)
			queue.DropRoom(chatID)
			if _, err := c.SendTextMessage(cid, F(cid, "stream_queue_finished"), nil); err != nil {
				logger.Error(err)
			}
			return
		}
		t.Requester = "♪ ᴀᴜᴛᴏᴘʟᴀʏ"
		isAutoplay = true
	} else if prevTrack != nil && t == prevTrack {
		wasLooping = true
	}

	statusText := F(cid, "stream_downloading_next")
	if isAutoplay {
		statusText = F(cid, "stream_autoplay_next", locales.Arg{
			"title": utils.EscapeHTML(utils.ShortTitle(t.Title, 25)),
		})
	} else if wasLooping && r.FilePath() != "" {
		statusText = F(cid, "cb_replaying")
	}

	statusMsg, err := c.SendTextMessage(cid, statusText, nil)
	if err != nil {
		logger.Errorf("[call.go] Failed to send msg: %v", err)
	}

	var filePath string
	var dlErr error
	if wasLooping && t != nil && r.FilePath() != "" {
		filePath = r.FilePath()
	} else {
		const maxRetries = 5
		const downloadTimeout = 60 * time.Second
		for attempt := 0; attempt < maxRetries; attempt++ {
			dlCtx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
			filePath, dlErr = platforms.Download(dlCtx, t, statusMsg)
			cancel()
			if dlErr == nil {
				break
			}
			if isAutoplay {
				logger.Warnf("[Autoplay] Download failed (attempt %d/%d) for %q: %v — trying next candidate", attempt+1, maxRetries, t.Title, dlErr)
				nextT := pickAutoplayCandidate(chatID, t)
				if nextT == nil {
					break
				}
				nextT.Requester = "♪ ᴀᴜᴛᴏᴘʟᴀʏ"
				t = nextT
			} else {
				break
			}
		}
	}

	if dlErr != nil {
		logger.Errorf(
			"[onStreamEndHandler] Download failed for %s: %v",
			t.URL,
			dlErr,
		)
		utils.EOR(c, statusMsg, F(cid, "stream_download_fail", locales.Arg{
			"error": dlErr.Error(),
		}), nil)
		queue.DropRoom(chatID)

		return
	}

	if err := r.Play(t, filePath, true); err != nil {
		logger.Errorf(
			"[onStreamEndHandler] Play failed for %s: %v",
			t.URL,
			err,
		)
		utils.EOR(c, statusMsg, F(cid, "stream_play_fail"), nil)
		queue.DropRoom(chatID)

		return
	}

	statusMsg = sendNowPlaying(c, statusMsg, cid, r, t)
	r.SetStatusMsg(statusMsg)

	if isAutoplay {
		go sendAutoplayLogs(c, cid, t, statusMsg)
	}
}

func tryAutoplay(chatID int64, r *queue.RoomState) *state.Track {
	if r == nil || !r.Autoplay() {
		logger.Debugf("[Autoplay] Autoplay is disabled for chat %d", chatID)
		return nil
	}
	return pickAutoplayCandidate(chatID, r.Track())
}

func pickAutoplayCandidate(chatID int64, cur *state.Track) *state.Track {
	if cur == nil || cur.ID == "" {
		logger.Warnf("[Autoplay] Current track is nil or empty for chat %d", chatID)
		return nil
	}

	apMarkPlayed(chatID, cur.ID)

	var candidates []*state.Track

	mixPlaylistID := cur.ID
	if !strings.HasPrefix(mixPlaylistID, "RD") {
		mixPlaylistID = "RD" + cur.ID
	}

	mix, err := platforms.GetYouTubeMixPlaylist(context.Background(), mixPlaylistID)
	if err == nil && len(mix) > 0 {
		for i, t := range mix {
			if i >= 15 {
				break
			}
			if t.ID != "" && t.ID != cur.ID && !apIsPlayed(chatID, t.ID) {
				candidates = append(candidates, t)
			}
		}
	}

	if len(candidates) == 0 {
		cleanTitle := cleanSongTitleForSearch(cur.Title)
		logger.Infof("[Autoplay] Radio mix empty for %s, falling back to smart search: %q", cur.Title, cleanTitle)
		query := fmt.Sprintf("%s similar songs", cleanTitle)
		if searchTracks, sErr := platforms.SearchTracks(query, false); sErr == nil && len(searchTracks) > 0 {
			for _, t := range searchTracks {
				if t.ID != "" && t.ID != cur.ID && !apIsPlayed(chatID, t.ID) {
					candidates = append(candidates, t)
				}
			}
		}
	}

	if len(candidates) == 0 {
		logger.Infof("[Autoplay] All candidates exhausted for chat %d, clearing history and stopping autoplay", chatID)
		apClearChat(chatID)
		return nil
	}

	maxPool := len(candidates)
	if maxPool > 4 {
		maxPool = 4
	}

	n, err := rand.Int(rand.Reader, big.NewInt(int64(maxPool)))
	var chosen *state.Track
	if err != nil {
		chosen = candidates[0]
	} else {
		chosen = candidates[n.Int64()]
	}

	apMarkPlayed(chatID, chosen.ID)
	chosen.Requester = "♪ ᴀᴜᴛᴏᴘʟᴀʏ"
	chosen.IsAutoplay = true
	logger.Infof("[Autoplay] Successfully picked Golden-Zone song: %q (%s) for chat %d", chosen.Title, chosen.ID, chatID)
	return chosen
}

func cleanSongTitleForSearch(title string) string {
	title = strings.Split(title, "|")[0]
	title = strings.Split(title, "(")[0]
	title = strings.Split(title, "[")[0]
	return strings.TrimSpace(title)
}

// ───────────── monitor ─────────────

func MonitorRooms() {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()

	sem := make(chan struct{}, 20)

	for range ticker.C {
		for chatID, room := range queue.AllRooms() {

			sem <- struct{}{}

			go func(chatID int64, r *queue.RoomState) {
				defer func() {
					<-sem
					// A panic here would take the whole process down.
					if rec := recover(); rec != nil {
						logger.Errorf("room monitor panic in chat %d: %v", chatID, rec)
					}
				}()

				if !r.Active() {
					/*
						// TODO: TEST IT AND INCREASE SLEEP TIME
						time.Sleep(5 * time.Second)

						if !r.Active() {
							core.DropRoom(chatID)
							return
						}
					*/
					return
				}

				if r.Paused() {
					return
				}

				r.Parse()

				track := r.Track()
				statusMsg := r.StatusMsg()
				if track == nil || statusMsg == nil {
					return
				}

				text := nowPlayingText(r.ChatID, track)
				markup := GetPlayMarkup(r.ChatID, r, false)

				switch statusMsg.Content.(type) {
				case *td.MessagePhoto:
					_, _ = statusMsg.EditCaption(voice.Bot, text, &td.EditCaptionOpts{
						ParseMode:   td.ParseModeHTML,
						ReplyMarkup: markup,
					})
				default:
					_, _ = statusMsg.EditText(voice.Bot, text, &td.EditTextMessageOpts{
						ParseMode:             td.ParseModeHTML,
						ReplyMarkup:           markup,
						DisableWebPagePreview: true,
					})
				}
			}(chatID, room)
		}
	}
}

// ───────────── participant ─────────────

func memberID(m *td.ChatMember) int64 {
	if m == nil || m.MemberId == nil {
		return 0
	}
	if user, ok := m.MemberId.(*td.MessageSenderUser); ok {
		return user.UserId
	}
	return 0
}

func memberStatus(m *td.ChatMember) string {
	if m == nil || m.Status == nil {
		return "left"
	}

	switch status := m.Status.(type) {
	case *td.ChatMemberStatusCreator:
		return "creator"
	case *td.ChatMemberStatusAdministrator:
		return "administrator"
	case *td.ChatMemberStatusMember:
		return "member"
	case *td.ChatMemberStatusLeft:
		return "left"
	case *td.ChatMemberStatusBanned:
		return "kicked"
	case *td.ChatMemberStatusRestricted:
		if status.IsMember {
			return "member"
		}
		return "kicked"
	default:
		return "unknown"
	}
}

func handleParticipantUpdate(c *td.Client, u *td.UpdateChatMember) error {
	isMaint, _ := store.IsMaintenanceEnabled()
	if isMaint && u.ActorUserId != config.OwnerID {
		if ok, _ := store.IsSudo(u.ActorUserId); !ok {
			return nil
		}
	}

	chatID := u.ChatId
	if chatID == 0 {
		return nil
	}

	userID := memberID(u.NewChatMember)
	if userID == 0 {
		userID = memberID(u.OldChatMember)
	}
	if userID == 0 {
		return nil
	}

	state, err := queue.ChatStateFor(chatID)
	if err != nil {
		logger.Error("Failed to get chat state: " + err.Error())
		state = nil
	}

	oldStatus := memberStatus(u.OldChatMember)
	newStatus := memberStatus(u.NewChatMember)

	if c.Me != nil && userID == c.Me.Id && (newStatus == "left" || newStatus == "kicked") {
		handleBotRemoved(c, u, chatID)
		return nil
	}

	// Bot added to a group or channel (channels send no "members added" message).
	if c.Me != nil && userID == c.Me.Id &&
		(oldStatus == "left" || oldStatus == "kicked") &&
		(newStatus == "member" || newStatus == "administrator" || newStatus == "creator") {
		logChatChange(c, chatID, u.ActorUserId, true)
	}

	switch {
	case (newStatus == "administrator" || newStatus == "creator") &&
		(oldStatus != "administrator" && oldStatus != "creator"):

		utils.AddChatAdmin(c, chatID, userID)

	case oldStatus == "administrator" &&
		newStatus != "administrator" &&
		newStatus != "creator":

		if c.Me != nil && userID == c.Me.Id && config.LeaveOnDemoted {
			queue.DropRoom(chatID)
			queue.DropChatState(chatID)
			if _, rerr := c.SendTextMessage(chatID, F(chatID, "bot_demotion_goodbye"), nil); rerr != nil {
				logger.Error(rerr)
			}
			c.LeaveChat(chatID)

			if state != nil && state.Assistant != nil {
				state.Assistant.Client.LeaveChannel(chatID)
			}

			return nil
		}

		utils.RemoveChatAdmin(c, chatID, userID)

	case oldStatus == "left" &&
		(newStatus == "member" || newStatus == "administrator" || newStatus == "creator"):

		handleSudoJoin(c, chatID, userID)
	}

	if state != nil && state.Assistant != nil && userID == state.Assistant.Self.ID {
		switch newStatus {
		case "member", "administrator", "creator":
			state.SetPresent(true)
			state.SetBanned(false)
			return nil

		case "left":
			state.SetPresent(false)
			state.SetBanned(false)
			return nil

		case "kicked":
			handleAssistantRestriction(c, u, state, chatID)
			return nil
		}

		if !state.Fetched() {
			state.Refresh()
		}
	}

	return nil
}

func handleSudoJoin(c *td.Client, chatID, userID int64) {
	var msgKey string

	if userID == config.OwnerID {
		msgKey = "sudo_join_owner"
	} else {
		isSudo, err := store.IsSudo(userID)
		if err != nil {
			logger.Errorf("IsSudo failed for user %d: %v", userID, err)
			return
		}
		if !isSudo {
			return
		}
		msgKey = "sudo_join_sudo"
	}

	user, _ := c.GetUser(userID)
	userMention := mentionOf(user, userID)

	botMention := "bot"
	if c.Me != nil {
		botMention = mentionOf(c.Me, c.Me.Id)
	}

	text := F(chatID, msgKey, locales.Arg{
		"user": userMention,
		"bot":  botMention,
	})

	if _, err := c.SendTextMessage(chatID, text, nil); err != nil {
		logger.Error(err)
	}
}

func handleAssistantRestriction(
	c *td.Client,
	u *td.UpdateChatMember,
	s *queue.ChatState,
	chatID int64,
) {
	if !isTrueBan(u) {
		s.SetPresent(true)
		s.SetBanned(false)

		logger.Debug("Assistant muted in " + utils.IntToStr(chatID))

		return
	}

	logger.Debug("Assistant banned in " + utils.IntToStr(chatID))

	s.SetPresent(false)
	queue.DropRoom(chatID)

	err := c.SetChatMemberStatus(
		chatID,
		&td.MessageSenderUser{UserId: s.Assistant.Self.ID},
		&td.ChatMemberStatusMember{},
	)
	if err == nil {
		s.SetBanned(false)
	} else {
		s.SetBanned(true)

		msg := F(chatID, "assistant_restricted_warning", locales.Arg{
			"assistant": mentionOfAssistant(s.Assistant),
			"id":        s.Assistant.Self.ID,
		})

		if _, err := c.SendTextMessage(chatID, msg, nil); err != nil {
			logger.Error(err)
		}
	}
}

func isTrueBan(u *td.UpdateChatMember) bool {
	if u.NewChatMember == nil {
		return false
	}

	_, banned := u.NewChatMember.Status.(*td.ChatMemberStatusBanned)
	return banned
}
