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
	"context"
	"fmt"
	"sync"
	"time"

	tg "github.com/amarnathcjd/gogram/telegram"

	"github.com/elevenyts/ArtistMusic/ArtistMusic/config"
	"github.com/elevenyts/ArtistMusic/ArtistMusic/models"
	"github.com/elevenyts/ArtistMusic/ArtistMusic/platforms"
	"github.com/elevenyts/ArtistMusic/ArtistMusic/queue"
	"github.com/elevenyts/ArtistMusic/ArtistMusic/store"
	"github.com/elevenyts/ArtistMusic/ArtistMusic/voice"
)

type Bot struct {
	c   *tg.Client
	db  *store.Store
	eng voice.Engine

	bcMu     sync.Mutex
	bcCancel context.CancelFunc // non-nil while a broadcast is running
}

func New(db *store.Store, eng voice.Engine) (*Bot, error) {
	c, err := tg.NewClient(tg.ClientConfig{
		AppID: config.C.APIID, AppHash: config.C.APIHash, MemorySession: true,
	})
	if err != nil {
		return nil, err
	}
	if err := c.LoginBot(config.C.Token); err != nil {
		return nil, err
	}
	b := &Bot{c: c, db: db, eng: eng}
	eng.OnEnded(b.onEnded)
	b.register()
	return b, nil
}

func (b *Bot) Idle() { b.c.Idle() }

// chat returns the chat state, loading autoplay / nothumb from MongoDB the first time.
func (b *Bot) chat(chatID int64) *queue.Chat {
	ch := queue.Get(chatID)
	if !ch.Loaded {
		ch.Autoplay, ch.NoThumb = b.db.Settings(chatID)
		ch.Loaded = true
	}
	return ch
}

// ───────────── playback core ─────────────

func (b *Bot) startTrack(chatID int64, t *models.Track) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	file, err := platforms.Download(ctx, t)
	if err != nil {
		return err
	}
	if err := b.eng.Play(chatID, file, t.Video); err != nil {
		return err
	}
	ch := b.chat(chatID)
	ch.Current = t
	b.nowPlaying(chatID, t)
	return nil
}

// onEnded runs when a stream finishes: loop → queue → autoplay → stop.
func (b *Bot) onEnded(chatID int64) {
	ch := b.chat(chatID)
	prev := ch.Current
	for {
		next := ch.Next()
		if next == nil && ch.Autoplay && prev != nil {
			if rel, err := platforms.Related(prev.ID, 5); err == nil {
				for _, t := range rel {
					t.Requester = "Autoplay"
				}
				ch.Add(config.C.QueueLimit, rel...)
				next = ch.Next()
			}
		}
		if next == nil {
			_ = b.eng.Stop(chatID)
			ch.Clear()
			return
		}
		if err := b.startTrack(chatID, next); err != nil {
			b.send(chatID, "✗ Could not play <b>"+next.Title+"</b>, skipping.\n"+short(err.Error()))
			prev = next
			continue
		}
		return
	}
}

func (b *Bot) nowPlaying(chatID int64, t *models.Track) {
	ch := b.chat(chatID)
	ap := "off"
	if ch.Autoplay {
		ap = "on"
	}
	who := t.Requester
	if who == "" {
		who = "—"
	}
	text := fmt.Sprintf("➲ <b>NOW STREAMING</b>\n\n🎧 <a href=\"%s\">%s</a>\n⏱ %s | 👤 %s\n\n❤️ POWERED BY ARTIST",
		t.URL, esc(trunc(t.Title, 40)), dur(t.Duration), esc(who))
	if !ch.NoThumb && t.Artwork != "" {
		if b.sendPhoto(chatID, t.Artwork, text, controls(ap)...) {
			return
		}
	}
	b.send(chatID, text, controls(ap)...) // /nothumb on, or photo failed
}

func controls(ap string) [][]button {
	return [][]button{
		{{Text: "⏸", Data: "ctl:pause"}, {Text: "▷", Data: "ctl:resume"}, {Text: "↻", Data: "ctl:replay"}, {Text: "⏭", Data: "ctl:skip"}, {Text: "◻", Data: "ctl:stop"}},
		{{Text: "« 15s", Data: "ctl:back"}, {Text: "15s »", Data: "ctl:fwd"}},
		{{Text: "◉ AUTOPLAY : " + ap, Data: "ctl:autoplay"}, {Text: "✕ CLOSE", Data: "ctl:close"}},
	}
}

// ───────────── small helpers ─────────────

func dur(s int) string {
	if s <= 0 {
		return "live"
	}
	if s >= 3600 {
		return fmt.Sprintf("%dh %dm", s/3600, s%3600/60)
	}
	return fmt.Sprintf("%dm %02ds", s/60, s%60)
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func short(s string) string { return trunc(s, 300) }

func esc(s string) string {
	r := []rune(s)
	out := make([]rune, 0, len(r))
	for _, c := range r {
		switch c {
		case '<':
			out = append(out, []rune("&lt;")...)
		case '>':
			out = append(out, []rune("&gt;")...)
		case '&':
			out = append(out, []rune("&amp;")...)
		default:
			out = append(out, c)
		}
	}
	return string(out)
}
