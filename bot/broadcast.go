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
	"strconv"
	"strings"
	"time"

	tg "github.com/amarnathcjd/gogram/telegram"

	"github.com/elevenyts/ArtistMusic/config"
)

type bcOpts struct {
	users, groups bool
	copyMsg       bool
	pin, pinLoud  bool
	limit         int
	delay         time.Duration
	text          string
}

// parseBroadcast reads flags (-flag or --flag) from the front of the args; the rest is the text.
func parseBroadcast(args string) (o bcOpts, cancel bool) {
	o = bcOpts{users: true, groups: true, delay: 700 * time.Millisecond}
	f := strings.Fields(args)
	var rest []string
	for i := 0; i < len(f); i++ {
		w := strings.ToLower(strings.TrimLeft(f[i], "-"))
		isFlag := strings.HasPrefix(f[i], "-")
		switch {
		case !isFlag:
			rest = append(rest, f[i])
		case w == "cancel":
			return o, true
		case w == "user":
			o.users, o.groups = true, false
		case w == "group":
			o.users, o.groups = false, true
		case w == "all":
			o.users, o.groups = true, true
		case w == "nochat":
			o.groups = false
		case w == "nouser":
			o.users = false
		case w == "copy":
			o.copyMsg = true
		case w == "pin":
			o.pin = true
		case w == "pinloud":
			o.pin, o.pinLoud = true, true
		case w == "limit" && i+1 < len(f):
			i++
			o.limit, _ = strconv.Atoi(f[i])
		case w == "delay" && i+1 < len(f):
			i++
			if s, err := strconv.ParseFloat(f[i], 64); err == nil && s >= 0 {
				o.delay = time.Duration(s * float64(time.Second))
			}
		default:
			rest = append(rest, f[i])
		}
	}
	o.text = strings.Join(rest, " ")
	return o, false
}

func (b *Bot) cmdBroadcast(m *tg.NewMessage) error {
	if m.SenderID() != config.C.OwnerID {
		return nil
	}
	o, cancel := parseBroadcast(argsOf(m))

	b.bcMu.Lock()
	if cancel {
		if b.bcCancel != nil {
			b.bcCancel()
			b.reply(m, "Cancelling broadcast…")
		} else {
			b.reply(m, "No broadcast is running.")
		}
		b.bcMu.Unlock()
		return nil
	}
	if b.bcCancel != nil {
		b.bcMu.Unlock()
		b.reply(m, "A broadcast is already running. Use /broadcast -cancel to stop it.")
		return nil
	}
	ctx, stop := context.WithCancel(context.Background())
	b.bcCancel = stop
	b.bcMu.Unlock()
	defer func() { stop(); b.bcMu.Lock(); b.bcCancel = nil; b.bcMu.Unlock() }()

	var src *tg.NewMessage
	if m.IsReply() {
		src, _ = m.GetReplyMessage()
	}
	if src == nil && o.text == "" {
		b.reply(m, "Give me a message or reply to one.")
		return nil
	}

	var targets []int64
	if o.groups {
		targets = append(targets, b.db.ChatIDs()...)
	}
	if o.users {
		targets = append(targets, b.db.UserIDs()...)
	}
	if o.limit > 0 && len(targets) > o.limit {
		targets = targets[:o.limit]
	}

	status := b.reply(m, fmt.Sprintf("📣 Broadcasting to %d chats…", len(targets)))
	sent, failed := 0, 0
	reasons := map[string]int{}
	for i, id := range targets {
		if ctx.Err() != nil {
			break
		}
		var err error
		if src != nil {
			err = b.copyTo(id, src, o.copyMsg)
		} else {
			_, err = b.c.SendMessage(id, o.text, &tg.SendOptions{LinkPreview: false})
		}
		if err != nil {
			failed++
			reasons[trunc(err.Error(), 50)]++
		} else {
			sent++
		}
		if i%30 == 29 { // auto pause after every 30 sends
			select {
			case <-ctx.Done():
			case <-time.After(7500 * time.Millisecond):
			}
		} else {
			select {
			case <-ctx.Done():
			case <-time.After(o.delay):
			}
		}
	}

	report := fmt.Sprintf("📣 Broadcast done.\n✓ Sent: %d\n✗ Failed: %d", sent, failed)
	if ctx.Err() != nil {
		report = "📣 Broadcast cancelled.\n✓ Sent: " + strconv.Itoa(sent) + "\n✗ Failed: " + strconv.Itoa(failed)
	}
	for r, n := range reasons {
		report += fmt.Sprintf("\n• %s ×%d", r, n)
	}
	if status != nil {
		b.edit(status, report)
	}
	return nil
}
