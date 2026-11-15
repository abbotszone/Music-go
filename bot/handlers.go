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

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tg "github.com/amarnathcjd/gogram/telegram"

	"github.com/elevenyts/ArtistMusic/config"
	"github.com/elevenyts/ArtistMusic/platforms"
	"github.com/elevenyts/ArtistMusic/queue"
)

func (b *Bot) register() {
	c := b.c
	c.On("cmd:start", b.cmdStart)
	c.On("cmd:help", b.cmdHelp)
	c.On("cmd:ping", b.cmdPing)
	c.On("cmd:reload", func(m *tg.NewMessage) error { b.reply(m, "✓ Admin cache refreshed."); return nil })

	c.On("cmd:play", func(m *tg.NewMessage) error { return b.cmdPlay(m, false, false) })
	c.On("cmd:vplay", func(m *tg.NewMessage) error { return b.cmdPlay(m, true, false) })
	c.On("cmd:playforce", func(m *tg.NewMessage) error { return b.cmdPlay(m, false, true) })

	c.On("cmd:pause", b.adminCmd(func(id int64, m *tg.NewMessage) string { return b.do(b.eng.Pause(id), "⏸ Paused.") }))
	c.On("cmd:resume", b.adminCmd(func(id int64, m *tg.NewMessage) string { return b.do(b.eng.Resume(id), "▶ Resumed.") }))
	c.On("cmd:mute", b.adminCmd(func(id int64, m *tg.NewMessage) string { return b.do(b.eng.Mute(id), "🔇 Muted.") }))
	c.On("cmd:unmute", b.adminCmd(func(id int64, m *tg.NewMessage) string { return b.do(b.eng.Unmute(id), "🔊 Unmuted.") }))
	c.On("cmd:skip", b.adminCmd(func(id int64, m *tg.NewMessage) string { go b.onEnded(id); return "⏭ Skipped." }))
	c.On("cmd:stop", b.adminCmd(func(id int64, m *tg.NewMessage) string { b.stop(id); return "◻ Stopped and queue cleared." }))
	c.On("cmd:loop", b.adminCmd(func(id int64, m *tg.NewMessage) string {
		n, err := strconv.Atoi(argsOf(m))
		if err != nil || n < 0 || n > 10 {
			return "Usage: /loop [0-10]  (0 = off)"
		}
		queue.Get(id).Loop = n
		return fmt.Sprintf("🔁 Loop set to %d.", n)
	}))
	c.On("cmd:seek", b.adminCmd(func(id int64, m *tg.NewMessage) string { return b.seekBy(id, argsOf(m), 1) }))
	c.On("cmd:seekback", b.adminCmd(func(id int64, m *tg.NewMessage) string { return b.seekBy(id, argsOf(m), -1) }))
	c.On("cmd:queue", b.cmdQueue)

	c.On("cmd:autoplay", b.adminCmd(func(id int64, m *tg.NewMessage) string { return b.toggleAutoplay(id) }))
	c.On("cmd:nothumb", b.adminCmd(func(id int64, m *tg.NewMessage) string {
		ch := queue.Get(id)
		ch.NoThumb = !ch.NoThumb
		b.db.SetNoThumb(id, ch.NoThumb)
		if ch.NoThumb {
			return "Thumbnails disabled for this chat."
		}
		return "Thumbnails enabled for this chat."
	}))

	for _, name := range []string{"broadcast", "bcast", "gcast"} {
		c.On("cmd:"+name, b.cmdBroadcast)
	}

	c.On("callback:ctl:", b.onControl)
	c.On("callback:help:", b.onHelp)

	// remember chats/users so /broadcast has targets
	c.On("message:", func(m *tg.NewMessage) error {
		if isGroup(m.ChatID()) {
			b.db.SeenChat(m.ChatID())
		} else {
			b.db.SeenUser(m.SenderID())
		}
		return nil
	})
}

// adminCmd wraps a group-admin-only command.
func (b *Bot) adminCmd(fn func(chatID int64, m *tg.NewMessage) string) func(*tg.NewMessage) error {
	return func(m *tg.NewMessage) error {
		if !isGroup(m.ChatID()) {
			b.reply(m, "This command works in groups only.")
			return nil
		}
		if !b.isAdmin(m.ChatID(), m.SenderID()) {
			b.reply(m, "✗ Admins only.")
			return nil
		}
		b.reply(m, fn(m.ChatID(), m))
		return nil
	}
}

func (b *Bot) do(err error, ok string) string {
	if err != nil {
		return "✗ " + short(err.Error())
	}
	return ok
}

func (b *Bot) stop(chatID int64) {
	_ = b.eng.Stop(chatID)
	queue.Get(chatID).Clear()
}

