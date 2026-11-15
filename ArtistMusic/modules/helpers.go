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
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"ArtistMusic/ArtistMusic/core"
	state "ArtistMusic/ArtistMusic/core/models"
	"ArtistMusic/ArtistMusic/database"
	"ArtistMusic/ArtistMusic/locales"
	"ArtistMusic/ArtistMusic/logger"
	"ArtistMusic/ArtistMusic/utils"
	"ArtistMusic/config"
	td "github.com/ArtistMusic/Artist"
)

// ───────────── helpers ─────────────

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
		"title":    utils.EscapeHTML(utils.ShortTitle(t.Title, 25)),
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

// sendLog posts an HTML message to the logger chat. It is a no-op when no
// logger chat is configured or the owner switched the logger off.
func sendLog(text string) {
	if config.LoggerID == 0 || core.Bot == nil || !isLoggerEnabled() {
		return
	}
	if _, err := core.Bot.SendTextMessage(
		config.LoggerID,
		text,
		&td.SendTextMessageOpts{ParseMode: "HTML", DisableWebPagePreview: true},
	); err != nil {
		logger.Errorf("failed to send log message: %v", err)
	}
}

// recentChatLogs de-duplicates add/remove logs: adding the bot to a group
// raises both a "members added" service message and a chat-member update.
var recentChatLogs sync.Map

// logChatChange reports the bot being added to / removed from a group or
// channel. actorID is the user who did it (0 if unknown).
func logChatChange(c *td.Client, chatID, actorID int64, added bool) {
	if config.LoggerID == 0 || config.LoggerID == chatID {
		return
	}

	key := fmt.Sprintf("%t:%d", added, chatID)
	if t, ok := recentChatLogs.Load(key); ok && time.Since(t.(time.Time)) < time.Minute {
		return
	}
	recentChatLogs.Store(key, time.Now())

	title, kind := "N/A", "ɢʀσᴜᴘ"
	if chat, err := c.GetChat(chatID); err == nil && chat != nil {
		title = utils.EscapeHTML(chat.Title)
		if sg, ok := chat.Type.(*td.ChatTypeSupergroup); ok && sg.IsChannel {
			kind = "ᴄʜᴧηηєʟ"
		}
	}

	byName, byMention := "N/A", "N/A"
	if actorID != 0 {
		if actor, err := c.GetUser(actorID); err == nil && actor != nil {
			if n := strings.TrimSpace(actor.FirstName + " " + actor.LastName); n != "" {
				byName = n
			}
			byMention = mentionOf(actor, actorID)
		}
	}

	msgKey := "logger_bot_removed"
	if added {
		msgKey = "logger_bot_added"
	}
	sendLog(F(config.LoggerID, msgKey, locales.Arg{
		"chat_type":  kind,
		"chat_title": title,
		"chat_id":    chatID,
		"by_name":    byName,
		"by_id":      actorID,
		"by_mention": byMention,
		"date_time":  time.Now().Format("02 Jan 2006 • 15:04"),
	}))
}

