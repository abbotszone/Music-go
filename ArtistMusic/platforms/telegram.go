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

package platforms

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	state "ArtistMusic/ArtistMusic/models"
	"ArtistMusic/ArtistMusic/voice"
	"ArtistMusic/internal/logger"
	"ArtistMusic/internal/utils"
	td "github.com/ArtistMusic/Artist"
)

// ───────────── telegram ─────────────

const PlatformTelegram state.PlatformName = "Telegram"

type TelegramPlatform struct{}

var (
	telegramLinkRegex = regexp.MustCompile(
		`^(?:(?:https?://)?t\.me/((c/)?[\w\d_-]+/\d+|[\w\d_-]+)|@[\w\d_-]+)$`,
	)
	telegramExtractRegex = regexp.MustCompile(
		`^(?:https?://)?t\.me/(c/)?([\w\d_-]+)/(\d+)$`,
	)
	telegramProfileRegex = regexp.MustCompile(
		`^(?:(?:https?://)?t\.me/|@)([\w\d_-]{4,})/?$`,
	)
	telegramMsgCache = utils.NewCache[string, *td.Message](1 * time.Hour)
)

func init() {
	Register(&TelegramPlatform{})
}

func (t *TelegramPlatform) Name() state.PlatformName { return PlatformTelegram }
func (t *TelegramPlatform) Priority() int            { return 100 }

func (t *TelegramPlatform) CanGet(query string) bool {
	return telegramLinkRegex.MatchString(strings.TrimSpace(query))
}

func (t *TelegramPlatform) Get(query string, _ bool) ([]*state.Track, error) {
	query = strings.TrimSpace(query)

	if telegramExtractRegex.MatchString(query) {
		msg, err := fetchTelegramMessage(query)
		if err != nil {
			return nil, err
		}

		_, isVideo, isAudio := playableMedia(voice.Bot, msg)
		if !isVideo && !isAudio {
			return nil, errors.New("message does not contain playable media")
		}

		track, err := t.GetTracksByMessage(voice.Bot, msg)
		if err != nil {
			return nil, err
		}
		track.Video = isVideo
		return []*state.Track{track}, nil
	}

	if matches := telegramProfileRegex.FindStringSubmatch(query); len(matches) >= 2 {
		return nil, errors.New("telegram profile music is not supported")
	}

	return nil, errors.New("invalid Telegram link")
}

// fetchTelegramMessage resolves a t.me message link via GetMessageLinkInfo
// and returns the linked message.
func fetchTelegramMessage(query string) (*td.Message, error) {
	if !strings.HasPrefix(query, "http://") && !strings.HasPrefix(query, "https://") {
		query = "https://" + query
	}

	info, err := voice.Bot.GetMessageLinkInfo(query)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve message link: %w", err)
	}
	if info == nil || info.Message == nil {
		return nil, errors.New("could not fetch message from link")
	}

	return info.Message, nil
}

func (t *TelegramPlatform) CanDownload(source state.PlatformName) bool {
	return source == PlatformTelegram
}

func (t *TelegramPlatform) Download(
	ctx context.Context,
	track *state.Track,
	statusMsg *td.Message,
) (string, error) {
	ext := ".mp3"
	if track.Video {
		ext = ".mp4"
	}
	path := getPath(track, ext)

	if fileExists(path) {
		if track.Duration == 0 {
			if dur, err := utils.GetDurationByFFProbe(path); err == nil {
				track.Duration = dur
			}
		}
		return path, nil
	}

	msg, msgOk := telegramMsgCache.Get(track.ID)
	if !msgOk {
		fetched, fetchErr := fetchTelegramMessage(track.URL)
		if fetchErr == nil {
			msg = fetched
			msgOk = true
		}
	}
	if !msgOk {
		return "", errors.New("failed to locate file")
	}

	fileID := msg.RemoteFileID()
	if OnDownloadStart != nil {
		OnDownloadStart(fileID, statusMsg)
	}
	file, err := msg.Download(voice.Bot, 1, 0, 0, true)
	if err != nil {
		os.Remove(path)
		if isDownloadCancelled(err) {
			return "", context.Canceled
		}
		return "", fmt.Errorf("download failed: %w", err)
	}
	if file == nil || file.Local == nil || file.Local.Path == "" {
		return "", errors.New("downloaded file missing")
	}

	if err := copyFile(file.Local.Path, path); err != nil {
		return "", fmt.Errorf("copy failed: %w", err)
	}
	os.Remove(file.Local.Path)

	if track.Duration == 0 {
		if dur, err := utils.GetDurationByFFProbe(path); err == nil {
			track.Duration = dur
		}
	}

	return path, nil
}

