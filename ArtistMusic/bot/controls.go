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
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"ArtistMusic/ArtistMusic/platforms"
	"ArtistMusic/ArtistMusic/queue"
	"ArtistMusic/ArtistMusic/voice"
	"ArtistMusic/internal/locales"
	"ArtistMusic/internal/logger"
	"ArtistMusic/internal/utils"
	td "github.com/ArtistMusic/Artist"
)

// ───────────── pause ─────────────

func init() {
	helpTexts["/pause"] = `<i>Pause the current playback.</i>

<u>Usage:</u>
<b>/pause</b> — Pause playback

<b>❖ Features:</b>
• Manual pause/resume control

<b>◇ Examples:</b>
<code>/pause</code> — Pause indefinitely
`
}

func pauseHandler(c *td.Client, m *td.Message) error {
	return handlePause(c, m, false)
}

func handlePause(c *td.Client, m *td.Message, cplay bool) error {
	if !isSuperGroup(c, m) {
		return nil
	}

	if !filterAuthUsers(c, m) {
		return nil
	}

	chatID := m.ChatID()
	r, err := getEffectiveRoom(m.ChatID(), cplay)
	if err != nil {
		_, err := m.ReplyText(c, err.Error(), nil)
		return err
	}

	if !r.Active() {
		_, err := m.ReplyText(c, F(chatID, "room_no_active"), nil)
		return err
	}

	if r.Paused() {
		_, err := m.ReplyText(c, F(chatID, "pause_already"), nil)
		return err
	}

	var pauseErr error
	_, pauseErr = r.Pause()
	if pauseErr != nil {
		_, err := m.ReplyText(c, F(chatID, "room_pause_failed", locales.Arg{
			"error": pauseErr.Error(),
		}), nil)
		return err
	}

	sender, _ := m.GetUser(c)
	mention := mentionOf(sender, m.SenderID())
	title := utils.EscapeHTML(utils.ShortTitle(r.Track().Title, 25))

	msg := F(chatID, "pause_success", locales.Arg{
		"title":    title,
		"position": utils.FormatDuration(r.Position()),
		"duration": utils.FormatDuration(r.Track().Duration),
		"user":     mention,
	})

	if sp := r.Speed(); sp != 1.0 {
		msg += "\n" + F(chatID, "speed_line", locales.Arg{
			"speed": fmt.Sprintf("%.2f", sp),
		})
	}
	_, rerr := m.ReplyText(c, msg, nil)
	return rerr
}

// ───────────── resume ─────────────

func init() {
	helpTexts["/resume"] = `<i>Resume the paused playback.</i>

<u>Usage:</u>
<b>/resume</b> — Resume playback from pause

<b>❖ Behavior:</b>
• Continues from last paused position
• Cancels auto-resume timer if active

<b>ϟ Notes:</b>
• Can only resume if currently paused
• Position is preserved during pause
• Speed settings remain active after resume`
}

func resumeHandler(c *td.Client, m *td.Message) error {
	return handleResume(c, m, false)
}

func handleResume(c *td.Client, m *td.Message, cplay bool) error {
	if !isSuperGroup(c, m) {
		return nil
	}

	if !filterAuthUsers(c, m) {
		return nil
	}

	chatID := m.ChatID()

	r, err := getEffectiveRoom(m.ChatID(), cplay)
	if err != nil {
		_, err := m.ReplyText(c, err.Error(), nil)
		return err
	}

	if !r.Active() {
		_, err := m.ReplyText(c, F(chatID, "room_no_active"), nil)
		return err
	}

	if !r.Paused() {
		_, err := m.ReplyText(c, F(chatID, "resume_already_playing"), nil)
		return err
	}

	t := r.Track()
	if _, err := r.Resume(); err != nil {
		if _, rerr := m.ReplyText(c, F(chatID, "resume_failed", locales.Arg{
			"error": err,
		}), nil); rerr != nil {
			return rerr
		}
	} else {
		title := utils.EscapeHTML(utils.ShortTitle(t.Title, 25))
		pos := utils.FormatDuration(r.Position())
		total := utils.FormatDuration(t.Duration)
		sender, _ := m.GetUser(c)
		mention := mentionOf(sender, m.SenderID())

		speedLine := ""
		if sp := r.Speed(); sp != 1.0 {
			speedLine = F(chatID, "speed_line", locales.Arg{
				"speed": fmt.Sprintf("%.2f", r.Speed()),
			})
		}
		if _, rerr := m.ReplyText(c, F(chatID, "resume_success", locales.Arg{
			"title":      title,
			"position":   pos,
			"duration":   total,
			"user":       mention,
			"speed_line": speedLine,
		}), nil); rerr != nil {
			return rerr
		}
	}

	return nil
}