func sendPlayLogs(c *td.Client, m *td.Message, track *state.Track, queued bool) {
	if config.LoggerID == 0 || config.LoggerID == m.ChatID() || !isLoggerEnabled() {
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

// ───────────── thumbnail ─────────────

func init() {
	helpTexts["/nothumb"] = `<i>Toggle thumbnail/artwork display in playback messages.</i>

<u>Usage:</u>
<b>/nothumb</b> — Show current status
<b>/nothumb [enable|disable]</b> — Change setting

<b>❖ Behavior:</b>
• <b>Disabled (default):</b> Shows track artwork/thumbnail
• <b>Enabled:</b> Hides artwork, text-only messages

<b>◇ Examples:</b>
<code>/nothumb enable</code> — Disable thumbnails
<code>/nothumb disable</code> — Enable thumbnails

<b>ϟ Note:</b>
This setting affects all future playback messages in this chat.`
}

func nothumbHandler(c *td.Client, m *td.Message) error {
	if !isSuperGroup(c, m) || !filterAuthUsers(c, m) {
		return nil
	}
	chatID := m.ChatID()
	args := strings.Fields(m.Text())

	current, err := database.ThumbnailsDisabled(chatID)
	if err != nil {
		_, err := m.ReplyText(c, F(chatID, "nothumb_fetch_fail"), nil)
		return err
	}

	if len(args) < 2 {
		action := utils.IfElse(!current, "enabled", "disabled")
		_, err := m.ReplyText(c, F(chatID, "nothumb_status", locales.Arg{
			"cmd":    getCommand(m),
			"action": action,
		}), nil)
		return err
	}

	value, err := utils.ParseBool(args[1])
	if err != nil {
		_, err := m.ReplyText(c, F(chatID, "invalid_bool"), nil)
		return err
	}

	if current == value {
		action := utils.IfElse(!value, "enabled", "disabled")
		_, err := m.ReplyText(c, F(chatID, "nothumb_already", locales.Arg{
			"action": action,
		}), nil)
		return err
	}

	if err := database.SetThumbnailsDisabled(chatID, value); err != nil {
		_, err := m.ReplyText(c, F(chatID, "nothumb_update_fail"), nil)
		return err
	}

	action := utils.IfElse(!value, "enabled", "disabled")
	_, rerr := m.ReplyText(c, F(chatID, "nothumb_updated", locales.Arg{
		"action": action,
	}), nil)
	return rerr
}

// ───────────── thumb169 ─────────────

// Every track thumbnail sent to a chat is kept at 16:9.
//
//   - YouTube thumbnails are swapped for the 16:9 variants of the same video
//     (maxresdefault, hq720 or mqdefault), never the 4:3 hqdefault.
//   - Any other artwork (Spotify/Deezer covers, yt-dlp sites, ...) that is not
//     already 16:9 is placed on a blurred 1280x720 background, so square
//     covers keep their shape and are never stretched or cropped.
//
// If anything fails the original artwork is used, so playback never breaks.

const (
	thumb169W      = 1280
	thumb169H      = 720
	thumb169MaxMem = 1000
)

var (
	thumb169YouTubeRe = regexp.MustCompile(`/vi(?:_webp)?/([A-Za-z0-9_-]{11})/`)
	thumb169HTTP      = &http.Client{Timeout: 8 * time.Second}
	thumb169Mu        sync.Mutex
	thumb169Memo      = make(map[string]string)
)

// thumb169 returns the photo to send for the given artwork URL.
func thumb169(artwork string) td.InputFile {
	thumb169Mu.Lock()
	cached, ok := thumb169Memo[artwork]
	thumb169Mu.Unlock()

	if ok && thumb169Usable(cached) {
		return thumb169ToInput(cached)
	}

	res := thumb169Resolve(artwork)

	thumb169Mu.Lock()
	if len(thumb169Memo) >= thumb169MaxMem {
		thumb169Memo = make(map[string]string)
	}
	thumb169Memo[artwork] = res
	thumb169Mu.Unlock()

	return thumb169ToInput(res)
}

func thumb169IsRemote(v string) bool {
	return strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://")
}

// thumb169Usable reports whether a cached result can still be used.
func thumb169Usable(v string) bool {
	if thumb169IsRemote(v) {
		return true
	}
	_, err := os.Stat(v)
	return err == nil
}

func thumb169ToInput(v string) td.InputFile {
	if thumb169IsRemote(v) {
		return td.InputFileRemote{Id: v}
	}
	return td.InputFileLocal{Path: v}
}

func thumb169Resolve(artwork string) string {
	clean := utils.CleanURL(artwork)

	if m := thumb169YouTubeRe.FindStringSubmatch(clean); m != nil {
		return thumb169BestYouTube(m[1])
	}

	data, err := thumb169Fetch(clean)
	if err != nil {
		logger.Warnf("thumb169: fetch failed for %s: %v", clean, err)
		return clean
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// Unknown format (for example webp): send it unchanged.
		return clean
	}

	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return clean
	}

	ratio := float64(b.Dx()) / float64(b.Dy())
	if ratio >= 1.70 && ratio <= 1.85 {
		return clean
	}

	path, err := thumb169Save(thumb169Compose(img), clean)
	if err != nil {
		logger.Warnf("thumb169: compose failed for %s: %v", clean, err)
		return clean
	}
	return path
}

// thumb169BestYouTube picks the best 16:9 thumbnail that exists for a video.
func thumb169BestYouTube(id string) string {
	for _, name := range []string{"maxresdefault", "hq720"} {
		u := "https://i.ytimg.com/vi/" + id + "/" + name + ".jpg"
		if thumb169Exists(u) {
			return u
		}
	}
	// mqdefault (320x180) exists for every video and is always 16:9.
	return "https://i.ytimg.com/vi/" + id + "/mqdefault.jpg"
}

func thumb169Exists(u string) bool {
	req, err := http.NewRequest(http.MethodHead, u, nil)
	if err != nil {
		return false
	}
	resp, err := thumb169HTTP.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func thumb169Fetch(u string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ArtistMusic)")

	resp, err := thumb169HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// thumb169Compose draws src centered on a blurred, dimmed 16:9 background.
func thumb169Compose(src image.Image) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, thumb169W, thumb169H))
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()

	// Background: centre-crop the source to 16:9, shrink it, then stretch it
	// back up. The bilinear upscale gives a soft blur.
	cropW, cropH := sw, sh
	if float64(sw)*thumb169H > float64(sh)*thumb169W {
		cropW = sh * thumb169W / thumb169H
	} else {
		cropH = sw * thumb169H / thumb169W
	}
	if cropW < 1 {
		cropW = 1
	}
	if cropH < 1 {
		cropH = 1
	}
	cx := sb.Min.X + (sw-cropW)/2
	cy := sb.Min.Y + (sh-cropH)/2

	small := image.NewRGBA(image.Rect(0, 0, 32, 18))
	thumb169Scale(small, small.Bounds(), src, image.Rect(cx, cy, cx+cropW, cy+cropH))
	thumb169Scale(dst, dst.Bounds(), small, small.Bounds())

	for i := 0; i+3 < len(dst.Pix); i += 4 {
		dst.Pix[i] = uint8(float64(dst.Pix[i]) * 0.45)
		dst.Pix[i+1] = uint8(float64(dst.Pix[i+1]) * 0.45)
		dst.Pix[i+2] = uint8(float64(dst.Pix[i+2]) * 0.45)
	}

	// Foreground: the original artwork, fitted inside the frame.
	maxW := thumb169W * 88 / 100
	maxH := thumb169H * 88 / 100
	fh := maxH
	fw := sw * fh / sh
	if fw > maxW {
		fw = maxW
		fh = sh * fw / sw
	}
	if fw < 1 {
		fw = 1
	}
	if fh < 1 {
		fh = 1
	}
	x0 := (thumb169W - fw) / 2
	y0 := (thumb169H - fh) / 2
	thumb169Scale(dst, image.Rect(x0, y0, x0+fw, y0+fh), src, sb)

	return dst
}

