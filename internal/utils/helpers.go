/*
 * ● ArtistMusic
 * ○ A high-performance engine for streaming music in Telegram voicechats.
 *
 * Copyright (C) 2024 ArtistMusic
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

package utils

import (
	"fmt"
	"html"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"

	"ArtistMusic/internal/logger"
	td "github.com/ArtistMusic/Artist"
)

// ───────────── format ─────────────

// FormatBytes returns a human-readable byte size (e.g. "1.2 MB").
func FormatBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

// FormatDuration returns a duration string (e.g., "1d 2h", "3h 4m", "5m 6s").
func FormatDuration(sec int) string {
	if sec < 0 {
		sec = 0
	}

	const (
		day  = 86400
		hour = 3600
		min  = 60
	)

	if sec < min {
		return strconv.Itoa(sec) + "s"
	}
	if sec < hour {
		return strconv.Itoa(sec/min) + "m " + strconv.Itoa(sec%min) + "s"
	}
	if sec < day {
		return strconv.Itoa(sec/hour) + "h " + strconv.Itoa((sec%hour)/min) + "m"
	}

	return strconv.Itoa(sec/day) + "d " + strconv.Itoa((sec%day)/hour) + "h"
}

// FormatTime returns a clock-style duration string (e.g., "HH:MM:SS" or "MM:SS").
func FormatTime(sec int) string {
	if sec < 0 {
		sec = 0
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60

	if h > 0 {
		return strconv.Itoa(h) + ":" + padZero(m) + ":" + padZero(s)
	}
	return padZero(m) + ":" + padZero(s)
}

func padZero(v int) string {
	if v < 10 {
		return "0" + strconv.Itoa(v)
	}
	return strconv.Itoa(v)
}

// ───────────── parser ─────────────

func ShortTitle(title string, max ...int) string {
	title = html.UnescapeString(title)
	limit := 25
	if len(max) > 0 {
		limit = max[0]
	}
	runes := []rune(title)
	if len(runes) <= limit {
		return title
	}
	return string(runes[:limit]) + "..."
}

var htmlReplacer = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

// EscapeHTML escapes characters for Telegram HTML mode (only &, <, >).
func EscapeHTML(s string) string {
	return htmlReplacer.Replace(s)
}

func CleanURL(raw string) string {
	before, _, _ := strings.Cut(raw, "?")
	return before
}

// IfElse returns `a` if condition is true, else returns `b`.
func IfElse[T any](condition bool, a, b T) T {
	if condition {
		return a
	}
	return b
}

// ParseBool converts strings like "on", "off", "enable", "disable", "true", "false"
// into a boolean value. Returns an error if input is invalid.
func ParseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "enable", "enabled", "true", "1", "yes", "y":
		return true, nil
	case "off", "disable", "disabled", "false", "0", "no", "n":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean string: %q", s)
	}
}

// IntToStr converts any signed integer type to string.
func IntToStr[T ~int | ~int8 | ~int16 | ~int32 | ~int64](v T) string {
	return strconv.FormatInt(int64(v), 10)
}

// ───────────── extract_url ─────────────

// ExtractURLs collects URLs from the given message and, if it is a reply,
// from the replied message too. Both message text and media captions are
// inspected, since Telegram entities cover both.
func ExtractURLs(c *td.Client, m *td.Message) ([]string, error) {
	if m == nil {
		return nil, fmt.Errorf("invalid message")
	}

	urls := collectURLsFromMessage(m)

	if m.ReplyToMessageID() <= 0 {
		return finalizeURLs(urls)
	}

	r, err := m.GetRepliedMessage(c)
	if err != nil {
		if len(urls) > 0 {
			return urls, fmt.Errorf("failed to fetch reply message: %w", err)
		}
		return nil, fmt.Errorf("failed to fetch reply message: %w", err)
	}

	urls = append(urls, collectURLsFromMessage(r)...)
	return finalizeURLs(urls)
}

// --- Sub Functions ---

// collectURLsFromMessage extracts URLs from both the message text and its
// caption, as either can carry entities.
func collectURLsFromMessage(m *td.Message) []string {
	if m == nil {
		return nil
	}
	urls := collectURLs(m.Text(), m.Entities())
	urls = append(urls, collectURLs(m.Caption(), m.CaptionEntities())...)
	return urls
}

func collectURLs(text string, entities []td.TextEntity) []string {
	urls := make([]string, 0, len(entities))

	for _, ent := range entities {
		switch e := ent.Type.(type) {
		case *td.TextEntityTypeUrl:
			if seg, ok := sliceUTF16(text, ent.Offset, ent.Length); ok {
				urls = append(urls, seg)
			}
		case *td.TextEntityTypeTextUrl:
			if e.Url != "" {
				urls = append(urls, e.Url)
			}
		}
	}
	return urls
}

// sliceUTF16 slices the given string using UTF-16 code unit offsets,
// which is what Telegram text entities use.
func sliceUTF16(s string, offset, length int32) (string, bool) {
	if offset < 0 || length < 0 {
		return "", false
	}
	u16 := utf16.Encode([]rune(s))
	start := int(offset)
	end := start + int(length)
	if end > len(u16) {
		return "", false
	}
	return string(utf16.Decode(u16[start:end])), true
}

func finalizeURLs(urls []string) ([]string, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("no URLs found")
	}

	seen := make(map[string]struct{}, len(urls))
	deduped := make([]string, 0, len(urls))
	for _, u := range urls {
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		deduped = append(deduped, u)
	}
	return deduped, nil
}

// ───────────── edit_or_reply ─────────────

func callerInfo(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown:0"
	}
	return fmt.Sprintf("%s:%d", file, line)
}

// EOR edits a message, falling back to delete + reply on edit failure.
func EOR(
	c *td.Client,
	msg *td.Message,
	text string,
	opts *td.EditTextMessageOpts,
) (*td.Message, error) {
	if msg == nil {
		logger.Error("[EOR] nil msg at " + callerInfo(2))
		return nil, nil
	}

	m, err := msg.EditText(c, text, opts)
	if err != nil {
		_ = msg.Delete(c, true)
		replyOpts := &td.SendTextMessageOpts{}
		if opts != nil {
			replyOpts.ParseMode = opts.ParseMode
			replyOpts.ReplyMarkup = opts.ReplyMarkup
			replyOpts.DisableWebPagePreview = opts.DisableWebPagePreview
		}
		m, err = msg.ReplyText(c, text, replyOpts)
	}

	if err != nil {
		logger.Error(
			"[EOR] " + err.Error() + " | called from " + callerInfo(2),
		)
	}
	return m, err
}