// ───────────── skip ─────────────

func init() {
	helpTexts["/skip"] = `<i>Skip the currently playing track and play the next in queue.</i>

<u>Usage:</u>
<b>/skip</b> — Skip current track

<b>❖ Behavior:</b>
• Downloads next track in queue
• Starts playback automatically
• If queue is empty and loop is 0, stops playback

<b>◆ Restrictions:</b>
• Only <b>chat admins</b> or <b>authorized users</b> can use this

<b>ϟ Notes:</b>
• Cannot be undone
• If no tracks in queue, playback stops
• Loop count affects skip behavior`

	helpTexts["/askip"] = `<i>Skip the current track and immediately play the next recommended autoplay track.</i>

<u>Usage:</u>
<b>/askip</b> — Skip current track to next autoplay track

<b>❖ Behavior:</b>
• Automatically picks a non-repeating recommendation
• Skips tracks already played in the current session
• Seamlessly continues playback

<b>◆ Restrictions:</b>
• Only <b>chat admins</b> or <b>authorized users</b> can use this`
}

func skipHandler(c *td.Client, m *td.Message) error {
	if !isSuperGroup(c, m) || !filterAuthUsers(c, m) {
		return nil
	}
	return handleSkip(c, m, false)
}

func handleSkip(c *td.Client, m *td.Message, cplay bool) error {
	r, err := getEffectiveRoom(m.ChatID(), cplay)
	if err != nil {
		_, err := m.ReplyText(c, err.Error(), nil)
		return err
	}

	chatID := m.ChatID()
	if !r.Active() {
		_, err := m.ReplyText(c, F(chatID, "room_no_active"), nil)
		return err
	}

	mention := mentionOf(nil, m.SenderID())
	skipCount := 1

	if args := m.Args(); args != "" {
		parsed, parseErr := strconv.Atoi(args)
		if parseErr != nil {
			_, err := m.ReplyText(c, F(chatID, "skip_invalid_number"), nil)
			return err
		}

		queuedTracks := len(r.Queue())
		if queuedTracks == 0 {
			_, err := m.ReplyText(c, F(chatID, "skip_queue_empty_for_count"), nil)
			return err
		}

		if parsed < 1 || parsed > queuedTracks {
			_, err := m.ReplyText(c, F(chatID, "skip_count_exceeds_queue", locales.Arg{
				"requested": parsed,
				"available": queuedTracks,
			}), nil)
			return err
		}

		// /skip N means: skip current + N queued tracks.
		skipCount = parsed + 1
	}

	if len(r.Queue()) == 0 {
		if r.Autoplay() {
			return handleAutoplaySkip(c, m, cplay)
		}
		queue.DropRoom(r.ID)
		_, err := m.ReplyText(c, F(chatID, "skip_stopped", locales.Arg{
			"user": mention,
		}), nil)
		return err
	}

	r.SetLoop(0)

	for i := 1; i < skipCount; i++ {
		if len(r.Queue()) == 0 {

			queue.DropRoom(r.ID)
			_, err := m.ReplyText(c, F(chatID, "skip_stopped", locales.Arg{
				"user": mention,
			}), nil)
			return err
		}
		_ = r.NextTrack()
	}

	if len(r.Queue()) == 0 {

		queue.DropRoom(r.ID)
		_, err := m.ReplyText(c, F(chatID, "skip_stopped", locales.Arg{
			"user": mention,
		}), nil)
		return err
	}

	t := r.NextTrack()
	if t == nil {

		queue.DropRoom(r.ID)
		_, err := m.ReplyText(c, F(chatID, "skip_stopped", locales.Arg{
			"user": mention,
		}), nil)
		return err
	}

	statusMsg, err := voice.Bot.SendTextMessage(
		chatID,
		F(chatID, "stream_downloading_next"),
		nil,
	)
	if err != nil {
		logger.Errorf("[skip.go] err: %v", err)
	}

	path, err := platforms.Download(context.Background(), t, statusMsg)
	if err != nil {
		txt := F(chatID, "stream_download_fail", locales.Arg{
			"error": err.Error(),
		})

		if statusMsg != nil {
			utils.EOR(c, statusMsg, txt, nil)
		} else {
			voice.Bot.SendTextMessage(chatID, txt, nil)
		}

		queue.DropRoom(r.ID)
		return nil
	}

	if err := r.Play(t, path, true); err != nil {
		txt := F(chatID, "stream_play_fail")
		if statusMsg != nil {
			utils.EOR(c, statusMsg, txt, nil)
		} else {
			voice.Bot.SendTextMessage(chatID, txt, nil)
		}
		queue.DropRoom(r.ID)
		return nil
	}

	statusMsg = sendNowPlaying(c, statusMsg, chatID, r, t)
	if statusMsg != nil {
		r.SetStatusMsg(statusMsg)
	}

	return nil
}