func (t *TelegramPlatform) GetTracksByMessage(c *td.Client, msg *td.Message) (*state.Track, error) {
	if msg == nil {
		return nil, errors.New("nil message")
	}

	if msg.RemoteFileID() == "" && msg.ReplyToMessageID() > 0 {
		r, err := msg.GetRepliedMessage(c)
		if err == nil && r.RemoteFileID() != "" {
			msg = r
		}
	}

	if msg.RemoteFileID() == "" {
		return nil, fmt.Errorf("ϟ This message doesn't contain any media")
	}

	title := "Telegram File"
	switch content := msg.Content.(type) {
	case *td.MessageAudio:
		if content.Audio != nil {
			title = firstNonEmpty(content.Audio.Title, content.Audio.FileName, title)
		}
	case *td.MessageVideo:
		if content.Video != nil {
			title = firstNonEmpty(content.Video.FileName, title)
		}
	}

	url := ""
	if link, err := msg.GetLink(c); err == nil && link != nil {
		url = link.Link
	}

	track := &state.Track{
		ID:       msg.RemoteFileID(),
		Title:    title,
		Duration: utils.GetDuration(msg),
		URL:      url,
		Source:   PlatformTelegram,
	}
	telegramMsgCache.Set(track.ID, msg)

	return track, nil
}

// ───────────── directstream ─────────────

const PlatformDirectStream state.PlatformName = "DirectStream"

type DirectStreamPlatform struct {
	mu    sync.Mutex
	cache map[string]*cachedStream
}

type cachedStream struct {
	info    *streamInfo
	expires time.Time
}

type streamInfo struct {
	URL         string
	ContentType string
	Size        int64
	IsAudio     bool
	IsVideo     bool
	Duration    int
}