// thumb169Scale resizes the region sr of src into the region dr of dst using
// bilinear sampling.
func thumb169Scale(dst *image.RGBA, dr image.Rectangle, src image.Image, sr image.Rectangle) {
	dw, dh := dr.Dx(), dr.Dy()
	srcW, srcH := sr.Dx(), sr.Dy()
	if dw <= 0 || dh <= 0 || srcW <= 0 || srcH <= 0 {
		return
	}

	for y := 0; y < dh; y++ {
		fy := (float64(y)+0.5)*float64(srcH)/float64(dh) - 0.5
		y0 := int(math.Floor(fy))
		ty := fy - float64(y0)

		for x := 0; x < dw; x++ {
			fx := (float64(x)+0.5)*float64(srcW)/float64(dw) - 0.5
			x0 := int(math.Floor(fx))
			tx := fx - float64(x0)

			r00, g00, b00 := thumb169Pixel(src, sr, x0, y0)
			r10, g10, b10 := thumb169Pixel(src, sr, x0+1, y0)
			r01, g01, b01 := thumb169Pixel(src, sr, x0, y0+1)
			r11, g11, b11 := thumb169Pixel(src, sr, x0+1, y0+1)

			r := thumb169Mix(r00, r10, r01, r11, tx, ty)
			g := thumb169Mix(g00, g10, g01, g11, tx, ty)
			b := thumb169Mix(b00, b10, b01, b11, tx, ty)

			i := dst.PixOffset(dr.Min.X+x, dr.Min.Y+y)
			dst.Pix[i] = uint8(r + 0.5)
			dst.Pix[i+1] = uint8(g + 0.5)
			dst.Pix[i+2] = uint8(b + 0.5)
			dst.Pix[i+3] = 255
		}
	}
}

func thumb169Mix(c00, c10, c01, c11, tx, ty float64) float64 {
	top := c00*(1-tx) + c10*tx
	bottom := c01*(1-tx) + c11*tx
	return top*(1-ty) + bottom*ty
}

func thumb169Pixel(src image.Image, sr image.Rectangle, x, y int) (float64, float64, float64) {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x >= sr.Dx() {
		x = sr.Dx() - 1
	}
	if y >= sr.Dy() {
		y = sr.Dy() - 1
	}
	r, g, b, _ := src.At(sr.Min.X+x, sr.Min.Y+y).RGBA()
	return float64(r >> 8), float64(g >> 8), float64(b >> 8)
}

// thumb169Save writes the image to the cache folder and returns its absolute path.
func thumb169Save(img image.Image, key string) (string, error) {
	dir := filepath.Join("cache", "thumbs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	sum := sha1.Sum([]byte(key))
	path, err := filepath.Abs(filepath.Join(dir, "thumb_"+hex.EncodeToString(sum[:8])+".jpg"))
	if err != nil {
		return "", err
	}

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}

	thumb169Prune(dir)
	return path, nil
}

// thumb169Prune deletes generated thumbnails older than two hours.
func thumb169Prune(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-2 * time.Hour)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "thumb_") {
			continue
		}
		info, err := e.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