func handleAutoplaySkip(c *td.Client, m *td.Message, cplay bool) error {
	r, err := getEffectiveRoom(m.ChatID(), cplay)
	if err != nil {
		_, err := m.ReplyText(c, err.Error(), nil)
		return err
	}

	chatID := m.ChatID()
	if !r.Active() {
		_, err := m.ReplyText(c, F(chatID, "room_no_active"), nil)
		return err
	}

	if !r.Autoplay() {
		_, err := m.ReplyText(c, F(chatID, "askip_disabled"), nil)
		return err
	}

	last := r.Track()
	if last == nil {
		_, err := m.ReplyText(c, F(chatID, "room_no_active"), nil)
		return err
	}

	statusMsg, err := voice.Bot.SendTextMessage(
		chatID,
		F(chatID, "stream_downloading_next"),
		nil,
	)
	if err != nil {
		logger.Errorf("[skip.go] askip send err: %v", err)
	}

	next := PickAutoplayTrack(r, last)
	if next == nil {
		txt := F(chatID, "askip_no_recommendations")
		if statusMsg != nil {
			utils.EOR(c, statusMsg, txt, nil)
		} else {
			_, _ = m.ReplyText(c, txt, nil)
		}
		return nil
	}

	r.SetLoop(0)

	var path string
	var dlErr error
	const maxRetries = 5
	const downloadTimeout = 60 * time.Second
	for attempt := 0; attempt < maxRetries; attempt++ {
		dlCtx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
		path, dlErr = platforms.Download(dlCtx, next, statusMsg)
		cancel()
		if dlErr == nil {
			break
		}
		logger.Warnf("[Autoplay] /askip download failed (attempt %d/%d) for %q: %v — trying next candidate", attempt+1, maxRetries, next.Title, dlErr)
		nextT := pickAutoplayCandidate(chatID, next)
		if nextT == nil {
			break
		}
		next = nextT
	}

	if dlErr != nil {
		txt := F(chatID, "stream_download_fail", locales.Arg{
			"error": dlErr.Error(),
		})
		if statusMsg != nil {
			utils.EOR(c, statusMsg, txt, nil)
		} else {
			voice.Bot.SendTextMessage(chatID, txt, nil)
		}
		queue.DropRoom(r.ID)
		return nil
	}

	if err := r.Play(next, path, true); err != nil {
		txt := F(chatID, "stream_play_fail")
		if statusMsg != nil {
			utils.EOR(c, statusMsg, txt, nil)
		} else {
			voice.Bot.SendTextMessage(chatID, txt, nil)
		}
		queue.DropRoom(r.ID)
		return nil
	}

	statusMsg = sendNowPlaying(c, statusMsg, chatID, r, next)
	if statusMsg != nil {
		r.SetStatusMsg(statusMsg)
	}

	go sendAutoplayLogs(c, chatID, next, statusMsg)

	return nil
}