func (b *Bot) seekBy(chatID int64, arg string, dir int) string {
	n, err := strconv.Atoi(arg)
	if err != nil || n <= 0 {
		return "Usage: /seek [seconds]"
	}
	to := b.eng.Position(chatID) + dir*n
	if to < 0 {
		to = 0
	}
	return b.do(b.eng.Seek(chatID, to), fmt.Sprintf("⏩ Moved to %ds.", to))
}

func (b *Bot) toggleAutoplay(chatID int64) string {
	ch := queue.Get(chatID)
	ch.Autoplay = !ch.Autoplay
	b.db.SetAutoplay(chatID, ch.Autoplay)
	if ch.Autoplay {
		return "◉ Autoplay enabled."
	}
	return "◎ Autoplay disabled."
}

// ───────────── basic ─────────────

func (b *Bot) cmdStart(m *tg.NewMessage) error {
	name := m.Sender.FirstName
	text := fmt.Sprintf("♡ HEY, <b>%s</b>\n\n♪ <b>Artist Music</b>\n\n✧ Smooth and lag-free music bot with easy queue management and useful features.\n\nClick on the help button for more info.", esc(name))
	me, _ := b.c.GetMe()
	add := "https://t.me/"
	if me != nil {
		add += me.Username + "?startgroup=true"
	}
	rows := [][]button{
		{{Text: "✧ ADD ME TO YOUR CHAT ✧", URL: add}},
		{{Text: "HELP & COMMANDS", Data: "help:menu"}},
		{{Text: "UPDATES", URL: config.C.SupportChannel}, {Text: "SUPPORT", URL: config.C.SupportChat}},
	}
	b.reply(m, text, rows...)
	return nil
}

func (b *Bot) cmdPing(m *tg.NewMessage) error {
	t0 := time.Now()
	msg := b.reply(m, "pinging…")
	if msg != nil {
		b.edit(msg, fmt.Sprintf("🏓 <b>Pong!</b> %d ms", time.Since(t0).Milliseconds()))
	}
	return nil
}

func (b *Bot) cmdQueue(m *tg.NewMessage) error {
	ch := queue.Get(m.ChatID())
	var sb strings.Builder
	if ch.Current != nil {
		sb.WriteString("▶ <b>Now:</b> " + esc(trunc(ch.Current.Title, 45)) + "\n\n")
	}
	items := ch.List()
	if len(items) == 0 {
		sb.WriteString("Queue is empty.")
	}
	for i, t := range items {
		sb.WriteString(fmt.Sprintf("%d. %s · %s\n", i+1, esc(trunc(t.Title, 40)), dur(t.Duration)))
	}
	b.reply(m, sb.String())
	return nil
}

// ───────────── /play /vplay /playforce ─────────────

func (b *Bot) cmdPlay(m *tg.NewMessage, video, force bool) error {
	chatID := m.ChatID()
	if !isGroup(chatID) {
		b.reply(m, "Use this command in a group with an active voice chat.")
		return nil
	}
	q := argsOf(m)
	if q == "" && m.IsReply() {
		if r, err := m.GetReplyMessage(); err == nil {
			q = strings.TrimSpace(r.Text())
		}
	}
	if q == "" {
		b.reply(m, "Usage: /play song name or YouTube link")
		return nil
	}
	if force && !b.isAdmin(chatID, m.SenderID()) {
		b.reply(m, "✗ /playforce is for admins only.")
		return nil
	}

	status := b.reply(m, "🔎 Searching…")
	tracks, err := platforms.GetTracks(q, video)
	if err != nil {
		if status != nil {
			b.edit(status, "✗ "+short(err.Error()))
		}
		return nil
	}
	who := m.Sender.FirstName
	for _, t := range tracks {
		t.Requester = who
	}

	ch := queue.Get(chatID)
	idle := ch.Current == nil
	if !idle && !force {
		n := ch.Add(config.C.QueueLimit, tracks...)
		if status != nil {
			b.edit(status, fmt.Sprintf("➕ Added %d track(s) to the queue.\n<b>%s</b>", n, esc(trunc(tracks[0].Title, 45))))
		}
		return nil
	}
	first := tracks[0]
	ch.Add(config.C.QueueLimit, tracks[1:]...)
	if force {
		_ = b.eng.Stop(chatID)
	}
	if status != nil {
		b.edit(status, "⬇ Preparing <b>"+esc(trunc(first.Title, 45))+"</b>…")
	}
	if err := b.startTrack(chatID, first); err != nil {
		if status != nil {
			b.edit(status, "✗ "+short(err.Error()))
		}
		ch.Clear()
	}
	return nil
}

// ───────────── inline buttons ─────────────