var (
	streamExtensions = []string{
		".m3u8", ".m3u", ".mp3", ".mp4", ".webm", ".ogg",
		".wav", ".flac", ".aac", ".opus", ".mkv", ".avi",
		".mov", ".ts", ".mpd",
	}
	streamPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\.m3u8(\?|$)`),
		regexp.MustCompile(`(?i)\.mpd(\?|$)`),
		regexp.MustCompile(`(?i)/hls/`),
		regexp.MustCompile(`(?i)/dash/`),
		regexp.MustCompile(`(?i)/stream/`),
	}
	streamMimeTypes = []string{
		"audio/", "video/",
		"application/vnd.apple.mpegurl",
		"application/x-mpegurl",
		"application/dash+xml",
		"application/octet-stream",
	}
	streamHosts = []string{"cdn", "stream", "media", "video", "audio"}
)

func init() {
	Register(&DirectStreamPlatform{
		cache: make(map[string]*cachedStream),
	})
}

func (d *DirectStreamPlatform) Name() state.PlatformName { return PlatformDirectStream }
func (d *DirectStreamPlatform) Priority() int            { return 65 }

func (d *DirectStreamPlatform) CanGet(query string) bool {
	if _, err := sanitizeMediaURL(query); err != nil {
		return false
	}
	return d.looksLikeStream(query)
}

func (d *DirectStreamPlatform) Get(query string, video bool) ([]*state.Track, error) {
	safeURL, err := sanitizeMediaURL(query)
	if err != nil {
		return nil, errUnsafeURL
	}

	info, err := d.getStreamInfo(safeURL)
	if err != nil {
		return nil, fmt.Errorf("stream validation failed: %w", err)
	}

	track := &state.Track{
		ID:       d.generateID(safeURL),
		Title:    d.extractTitle(safeURL),
		Duration: info.Duration,
		URL:      safeURL,
		Source:   PlatformDirectStream,
		Video:    video || info.IsVideo,
	}

	if info.IsVideo {
		track.Video = true
	}

	if info.Size > 0 && track.Duration == 0 {
		bitrate := int64(128000)
		if info.IsVideo {
			bitrate = 500000
		}
		dur := int((info.Size * 8) / bitrate)
		if dur <= 3600 {
			track.Duration = dur
		}
	}

	logger.Infof("DirectStream: %s (audio:%v video:%v size:%d dur:%d)",
		track.Title, info.IsAudio, info.IsVideo, info.Size, track.Duration)

	return []*state.Track{track}, nil
}

func (d *DirectStreamPlatform) CanDownload(source state.PlatformName) bool {
	return source == PlatformDirectStream
}

func (d *DirectStreamPlatform) Download(
	_ context.Context,
	track *state.Track,
	_ *td.Message,
) (string, error) {
	logger.Infof("DirectStream: returning URL for streaming: %s", track.URL)
	return track.URL, nil
}

func (d *DirectStreamPlatform) getStreamInfo(urlStr string) (*streamInfo, error) {
	d.mu.Lock()
	if c, ok := d.cache[urlStr]; ok && time.Now().Before(c.expires) {
		d.mu.Unlock()
		return c.info, nil
	}
	d.mu.Unlock()

	info, err := d.validateStream(urlStr)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	d.cache[urlStr] = &cachedStream{info: info, expires: time.Now().Add(5 * time.Minute)}
	d.mu.Unlock()

	return info, nil
}

func (d *DirectStreamPlatform) looksLikeStream(urlStr string) bool {
	parsed, _ := url.Parse(urlStr)
	ext := strings.ToLower(path.Ext(parsed.Path))
	if slices.Contains(streamExtensions, ext) {
		return true
	}
	for _, p := range streamPatterns {
		if p.MatchString(urlStr) {
			return true
		}
	}
	host := strings.ToLower(parsed.Host)
	for _, sh := range streamHosts {
		if strings.Contains(host, sh) {
			return true
		}
	}
	return false
}

func (d *DirectStreamPlatform) validateStream(urlStr string) (*streamInfo, error) {
	client := rc.
		SetTimeout(10*time.Second).
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.R().Head(urlStr)
	if err != nil || resp.StatusCode() >= 400 {
		resp, err = client.R().
			SetHeader("Range", "bytes=0-1024").
			Get(urlStr)
		if err != nil {
			return nil, fmt.Errorf("failed to validate URL: %w", err)
		}
	}

	if resp.StatusCode() >= 400 {
		return nil, fmt.Errorf("URL returned status %d", resp.StatusCode())
	}

	ct := strings.ToLower(resp.Header().Get("Content-Type"))
	isAudio, isVideo := false, false

	for _, mt := range streamMimeTypes {
		if strings.HasPrefix(ct, mt) {
			switch {
			case strings.HasPrefix(mt, "audio/"):
				isAudio = true
			case strings.HasPrefix(mt, "video/"):
				isVideo = true
			default:
				isAudio = true
			}
			break
		}
	}

	if !isAudio && !isVideo {
		return nil, fmt.Errorf("not audio/video (content-type: %s)", ct)
	}

	size := int64(0)
	if cl := resp.Header().Get("Content-Length"); cl != "" {
		size, _ = strconv.ParseInt(cl, 10, 64)
	}

	return &streamInfo{
		URL:         urlStr,
		ContentType: ct,
		Size:        size,
		IsAudio:     isAudio,
		IsVideo:     isVideo,
	}, nil
}

func (d *DirectStreamPlatform) generateID(urlStr string) string {
	h := 0
	for _, c := range urlStr {
		h = (h << 5) - h + int(c)
	}
	if h < 0 {
		h = -h
	}
	return fmt.Sprintf("direct_%d", h)
}

func (d *DirectStreamPlatform) extractTitle(urlStr string) string {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return "Direct Stream"
	}
	name := path.Base(parsed.Path)
	if idx := strings.LastIndex(name, "."); idx > 0 {
		name = name[:idx]
	}
	name = strings.NewReplacer("_", " ", "-", " ").Replace(name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "index" || name == "stream" {
		name = parsed.Host
	}
	if len(name) > 0 {
		name = strings.ToUpper(name[:1]) + name[1:]
	}
	return name
}