// ───────────── stop ─────────────

const stopConfirmSuggestionCooldown = 4 * time.Second

func init() {
	helpTexts["/end"] = `<i>Stop playback and leave the voice chat.</i>

<u>Usage:</u>
<b>/stop</b> or <b>/end</b> — Stop playback

<b>❖ Behavior:</b>
• Stops current track
• Clears queue
• Assistant leaves voice chat
•
<b>◆ Restrictions:</b>
• Only <b>chat admins</b> or <b>authorized users</b> can use this

<b>ϟ Note:</b>
This action cannot be undone. Use <code>/pause</code> for temporary stops.`
	helpTexts["/stop"] = helpTexts["/end"]
}

func stopHandler(c *td.Client, m *td.Message) error {
	if !isSuperGroup(c, m) || !filterAuthUsers(c, m) {
		return nil
	}
	return handleStop(c, m, false)
}

func handleStop(c *td.Client, m *td.Message, cplay bool) error {
	r, err := getEffectiveRoom(m.ChatID(), cplay)
	if err != nil {
		_, err := m.ReplyText(c, err.Error(), nil)
		return err
	}
	if !r.Active() {
		_, err := m.ReplyText(c, F(m.ChatID(), "room_no_active"), nil)
		return err
	}

	isPaused := r.Paused()
	isMuted := r.Muted()

	if isPaused || isMuted {
		stopSuggestFloodKey := fmt.Sprintf(
			"stop_suggest:%d",
			r.ID,
		)
		if utils.GetFlood(stopSuggestFloodKey) <= 0 {
			utils.SetFlood(stopSuggestFloodKey, stopConfirmSuggestionCooldown)
			msgKey := "stop_confirm_paused"
			if isMuted {
				msgKey = "stop_confirm_muted"
			}
			_, err := m.ReplyText(c, F(m.ChatID(), msgKey), &td.SendTextMessageOpts{
				ReplyMarkup: GetStopConfirmMarkup(m.ChatID(), r, isPaused),
			})
			return err
		}
	}

	queue.DropRoom(r.ID)
	_, rerr := m.ReplyText(
		c,
		F(
			m.ChatID(),
			"stopped",
			locales.Arg{"user": mentionOf(nil, m.SenderID())},
		),
		nil,
	)
	return rerr
}

// ───────────── seek ─────────────

func init() {
	helpTexts["/seek"] = `<i>Seek forward in the currently playing track.</i>

<u>Usage:</u>
<b>/seek [seconds]</b> — Skip forward by specified seconds

<b>❖ Features:</b>
• Jump ahead in current track
• Position tracking updated
• Cannot seek past track end (10s buffer)

<b>◆ Restrictions:</b>
• Only <b>chat admins</b> or <b>authorized users</b> can use this

<b>◇ Examples:</b>
<code>/seek 30</code> — Skip forward 30 seconds
<code>/seek 120</code> — Skip forward 2 minutes

<b>ϟ Notes:</b>
• Minimum: any positive value
• Maximum: track_duration - current_position - 10 seconds`

	helpTexts["/seekback"] = `<i>Seek backward in the currently playing track.</i>

<u>Usage:</u>
<b>/seekback [seconds]</b> — Go back by specified seconds

<b>◆ Restrictions:</b>
• Only <b>chat admins</b> or <b>authorized users</b> can use this

<b>◇ Examples:</b>
<code>/seekback 15</code> — Go back 15 seconds
<code>/seekback 60</code> — Go back 1 minute
`

	helpTexts["/jump"] = `<i>Jump to a specific position in the track.</i>

<u>Usage:</u>
<b>/jump [seconds]</b> — Jump to exact position

<b>❖ Features:</b>
• Absolute position seeking
• Precise time control
• 10-second buffer from end

<b>◆ Restrictions:</b>
• Only <b>chat admins</b> or <b>authorized users</b> can use this

<b>◇ Examples:</b>
<code>/jump 90</code> — Jump to 1:30
<code>/jump 0</code> — Jump to start (same as /replay)

<b>ϟ Notes:</b>
• Position must be within track duration - 10 seconds
• More precise than <code>/seek</code> and <code>/seekback</code>`
}