func (b *Bot) onControl(cb *tg.CallbackQuery) error {
	chatID := cb.ChatID
	if !b.isAdmin(chatID, cb.SenderID) {
		cb.Answer("Only group admins can use these buttons.", &tg.CallbackOptions{Alert: true})
		return nil
	}
	var res string
	switch strings.TrimPrefix(cb.DataString(), "ctl:") {
	case "pause":
		res = b.do(b.eng.Pause(chatID), "Paused")
	case "resume":
		res = b.do(b.eng.Resume(chatID), "Resumed")
	case "replay":
		res = b.do(b.eng.Seek(chatID, 0), "Replaying")
	case "skip":
		go b.onEnded(chatID)
		res = "Skipped"
	case "stop":
		b.stop(chatID)
		res = "Stopped"
	case "back":
		res = b.seekBy(chatID, "15", -1)
	case "fwd":
		res = b.seekBy(chatID, "15", 1)
	case "autoplay":
		res = b.toggleAutoplay(chatID)
	case "close":
		_, _ = cb.Delete()
		return nil
	}
	cb.Answer(trunc(res, 150))
	return nil
}

// ───────────── help menu ─────────────

var helpPages = map[string]string{
	"admin": "<b>ADMIN COMMANDS</b>\n\n/pause – halt the active stream\n/resume – restart the paused playback\n/skip – play the next track\n/stop – end the stream and empty the queue\n/mute · /unmute – mute or unmute playback\n/queue – list upcoming tracks\n/loop [1-10] – repeat the current song\n/seek [secs] – forward seek\n/seekback [secs] – reverse seek",
	"bcast": "<b>BROADCAST [owner only]</b>\n\n/broadcast, /bcast, /gcast – all work the same\n\n<b>Use</b>\n/broadcast your message – send text\nReply to any message with /broadcast – send that message\n\n<b>Who gets it</b>\n-user · -group · -all (default) · -nochat · -nouser\n\n<b>Options</b>\n-copy · -pin · -pinloud · -limit N · -delay S (0.7) · -cancel",
	"play":  "<b>PLAY</b>\n\n/play – start audio streaming in chat\n/vplay – start video streaming in chat\n/playforce – instant play (overrides queue)",
	"thumb": "<b>THUMBNAIL SETTINGS</b>\n\n/nothumb – toggle album art display. Disabling it reduces data load and keeps chats minimal.\nSettings are saved per chat.",
	"start": "<b>BASIC COMMANDS</b>\n\n/start – welcome message and menu\n/help – show the command guide\n/ping – latency and system stats\n/reload – refresh the admin cache",
	"auto":  "<b>AUTOPLAY CONTROL</b>\n\n/autoplay – toggle the autoplay panel\n/skip – play the next track in the autoplay queue\n\nAutoplay adds related tracks to the queue so the music never stops. Needs at least one active song.",
	"inline": "<b>BOT PLAY BUTTON DETAILS</b>\n\n⏸ pause · ▷ resume · ↻ replay · ⏭ skip · ◻ stop\n« 15s / 15s » – seek by 15 seconds\n\nAll buttons are for group admins only.",
}

func helpMenu() [][]button {
	return [][]button{
		{{Text: "ADMIN", Data: "help:admin"}, {Text: "B-CAST", Data: "help:bcast"}},
		{{Text: "PLAY", Data: "help:play"}, {Text: "THUMBNAIL", Data: "help:thumb"}},
		{{Text: "START", Data: "help:start"}, {Text: "AUTOPLAY", Data: "help:auto"}},
		{{Text: "INLINE", Data: "help:inline"}},
		{{Text: "BACK", Data: "help:home"}},
	}
}

func (b *Bot) cmdHelp(m *tg.NewMessage) error {
	b.reply(m, "✦ <b>COMMAND GUIDE</b>\n\n• Choose a category below to see its commands", helpMenu()...)
	return nil
}

func (b *Bot) onHelp(cb *tg.CallbackQuery) error {
	key := strings.TrimPrefix(cb.DataString(), "help:")
	switch key {
	case "menu":
		cb.Edit("✦ <b>COMMAND GUIDE</b>\n\n• Choose a category below to see its commands", &tg.SendOptions{ReplyMarkup: keyboard(helpMenu()...)})
	case "home":
		cb.Edit("✦ <b>COMMAND GUIDE</b>\n\n• Choose a category below to see its commands", &tg.SendOptions{ReplyMarkup: keyboard(helpMenu()...)})
	default:
		if page, ok := helpPages[key]; ok {
			cb.Edit(page, &tg.SendOptions{ReplyMarkup: keyboard([]button{{Text: "BACK", Data: "help:menu"}})})
		}
	}
	return nil
}
