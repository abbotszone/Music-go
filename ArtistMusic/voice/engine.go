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

// Package voice is the only place that talks to Telegram voice chats.
//
// The rest of the bot depends on the Engine interface, so you can plug in
// whichever ntgcalls binding you use (the userbot session in STRING_SESSIONS
// joins the call). LogEngine below lets the whole bot run end to end
// (commands, queue, downloads, buttons) while that adapter is wired.
package voice

import (
	"log"
	"sync"
)

type Engine interface {
	// Play streams a local file (or URL) into the chat's voice chat; video=true streams video too.
	Play(chatID int64, src string, video bool) error
	Pause(chatID int64) error
	Resume(chatID int64) error
	Mute(chatID int64) error
	Unmute(chatID int64) error
	Seek(chatID int64, toSec int) error
	Stop(chatID int64) error
	// Position returns the current playback position in seconds.
	Position(chatID int64) int
	// OnEnded registers the callback fired when a stream finishes.
	OnEnded(fn func(chatID int64))
}

// LogEngine is a no-audio placeholder that only logs.
type LogEngine struct {
	mu    sync.Mutex
	ended func(int64)
}

func (l *LogEngine) Play(c int64, src string, v bool) error { log.Printf("[voice] play %d %s video=%v", c, src, v); return nil }
func (l *LogEngine) Pause(c int64) error                    { log.Printf("[voice] pause %d", c); return nil }
func (l *LogEngine) Resume(c int64) error                   { log.Printf("[voice] resume %d", c); return nil }
func (l *LogEngine) Mute(c int64) error                     { log.Printf("[voice] mute %d", c); return nil }
func (l *LogEngine) Unmute(c int64) error                   { log.Printf("[voice] unmute %d", c); return nil }
func (l *LogEngine) Seek(c int64, s int) error              { log.Printf("[voice] seek %d -> %ds", c, s); return nil }
func (l *LogEngine) Stop(c int64) error                     { log.Printf("[voice] stop %d", c); return nil }
func (l *LogEngine) Position(int64) int                     { return 0 }
func (l *LogEngine) OnEnded(fn func(int64))                 { l.mu.Lock(); l.ended = fn; l.mu.Unlock() }