func seekHandler(c *td.Client, m *td.Message) error {
	return handleSeek(c, m, false, false)
}

func seekbackHandler(c *td.Client, m *td.Message) error {
	return handleSeek(c, m, false, true)
}

func handleSeek(c *td.Client, m *td.Message, cplay, isBack bool) error {
	if !isSuperGroup(c, m) {
		return nil
	}

	if !filterAuthUsers(c, m) {
		return nil
	}

	r, err := getEffectiveRoom(m.ChatID(), cplay)
	if err != nil {
		_, err := m.ReplyText(c, err.Error(), nil)
		return err
	}
	chatID := m.ChatID()
	t := r.Track()
	if !r.Active() || t == nil {
		_, err := m.ReplyText(c, F(chatID, "seek_no_active"), nil)
		return err
	}

	args := strings.Fields(m.Text())
	if len(args) < 2 {
		_, err := m.ReplyText(c, F(chatID, "seek_usage", locales.Arg{
			"cmd": getCommand(m),
		}), nil)
		return err
	}

	seconds, err := strconv.Atoi(args[1])
	if err != nil {
		_, err := m.ReplyText(c, F(chatID, "seek_invalid_seconds", locales.Arg{
			"cmd": getCommand(m),
		}), nil)
		return err
	}

	var direction, emoji string
	var seekErr error

	if isBack {
		if (r.Position() - seconds) <= 10 {
			_, err := m.ReplyText(c, F(chatID, "seek_too_close_start", locales.Arg{
				"seconds": seconds,
			}), nil)
			return err
		}
		seekErr = r.Seek(-seconds)
		direction = "backward"
		emoji = "«"
	} else {
		if (t.Duration - (r.Position() + seconds)) <= 10 {
			_, err := m.ReplyText(c, F(chatID, "seek_too_close_end", locales.Arg{
				"seconds": seconds,
			}), nil)
			return err
		}
		seekErr = r.Seek(seconds)
		direction = "forward"
		emoji = "»"
	}

	if seekErr != nil {
		if _, rerr := m.ReplyText(c, F(chatID, "seek_failed", locales.Arg{
			"direction": direction,
			"seconds":   seconds,
			"error":     seekErr,
		}), nil); rerr != nil {
			return rerr
		}
	} else {
		if _, rerr := m.ReplyText(c, F(chatID, "seek_success", locales.Arg{
			"emoji":     emoji,
			"direction": direction,
			"position":  utils.FormatDuration(r.Position()),
			"duration":  utils.FormatDuration(t.Duration),
		}), nil); rerr != nil {
			return rerr
		}
	}

	return nil
}

// ───────────── loop ─────────────

func init() {
	helpTexts["/loop"] = `<i>Set loop count for the current track.</i>

<u>Usage:</u>
<b>/loop</b> — Show current loop count
<b>/loop [count]</b> — Set loop count (0-10)

<b>❖ Behavior:</b>
• 0 = No loop (play once)
• 1-10 = Repeat track that many times
• Loop counter decrements after each playback

<b>◇ Examples:</b>
<code>/loop 0</code> — Disable loop
<code>/loop 3</code> — Loop current track 3 times
<code>/loop 10</code> — Loop current track 10 times

<b>ϟ Notes:</b>
• Maximum loop count: 10
• Loop affects only current track
• After loops complete, plays next in queue
• If the track is forcefully skipped using <code>/skip</code>, the loop will stop and reset automatically`
}

func loopHandler(c *td.Client, m *td.Message) error {
	return handleLoop(c, m, false)
}

func handleLoop(c *td.Client, m *td.Message, cplay bool) error {
	if !isSuperGroup(c, m) {
		return nil
	}

	if !filterAuthUsers(c, m) {
		return nil
	}

	r, err := getEffectiveRoom(m.ChatID(), cplay)
	if err != nil {
		_, err := m.ReplyText(c, err.Error(), nil)
		return err
	}
	chatID := m.ChatID()
	args := strings.Fields(m.Text())
	currentLoop := r.Loop()

	if !r.Active() {
		_, err := m.ReplyText(c, F(chatID, "room_no_active"), nil)
		return err
	}

	if len(args) < 2 {
		countLine := ""
		if currentLoop > 0 {
			countLine = "\n" + F(chatID, "loop_current", locales.Arg{
				"count": currentLoop,
			})
		}

		msg := F(chatID, "loop_usage", locales.Arg{
			"cmd":        getCommand(m),
			"count_line": countLine,
		})
		_, err := m.ReplyText(c, msg, nil)
		return err
	}

	newLoop, err := strconv.Atoi(args[1])
	if err != nil || newLoop < 0 || newLoop > 10 {
		_, err := m.ReplyText(c, F(chatID, "loop_invalid"), nil)
		return err
	}

	if newLoop == currentLoop {
		_, err := m.ReplyText(c, F(chatID, "loop_already_set", locales.Arg{
			"count": currentLoop,
		}), nil)
		return err
	}

	r.SetLoop(newLoop)

	sender, _ := m.GetUser(c)
	mention := mentionOf(sender, m.SenderID())
	var msg string
	if newLoop == 0 {
		msg = F(chatID, "loop_disabled", locales.Arg{
			"user": mention,
		})
	} else {
		msg = F(chatID, "loop_set", locales.Arg{
			"count": newLoop,
			"user":  mention,
		})
	}
	_, rerr := m.ReplyText(c, msg, nil)
	return rerr
}

// ───────────── replay ─────────────

func init() {
	helpTexts["/replay"] = `<i>Restart the current track from the beginning.</i>

<u>Usage:</u>
<b>/replay</b> — Restart current track

<b>❖ Behavior:</b>
• Resets position to 0:00
• Maintains speed setting
• Continues playback immediately

<b>◆ Restrictions:</b>
• Only <b>chat admins</b> or <b>authorized users</b> can use this
`
}

func replayHandler(c *td.Client, m *td.Message) error {
	return handleReplay(c, m, false)
}

func handleReplay(c *td.Client, m *td.Message, cplay bool) error {
	if !isSuperGroup(c, m) {
		return nil
	}

	if !filterAuthUsers(c, m) {
		return nil
	}

	chatID := m.ChatID()

	r, err := getEffectiveRoom(m.ChatID(), cplay)
	if err != nil {
		_, err := m.ReplyText(c, err.Error(), nil)
		return err
	}

	if !r.Active() {
		_, err := m.ReplyText(c, F(chatID, "room_no_active"), nil)
		return err
	}
	t := r.Track()

	if err := r.Replay(); err != nil {
		if _, rerr := m.ReplyText(c, F(chatID, "replay_failed", locales.Arg{
			"error": err,
		}), nil); rerr != nil {
			return rerr
		}
	} else {
		trackTitle := utils.EscapeHTML(utils.ShortTitle(t.Title, 25))
		totalDuration := utils.FormatDuration(t.Duration)
		if _, rerr := m.ReplyText(c, F(chatID, "replay_success", locales.Arg{
			"title":    trackTitle,
			"duration": totalDuration,
			"speed":    fmt.Sprintf("%.2f", r.Speed()),
		}), nil); rerr != nil {
			return rerr
		}
	}

	return nil
}

// ───────────── mute ─────────────

func init() {
	helpTexts["/mute"] = `<i>Mute the audio output in voice chat.</i>

<u>Usage:</u>
<b>/mute</b> — Mute indefinitely

<b>❖ Features:</b>
• Audio continues playing (progress tracked)

<b>◇ Examples:</b>
<code>/mute</code> — Mute until manual unmute

<b>ϟ Notes:</b>
• Track continues playing in background
• Use <code>/unmute</code> to restore audio`
}

func muteHandler(c *td.Client, m *td.Message) error {
	return handleMute(c, m, false)
}

func handleMute(c *td.Client, m *td.Message, cplay bool) error {
	if !isSuperGroup(c, m) || !filterAuthUsers(c, m) || m.Args() != "" {
		return nil
	}

	chatID := m.ChatID()
	r, err := getEffectiveRoom(m.ChatID(), cplay)
	if err != nil {
		_, err := m.ReplyText(c, err.Error(), nil)
		return err
	}

	if !r.Active() {
		_, err := m.ReplyText(c, F(chatID, "room_no_active"), nil)
		return err
	}

	if r.Muted() {
		_, err := m.ReplyText(c, F(chatID, "mute_already_muted"), nil)
		return err
	}

	sender, _ := m.GetUser(c)
	mention := mentionOf(sender, m.SenderID())

	_, muteErr := r.Mute()

	if muteErr != nil {
		_, err := m.ReplyText(c, F(chatID, "mute_failed", locales.Arg{
			"error": muteErr.Error(),
		}), nil)
		return err
	}
	_, rerr := m.ReplyText(c, F(chatID, "mute_success", locales.Arg{
		"title": utils.EscapeHTML(utils.ShortTitle(r.Track().Title, 25)),
		"user":  mention,
	}), nil)
	return rerr
}

// ───────────── unmute ─────────────

func init() {
	helpTexts["/unmute"] = `<i>Unmute the audio output in voice chat.</i>

<u>Usage:</u>
<b>/unmute</b> — Restore audio

<b>❖ Behavior:</b>
• Restores audio immediately
• Cancels auto-unmute timer if active
• Shows current playback info`
}

func unmuteHandler(c *td.Client, m *td.Message) error {
	return handleUnmute(c, m, false)
}

func handleUnmute(c *td.Client, m *td.Message, cplay bool) error {
	if m.Args() != "" {
		return nil
	}

	if !isSuperGroup(c, m) {
		return nil
	}

	if !filterAuthUsers(c, m) {
		return nil
	}

	r, err := getEffectiveRoom(m.ChatID(), cplay)
	if err != nil {
		_, err := m.ReplyText(c, err.Error(), nil)
		return err
	}

	chatID := m.ChatID()

	if !r.Active() {
		_, err := m.ReplyText(c, F(chatID, "room_no_active"), nil)
		return err
	}

	if !r.Muted() {
		_, err := m.ReplyText(c, F(chatID, "unmute_already"), nil)
		return err
	}

	title := utils.EscapeHTML(utils.ShortTitle(r.Track().Title, 25))
	sender, _ := m.GetUser(c)
	mention := mentionOf(sender, m.SenderID())

	if _, err := r.Unmute(); err != nil {
		_, err := m.ReplyText(c, F(chatID, "unmute_failed", locales.Arg{
			"error": err.Error(),
		}), nil)
		return err
	}

	// optional speed line
	var speedOpt string
	if sp := r.Speed(); sp != 1.0 {
		speedOpt = F(chatID, "speed_line", locales.Arg{
			"speed": fmt.Sprintf("%.2f", sp),
		})
	}

	msg := F(chatID, "unmute_success", locales.Arg{
		"title":      title,
		"user":       mention,
		"speed_line": speedOpt,
	})
	_, rerr := m.ReplyText(c, msg, nil)
	return rerr
}

// ───────────── reload ─────────────

func init() {
	helpTexts["/reload"] = `<i>Reload admin cache and refresh voice chat state.</i>

<u>Usage:</u>
<b>/reload</b> — Refresh all cached data

<b>◉ What Gets Reloaded:</b>
• Chat admin list
• Voice chat status
• Assistant presence status
• Assistant ban status

<b>◆ Flood Protection:</b>
• Regular users: 5 minute cooldown
• Admins: 2 minute cooldown

<b>◇ When to Use:</b>
• After promoting/demoting admins
• Voice chat issues
• Permission problems
• Bot behaving incorrectly

<b>ϟ Notes:</b>
• May reset room state if admin permissions required
`
}

func reloadHandler(c *td.Client, m *td.Message) error {
	if !isSuperGroup(c, m) {
		return nil
	}
	return handleReload(c, m, false)
}

func handleReload(c *td.Client, m *td.Message, cplay bool) error {
	r, err := getEffectiveRoom(m.ChatID(), cplay)
	if err != nil {
		_, err := m.ReplyText(c, err.Error(), nil)
		return err
	}

	chatID := m.ChatID()
	roomID := r.ID

	if handled, err := checkReloadFlood(c, m, chatID, roomID); handled {
		return err
	}

	statusMsg, err := m.ReplyText(c, F(chatID, "reload_start"), nil)
	if err != nil {
		return err
	}

	summary := ""

	admins, adminSummary := reloadAdminCache(c, chatID, roomID)
	summary += adminSummary

	isAdmin := slices.Contains(admins, m.SenderID())
	floodDuration := utils.IfElse(isAdmin, 2*time.Minute, 5*time.Minute)
	utils.SetFlood(fmt.Sprintf("reload:%d%d", roomID, m.SenderID()), floodDuration)

	cs, err := queue.ChatStateFor(roomID)
	if err != nil {
		summary += F(chatID, "reload_assistant_fail", locales.Arg{
			"error": err.Error(),
		}) + "\n"
		_, _ = utils.EOR(c, statusMsg, F(chatID, "reload_done", locales.Arg{
			"summary": summary,
		}), &td.EditTextMessageOpts{ParseMode: td.ParseModeHTML})
		return nil
	}

	if err := cs.Join(); err != nil {
		logger.Errorf("reload: failed to ensure assistant joined %d: %v", roomID, err)
		summary += F(chatID, "reload_assistant_fail", locales.Arg{
			"error": err.Error(),
		}) + "\n"
	}

	snapshot, snapErr := cs.Refresh()
	summary += reloadVoiceChatStatus(chatID, snapshot, snapErr)
	summary += reloadAssistantStatus(chatID, snapshot, snapErr)

	if isAdmin {
		if queue.DropRoom(roomID) {
			summary += F(chatID, "reload_room_reset") + "\n"
		}
	}

	_, _ = utils.EOR(c, statusMsg, F(chatID, "reload_done", locales.Arg{
		"summary": summary,
	}), &td.EditTextMessageOpts{ParseMode: td.ParseModeHTML})

	return nil
}

func checkReloadFlood(c *td.Client, m *td.Message, chatID, roomID int64) (bool, error) {
	floodKey := fmt.Sprintf("reload:%d%d", roomID, m.SenderID())
	if remaining := utils.GetFlood(floodKey); remaining > 0 {
		_, err := m.ReplyText(c, F(
			chatID,
			"flood_minutes",
			locales.Arg{
				"duration": utils.FormatDuration(int(remaining.Seconds())),
			},
		), nil)
		return true, err
	}
	return false, nil
}

func reloadAdminCache(c *td.Client, chatID, roomID int64) ([]int64, string) {
	admins, err := utils.RefreshChatAdmin(c, roomID)
	if err != nil {
		return nil, F(chatID, "reload_admin_cache_fail", locales.Arg{
			"error": err.Error(),
		}) + "\n"
	}
	return admins, F(chatID, "reload_admin_cache_ok") + "\n"
}

func reloadVoiceChatStatus(chatID int64, snap queue.StateSnapshot, err error) string {
	if err != nil {
		if errors.Is(err, queue.ErrAdminPermissionRequired) {
			return F(chatID, "reload_voice_admin_required") + "\n"
		}
		return F(chatID, "reload_voice_fail", locales.Arg{
			"error": err.Error(),
		}) + "\n"
	}

	if snap.VoiceChatActive != nil && *snap.VoiceChatActive {
		return F(chatID, "reload_voice_active") + "\n"
	}
	return F(chatID, "reload_voice_inactive") + "\n"
}

func reloadAssistantStatus(chatID int64, snap queue.StateSnapshot, err error) string {
	if err != nil {
		if errors.Is(err, queue.ErrAdminPermissionRequired) {
			return F(chatID, "reload_assistant_admin_required") + "\n"
		}
		return F(chatID, "reload_assistant_fail", locales.Arg{
			"error": err.Error(),
		}) + "\n"
	}

	if snap.AssistantBanned {
		return F(chatID, "reload_assistant_banned") + "\n"
	}

	if snap.AssistantPresent {
		return F(chatID, "reload_assistant_present") + "\n"
	}
	return F(chatID, "reload_assistant_not_present") + "\n"
}
