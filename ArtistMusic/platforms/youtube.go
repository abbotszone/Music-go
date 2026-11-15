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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	state "ArtistMusic/ArtistMusic/core/models"
	"ArtistMusic/ArtistMusic/logger"
	"ArtistMusic/ArtistMusic/utils"
	"ArtistMusic/config"
	"ArtistMusic/config/cookies"
	td "github.com/ArtistMusic/Artist"
)

// ───────────── youtube ─────────────

const (
	PlatformYouTube        state.PlatformName = "YouTube"
	innerTubeKey           string             = "AIzaSyBOti4mM-6x9WDnZIjIeyEU21OpBXqWBgw"
	innerTubeClientVersion string             = "2.20250101.01.00"
	innerTubeClientName    string             = "WEB"
)

type YouTubePlatform struct {
	cache *utils.Cache[string, []*state.Track]
}

var (
	youtubeLinkRe = regexp.MustCompile(
		`(?i)^(?:https?:\/\/)?(?:www\.|m\.|music\.)?(?:youtube\.com|youtu\.be)\/\S+`,
	)
	videoIDRe1 = regexp.MustCompile(
		`(?i)(?:youtube\.com/(?:watch\?v=|embed/|shorts/|live/)|youtu\.be/)([A-Za-z0-9_-]{11})`,
	)
	videoIDRe2    = regexp.MustCompile(`(?:v=|\/)([0-9A-Za-z_-]{11})`)
	playlistIDRe1 = regexp.MustCompile(
		`(?i)(?:youtube\.com|music\.youtube\.com).*(?:\?|&)list=([A-Za-z0-9_-]+)`,
	)
	playlistIDRe2 = regexp.MustCompile(`list=([0-9A-Za-z_-]+)`)
)

func init() {
	Register(&YouTubePlatform{
		cache: utils.NewCache[string, []*state.Track](1 * time.Hour),
	})
}

func (p *YouTubePlatform) Name() state.PlatformName { return PlatformYouTube }
func (p *YouTubePlatform) Priority() int            { return 90 }

func (p *YouTubePlatform) CanGet(query string) bool {
	return youtubeLinkRe.MatchString(query)
}

func (p *YouTubePlatform) Get(input string, video bool) ([]*state.Track, error) {
	query := strings.TrimSpace(input)
	if query == "" {
		return nil, errors.New("empty query")
	}

	var (
		tracks []*state.Track
		err    error
	)

	if !youtubeLinkRe.MatchString(query) {
		tracks, err = p.VideoSearch(query)
	} else {
		playlistID := p.extractPlaylistID(query)
		videoID := p.extractVideoID(query)

		switch {
		case playlistID != "" && videoID != "":
			tracks, err = p.handleCombined(query, videoID)
		case playlistID != "":
			tracks, err = p.handlePlaylist(query)
		default:
			tracks, err = p.handleTrackURL(query)
		}
	}

	if err != nil {
		return nil, err
	}
	if len(tracks) == 0 {
		return nil, errors.New("no tracks found")
	}

	return withVideo(tracks, video), nil
}

func (p *YouTubePlatform) CanDownload(_ state.PlatformName) bool { return false }

func (p *YouTubePlatform) Download(_ context.Context, _ *state.Track, _ *td.Message) (string, error) {
	return "", errors.New("youtube platform does not support downloading")
}

func withVideo(tracks []*state.Track, video bool) []*state.Track {
	out := make([]*state.Track, 0, len(tracks))
	for _, t := range tracks {
		if t == nil {
			continue
		}
		clone := *t
		clone.Video = video
		out = append(out, &clone)
	}
	return out
}

func (p *YouTubePlatform) VideoSearch(query string, single ...bool) ([]*state.Track, error) {
	limit := config.QueueLimit
	onlyOne := len(single) > 0 && single[0]
	if onlyOne {
		limit = 1
	}

	cacheKey := "search:" + strings.ToLower(strings.TrimSpace(query))
	if arr, ok := p.cache.Get(cacheKey); ok {
		if onlyOne && len(arr) > 0 {
			return []*state.Track{arr[0]}, nil
		}
		if !onlyOne && len(arr) > 1 {
			return arr, nil
		}
	}

	tracks, err := p.performSearch(query, limit)
	if err != nil {
		return nil, fmt.Errorf("ytsearch failed: %w", err)
	}
	if len(tracks) == 0 {
		return nil, errors.New("no tracks found")
	}

	p.cache.Set(cacheKey, tracks)

	if onlyOne {
		return []*state.Track{tracks[0]}, nil
	}
	return tracks, nil
}

func (p *YouTubePlatform) handlePlaylist(rawURL string) ([]*state.Track, error) {
	cacheKey := "playlist:" + strings.ToLower(rawURL)
	if cached, ok := p.cache.Get(cacheKey); ok {
		return cached, nil
	}

	playlistID := p.extractPlaylistID(rawURL)
	if playlistID == "" {
		return nil, errors.New("invalid playlist url")
	}

	var (
		tracks []*state.Track
		err    error
	)

	if strings.HasPrefix(playlistID, "RD") {
		tracks, err = p.fetchMixPlaylist(playlistID, config.QueueLimit)
	} else {
		tracks, err = p.fetchPlaylist(playlistID, config.QueueLimit)
	}

	if err != nil {
		return nil, fmt.Errorf("playlist fetch failed: %w", err)
	}

	if len(tracks) > 0 {
		p.cache.Set(cacheKey, tracks)
	}
	return tracks, nil
}

func (p *YouTubePlatform) handleCombined(rawURL, videoID string) ([]*state.Track, error) {
	vTracks, vErr := p.handleTrackURL(rawURL)
	pTracks, pErr := p.handlePlaylist(rawURL)

	if vErr == nil && pErr == nil && len(vTracks) > 0 {
		vid := vTracks[0].ID
		out := []*state.Track{vTracks[0]}
		for _, t := range pTracks {
			if t.ID != vid {
				out = append(out, t)
			}
		}
		return out, nil
	}
	if vErr == nil {
		return vTracks, nil
	}
	if pErr == nil {
		logger.Warnf("[YouTube] video fetch failed for %s: %v", videoID, vErr)
		return pTracks, nil
	}
	return nil, fmt.Errorf("video (%v) and playlist (%v) both failed", vErr, pErr)
}

func (p *YouTubePlatform) handleTrackURL(rawURL string) ([]*state.Track, error) {
	videoID := p.extractVideoID(rawURL)
	if videoID == "" {
		return nil, errors.New("invalid video url")
	}

	if cached, ok := p.cache.Get("track:" + videoID); ok && len(cached) > 0 {
		return cached, nil
	}

	track, err := p.fetchVideo(videoID)
	if err == nil && track != nil {
		p.cache.Set("track:"+videoID, []*state.Track{track})
		return []*state.Track{track}, nil
	}

	for _, q := range []string{videoID, rawURL} {
		results, err := p.VideoSearch(q)
		if err != nil {
			continue
		}
		for _, t := range results {
			if t.ID == videoID {
				p.cache.Set("track:"+videoID, []*state.Track{t})
				return []*state.Track{t}, nil
			}
		}
	}

	if t, err := p.fetchTrackViaOEmbed(videoID); err == nil && t != nil {
		p.cache.Set("track:"+videoID, []*state.Track{t})
		return []*state.Track{t}, nil
	}

	return nil, errors.New("track not found")
}

func (p *YouTubePlatform) fetchTrackViaOEmbed(videoID string) (*state.Track, error) {
	logger.Debugf("[YouTube] oembed fallback: %s", videoID)
	var result map[string]any

	oembedURL := fmt.Sprintf(
		"https://www.youtube.com/oembed?url=https://www.youtube.com/watch?v=%s&format=json",
		videoID,
	)
	resp, err := rc.R().SetResult(&result).Get(oembedURL)
	if err != nil {
		logger.Errorf("[YouTube] oembed request failed for %s: %v", videoID, err)
		return nil, fmt.Errorf("oembed request failed: %w", err)
	}
	if resp.IsStatusFailure() {
		logger.Errorf("[YouTube] oembed returned status %d for %s", resp.StatusCode(), videoID)
		return nil, fmt.Errorf("oembed error: %d", resp.StatusCode())
	}

	title := safeStr(result["title"])
	if title == "" {
		logger.Warnf("[YouTube] oembed: empty title for %s", videoID)
		return nil, errors.New("oembed: title not found")
	}
	logger.Debugf("[YouTube] oembed title for %s: %q", videoID, title)

	queries := []string{title}
	if len(title) > 35 {
		queries = append(queries, title[:35])
	}

	for _, q := range queries {
		logger.Debugf("[YouTube] oembed fallback search query: %q", q)
		tracks, err := p.VideoSearch(q)
		if err != nil {
			logger.Warnf("[YouTube] oembed fallback search failed for %q: %v", q, err)
			continue
		}
		for _, t := range tracks {
			if t.ID == videoID {
				logger.Debugf("[YouTube] oembed fallback matched %s via query %q", videoID, q)
				return t, nil
			}
		}
		logger.Debugf("[YouTube] oembed fallback: no id match for %s in query %q results", videoID, q)
	}

	logger.Warnf("[YouTube] oembed fallback exhausted, no match for %s", videoID)
	return nil, errors.New("no track found")
}

func (p *YouTubePlatform) extractPlaylistID(input string) string {
	if m := playlistIDRe1.FindStringSubmatch(input); len(m) > 1 {
		return m[1]
	}
	if m := playlistIDRe2.FindStringSubmatch(input); len(m) > 1 {
		return m[1]
	}
	return ""
}

func (p *YouTubePlatform) extractVideoID(u string) string {
	if m := videoIDRe1.FindStringSubmatch(u); len(m) > 1 {
		return m[1]
	}
	if m := videoIDRe2.FindStringSubmatch(u); len(m) > 1 {
		return m[1]
	}
	return ""
}

func (p *YouTubePlatform) performSearch(query string, limit int) ([]*state.Track, error) {
	logger.Debugf("[YouTube] search: %s", query)
	var result map[string]any

	payload := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    innerTubeClientName,
				"clientVersion": innerTubeClientVersion,
				"hl":            "en-IN",
				"gl":            "IN",
			},
		},
		"query":  query,
		"params": "CAASAhAB",
	}

	if err := p.callInnerTube("search", payload, &result); err != nil {
		return nil, err
	}

	contents, ok := dig(
		result,
		"contents", "twoColumnSearchResultsRenderer",
		"primaryContents", "sectionListRenderer", "contents",
	).([]any)
	if !ok {
		return nil, errors.New("invalid search results structure")
	}

	var tracks []*state.Track
	p.parseNodes(contents, &tracks, limit, "videoRenderer")
	return tracks, nil
}

func (p *YouTubePlatform) fetchVideo(videoID string) (*state.Track, error) {
	logger.Debugf("[YouTube] fetchVideo: %s", videoID)
	var result map[string]any

	payload := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    innerTubeClientName,
				"clientVersion": innerTubeClientVersion,
			},
		},
		"videoId": videoID,
	}

	if err := p.callInnerTube("player", payload, &result); err != nil {
		return nil, err
	}

	details, ok := dig(result, "videoDetails").(map[string]any)
	if !ok {
		return nil, errors.New("videoDetails not found")
	}

	id := safeStr(details["videoId"])
	return &state.Track{
		URL:      "https://www.youtube.com/watch?v=" + id,
		Title:    safeStr(details["title"]),
		ID:       id,
		Artwork:  getThumbnailURL(result),
		Duration: atoi(safeStr(details["lengthSeconds"])),
		Source:   PlatformYouTube,
	}, nil
}

func (p *YouTubePlatform) fetchPlaylist(playlistID string, limit int) ([]*state.Track, error) {
	logger.Debugf("[YouTube] fetchPlaylist: %s", playlistID)
	var result map[string]any

	browseID := playlistID
	if !strings.HasPrefix(playlistID, "VL") {
		browseID = "VL" + playlistID
	}

	payload := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    innerTubeClientName,
				"clientVersion": innerTubeClientVersion,
			},
		},
		"browseId": browseID,
	}

	if err := p.callInnerTube("browse", payload, &result); err != nil {
		return nil, err
	}

	var tracks []*state.Track
	p.parseNodes(result, &tracks, limit, "playlistVideoRenderer")
	return tracks, nil
}

func (p *YouTubePlatform) fetchMixPlaylist(playlistID string, limit int) ([]*state.Track, error) {
	logger.Debugf("[YouTube] fetchMix: %s", playlistID)
	var result map[string]any

	payload := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    innerTubeClientName,
				"clientVersion": innerTubeClientVersion,
			},
		},
		"playlistId": playlistID,
	}

	if err := p.callInnerTube("next", payload, &result); err != nil {
		return nil, err
	}

	items, ok := dig(
		result,
		"contents", "twoColumnWatchNextResults",
		"playlist", "playlist", "contents",
	).([]any)
	if !ok {
		return nil, errors.New("mix contents not found")
	}

	var tracks []*state.Track
	for _, item := range items {
		if limit > 0 && len(tracks) >= limit {
			break
		}
		vid, ok := dig(item, "playlistPanelVideoRenderer").(map[string]any)
		if !ok {
			continue
		}
		id := safeStr(vid["videoId"])
		if id == "" {
			continue
		}
		t := &state.Track{
			URL:      "https://www.youtube.com/watch?v=" + id,
			Title:    safeStr(dig(vid, "title", "simpleText")),
			ID:       id,
			Artwork:  getThumbnailURL(vid),
			Duration: parseDuration(safeStr(dig(vid, "lengthText", "simpleText"))),
			Source:   PlatformYouTube,
		}
		tracks = append(tracks, t)
		p.cache.Set("track:"+id, []*state.Track{t})
	}

	return tracks, nil
}

func (p *YouTubePlatform) fetchRelatedVideos(videoID string, limit int) ([]*state.Track, error) {
	logger.Debugf("[YouTube] fetchRelated: %s", videoID)
	var result map[string]any

	payload := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    innerTubeClientName,
				"clientVersion": innerTubeClientVersion,
				"hl":            "en-IN",
				"gl":            "IN",
			},
		},
		"videoId": videoID,
	}

	if err := p.callInnerTube("next", payload, &result); err != nil {
		return nil, err
	}

	var tracks []*state.Track
	p.parseNodes(result, &tracks, limit, "compactVideoRenderer")
	if len(tracks) == 0 {
		p.parseNodes(result, &tracks, limit, "videoRenderer")
	}
	return tracks, nil
}

// AutoplayCandidates gathers recommendation candidates using mix, related videos, or search fallback.
func (p *YouTubePlatform) AutoplayCandidates(videoID, query string, limit int) ([]*state.Track, error) {
	var candidates []*state.Track
	seen := make(map[string]bool)

	// 1. Try YouTube Mix playlist
	if videoID != "" {
		if mix, err := p.fetchMixPlaylist("RD"+videoID, limit); err == nil && len(mix) > 0 {
			for _, t := range mix {
				if t != nil && t.ID != "" && !seen[t.ID] {
					seen[t.ID] = true
					candidates = append(candidates, t)
				}
			}
		}
	}

	// 2. Try related watch-next videos
	if len(candidates) < limit && videoID != "" {
		if related, err := p.fetchRelatedVideos(videoID, limit); err == nil && len(related) > 0 {
			for _, t := range related {
				if t != nil && t.ID != "" && !seen[t.ID] {
					seen[t.ID] = true
					candidates = append(candidates, t)
				}
			}
		}
	}

	// 3. Fallback: Search for related query
	if len(candidates) < limit && query != "" {
		searchQuery := query
		if !strings.Contains(strings.ToLower(query), "song") {
			searchQuery = query + " song"
		}
		if searchResults, err := p.performSearch(searchQuery, limit); err == nil && len(searchResults) > 0 {
			for _, t := range searchResults {
				if t != nil && t.ID != "" && !seen[t.ID] {
					seen[t.ID] = true
					candidates = append(candidates, t)
				}
			}
		}
	}

	return candidates, nil
}

func (p *YouTubePlatform) callInnerTube(endpoint string, body, result any) error {
	apiURL := fmt.Sprintf(
		"https://m.youtube.com/youtubei/v1/%s?key=%s",
		endpoint, innerTubeKey,
	)
	resp, err := rc.R().
		SetBody(body).
		SetResult(result).
		SetHeader("Content-Type", "application/json").
		SetHeader("User-Agent", "Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Mobile Safari/537.36").
		Post(apiURL)
	if err != nil {
		return fmt.Errorf("innertube request failed: %w", err)
	}
	if resp.IsStatusFailure() {
		return fmt.Errorf("innertube error: %d", resp.StatusCode())
	}
	return nil
}

func (p *YouTubePlatform) parseNodes(node any, tracks *[]*state.Track, limit int, rendererKey string) {
	if limit > 0 && len(*tracks) >= limit {
		return
	}

	switch v := node.(type) {
	case []any:
		for _, item := range v {
			p.parseNodes(item, tracks, limit, rendererKey)
		}
	case map[string]any:
		if vid, ok := v[rendererKey].(map[string]any); ok {
			if rendererKey == "videoRenderer" && isLiveVideo(vid) {
				return
			}
			id := safeStr(vid["videoId"])
			if id == "" {
				return
			}
			durationText := safeStr(dig(vid, "lengthText", "simpleText"))
			if durationText == "" {
				durationText = safeStr(dig(vid, "lengthText", "runs", 0, "text"))
			}
			title := safeStr(dig(vid, "title", "runs", 0, "text"))
			if title == "" {
				title = safeStr(dig(vid, "title", "simpleText"))
			}
			if title == "" {
				return
			}
			t := &state.Track{
				URL:      "https://www.youtube.com/watch?v=" + id,
				Title:    title,
				ID:       id,
				Artwork:  getThumbnailURL(vid),
				Duration: parseDuration(durationText),
				Source:   PlatformYouTube,
			}
			*tracks = append(*tracks, t)
			p.cache.Set("track:"+id, []*state.Track{t})
		} else {
			for _, val := range v {
				p.parseNodes(val, tracks, limit, rendererKey)
			}
		}
	}
}

func isLiveVideo(vid map[string]any) bool {
	if badges, ok := dig(vid, "badges").([]any); ok {
		for _, b := range badges {
			if safeStr(dig(b, "metadataBadgeRenderer", "style")) == "BADGE_STYLE_TYPE_LIVE_NOW" {
				return true
			}
		}
	}
	return strings.Contains(
		strings.ToLower(safeStr(dig(vid, "viewCountText", "runs", 0, "text"))),
		"watching",
	)
}

func getThumbnailURL(vid map[string]any) string {
	thumbs, ok := dig(vid, "thumbnail", "thumbnails").([]any)
	if !ok {
		thumbs, ok = dig(vid, "videoDetails", "thumbnail", "thumbnails").([]any)
	}
	if ok && len(thumbs) > 0 {
		if last, ok := thumbs[len(thumbs)-1].(map[string]any); ok {
			return safeStr(last["url"])
		}
	}
	return ""
}

func dig(m any, path ...any) any {
	curr := m
	for _, key := range path {
		switch k := key.(type) {
		case string:
			mm, ok := curr.(map[string]any)
			if !ok {
				return nil
			}
			curr = mm[k]
		case int:
			arr, ok := curr.([]any)
			if !ok || k >= len(arr) {
				return nil
			}
			curr = arr[k]
		}
	}
	return curr
}

func safeStr(v any) string {
	s, _ := v.(string)
	return s
}

func parseDuration(s string) int {
	parts := strings.Split(s, ":")
	total, mult := 0, 1
	for i := len(parts) - 1; i >= 0; i-- {
		total += atoi(parts[i]) * mult
		mult *= 60
	}
	return total
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
		}
	}
	return n
}

// ───────────── ytdlp ─────────────

const PlatformYtDlp state.PlatformName = "YtDlp"

type YtdlpPlatform struct{}

type ytdlpInfo struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Duration    float64     `json:"duration"`
	Thumbnail   string      `json:"thumbnail"`
	URL         string      `json:"webpage_url"`
	OriginalURL string      `json:"original_url"`
	Uploader    string      `json:"uploader"`
	IsLive      bool        `json:"is_live"`
	Extractor   string      `json:"extractor"`
	Entries     []ytdlpInfo `json:"entries"`
}

var (
	bannedExtractors = map[string]bool{
		"alphaporno": true, "beeg": true, "behindkink": true, "bongacams": true,
		"cam4": true, "cammodels": true, "camsoda": true, "chaturbate": true,
		"drtuber": true, "eporner": true, "erocast": true, "eroprofile": true,
		"fourtube": true, "goshgay": true, "hellporno": true, "iwara": true,
		"lovehomeporn": true, "manyvids": true, "motherless": true, "murrtube": true,
		"nonktube": true, "noodlemagazine": true, "nubilesporn": true, "nuvid": true,
		"oftv": true, "peekvids": true, "pornbox": true, "pornflip": true,
		"pornhub": true, "pornotube": true, "pornovoisines": true, "pornoxo": true,
		"redgifs": true, "redtube": true, "rule34video": true, "sauceplus": true,
		"sexu": true, "slutload": true, "spankbang": true, "stripchat": true,
		"sunporno": true, "thisvid": true, "tnaflix": true, "toypics": true,
		"txxx": true, "xhamster": true, "xnxx": true, "xvideos": true,
		"xxxymovies": true, "youjizz": true, "youporn": true, "zenporn": true,
	}
	// audioOnlyExtractors are sites without video streams; a video request is
	// silently downgraded to audio.
	audioOnlyExtractors = map[string]bool{
		"soundcloud": true,
	}
	ytURLPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(youtube\.com|youtu\.be|music\.youtube\.com)`),
	}
)

func init() {
	Register(&YtdlpPlatform{})
}

func (y *YtdlpPlatform) Name() state.PlatformName { return PlatformYtDlp }
func (y *YtdlpPlatform) Priority() int            { return 60 }

func (y *YtdlpPlatform) CanGet(query string) bool {
	if _, err := sanitizeMediaURL(query); err != nil {
		return false
	}
	parsed, err := url.Parse(query)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Host)
	return host != "t.me" &&
		host != "telegram.me" &&
		host != "telegram.dog" &&
		!strings.HasSuffix(host, ".t.me")
}

func (y *YtdlpPlatform) Get(query string, video bool) ([]*state.Track, error) {
	safeURL, err := sanitizeMediaURL(query)
	if err != nil {
		return nil, errUnsafeURL
	}

	info, err := y.extractMetadata(safeURL)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	if info.IsLive {
		return nil, errors.New("live streams are not supported")
	}

	if bannedExtractors[strings.ToLower(info.Extractor)] {
		return nil, errors.New("adult content is not allowed")
	}

	var tracks []*state.Track
	if len(info.Entries) > 0 {
		for _, entry := range info.Entries {
			if entry.IsLive || bannedExtractors[strings.ToLower(entry.Extractor)] {
				continue
			}
			tracks = append(tracks, y.toTrack(&entry, video))
		}
	} else {
		tracks = []*state.Track{y.toTrack(info, video)}
	}

	return tracks, nil
}

func (y *YtdlpPlatform) CanDownload(source state.PlatformName) bool {
	return source == PlatformYtDlp || source == PlatformYouTube
}

func (y *YtdlpPlatform) Download(
	ctx context.Context,
	track *state.Track,
	_ *td.Message,
) (string, error) {
	if f := findFileIn(slotDir(ctx), track); f != "" {
		logger.Debug("YtDlp: cache hit " + f)
		return f, nil
	}

	safeURL, err := sanitizeMediaURL(track.URL)
	if err != nil {
		return "", errUnsafeURL
	}

	args := []string{
		"--no-playlist",
		"--no-part",
		"--geo-bypass",
		"--no-warnings",
		"--ignore-errors",
		"--no-check-certificate",
		"-q",
		"-o", getPathIn(slotDir(ctx), track, ".%(ext)s"),
	}

	if track.Video {
		args = append(args,
			"-f", "(b[height>=360][height<=1080]/bv*[height>=360][height<=1080]/bv*)+(ba[abr>=180][abr<=360]/ba)/b",
		)
	} else {
		args = append(args,
			"-f", "ba[abr>=180][abr<=360]/ba",
			"-x",
			"--concurrent-fragments", "4",
		)
	}

	if y.isYouTubeURL(track.URL) && cookieModeOf(ctx) != cookiesOff {
		if cookieFile, err := cookies.GetRandomCookieFile(); err == nil && cookieFile != "" {
			args = append(args, "--cookies", cookieFile)
		}
	}

	args = append(args, "--", safeURL)

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		findAndRemoveIn(slotDir(ctx), track)
		if isDownloadCancelled(err) {
			return "", err
		}
		return "", fmt.Errorf(
			"yt-dlp failed: %w\nstdout: %s\nstderr: %s",
			err, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()),
		)
	}

	p := findFileIn(slotDir(ctx), track)
	if p == "" {
		return "", errors.New("yt-dlp produced no output file")
	}

	logger.Infof("YtDlp: downloaded %s", p)
	return p, nil
}

// ytdlpCookiesAvailable reports whether at least one cookie file is configured.
func ytdlpCookiesAvailable() bool {
	f, err := cookies.GetRandomCookieFile()
	return err == nil && f != ""
}

func (y *YtdlpPlatform) extractMetadata(urlStr string) (*ytdlpInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	args := []string{
		"-j", "--flat-playlist",
		"--no-warnings", "--no-check-certificate",
	}

	if y.isYouTubeURL(urlStr) {
		if cf, err := cookies.GetRandomCookieFile(); err == nil && cf != "" {
			args = append(args, "--cookies", cf)
		}
	}

	args = append(args, "--", urlStr)

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("metadata extraction failed: %w\n%s", err, stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")

	if len(lines) > 1 {
		var info ytdlpInfo
		for _, line := range lines {
			var entry ytdlpInfo
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				logger.Debugf("YtDlp: skip bad entry: %v", err)
				continue
			}
			info.Entries = append(info.Entries, entry)
		}
		if len(info.Entries) == 0 {
			return nil, errors.New("no valid entries in playlist")
		}
		return &info, nil
	}

	var info ytdlpInfo
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return nil, fmt.Errorf("failed to parse metadata JSON: %w", err)
	}
	return &info, nil
}

func (y *YtdlpPlatform) toTrack(info *ytdlpInfo, video bool) *state.Track {
	if video && audioOnlyExtractors[strings.ToLower(info.Extractor)] {
		video = false
	}
	return &state.Track{
		ID:       info.ID,
		Title:    info.Title,
		Duration: int(info.Duration),
		Artwork:  info.Thumbnail,
		URL:      firstNonEmpty(info.OriginalURL, info.URL),
		Source:   PlatformYtDlp,
		Video:    video,
	}
}

func (y *YtdlpPlatform) isYouTubeURL(urlStr string) bool {
	for _, p := range ytURLPatterns {
		if p.MatchString(urlStr) {
			return true
		}
	}
	return false
}

// ───────────── artistbots_api ─────────────

const (
	audioDownloadTimeout = 300 * time.Second
	videoDownloadTimeout = 600 * time.Second

	minValidDownloadBytes = 10_000
)

// PlatformArtistbotsApi downloads YouTube tracks through the Artistbots API:
//
//	GET {ARTISTBOTS_API_URL}/download?url=VIDEO_ID&type=audio|video&api_key=KEY
//
// The endpoint answers with JSON carrying a download link (download_url); the
// file is then fetched from that link. If the endpoint streams the media
// directly instead, the body is saved as-is.
const PlatformArtistbotsApi state.PlatformName = "ArtistbotsApi"

var artistbotsHTTP = &http.Client{}

type ArtistbotsApiPlatform struct{}

func init() {
	Register(&ArtistbotsApiPlatform{})
}

func (a *ArtistbotsApiPlatform) Name() state.PlatformName { return PlatformArtistbotsApi }

// Priority 88: tried before ArtistApi (85) and yt-dlp (60) for YouTube tracks.
func (a *ArtistbotsApiPlatform) Priority() int { return 88 }

func (a *ArtistbotsApiPlatform) CanGet(_ string) bool { return false }

func (a *ArtistbotsApiPlatform) Get(_ string, _ bool) ([]*state.Track, error) {
	return nil, errors.New("artistbotsapi platform does not support search")
}

func (a *ArtistbotsApiPlatform) CanDownload(source state.PlatformName) bool {
	if config.ArtistbotsAPIURL == "" || config.ArtistbotsAPIKey == "" {
		return false
	}
	return source == PlatformYouTube
}

func (a *ArtistbotsApiPlatform) Download(
	ctx context.Context,
	track *state.Track,
	_ *td.Message,
) (string, error) {
	if p := findFileIn(slotDir(ctx), track); p != "" {
		logger.Debug("ArtistbotsApi: cache hit " + p)
		return p, nil
	}

	if track.ID == "" {
		return "", errors.New("artistbotsapi: missing video id")
	}

	dtype, ext, timeout := "audio", ".mp3", audioDownloadTimeout
	if track.Video {
		dtype, ext, timeout = "video", ".mp4", videoDownloadTimeout
	}

	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	path := getPathIn(slotDir(ctx), track, ext)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", errors.New("artistbotsapi: cannot create downloads dir")
	}

	fail := func(err error) (string, error) {
		os.Remove(path)
		if ctx.Err() != nil {
			return "", ctx.Err() // cancelled by the caller
		}
		if dctx.Err() != nil {
			return "", errors.New("artistbotsapi: request timed out")
		}
		return "", scrubArtistbotsErr(err)
	}

	base := strings.TrimRight(config.ArtistbotsAPIURL, "/")
	if !strings.HasSuffix(base, "/download") {
		base += "/download"
	}
	q := url.Values{}
	q.Set("url", track.ID)
	q.Set("type", dtype)
	q.Set("api_key", config.ArtistbotsAPIKey)

	resp, err := artistbotsGet(dctx, base+"?"+q.Encode())
	if err != nil {
		return fail(fmt.Errorf("artistbotsapi request failed: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fail(fmt.Errorf("artistbotsapi returned %d", resp.StatusCode))
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	body := io.Reader(resp.Body)

	if strings.Contains(ct, "json") || strings.HasPrefix(ct, "text/") {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return fail(fmt.Errorf("artistbotsapi read failed: %w", err))
		}

		link, err := parseArtistbotsLink(raw, config.ArtistbotsAPIURL)
		if err != nil {
			return fail(err)
		}

		resp2, err := artistbotsGet(dctx, link)
		if err != nil {
			return fail(fmt.Errorf("artistbotsapi file request failed: %w", err))
		}
		defer resp2.Body.Close()

		if resp2.StatusCode >= 400 {
			return fail(fmt.Errorf("artistbotsapi file returned %d", resp2.StatusCode))
		}
		body = resp2.Body
	}

	out, err := os.Create(path)
	if err != nil {
		return fail(errors.New("artistbotsapi: cannot create file"))
	}
	if _, err := io.Copy(out, body); err != nil {
		out.Close()
		return fail(fmt.Errorf("artistbotsapi download failed: %w", err))
	}
	if err := out.Close(); err != nil {
		return fail(errors.New("artistbotsapi: cannot write file"))
	}

	info, statErr := os.Stat(path)
	if statErr != nil || info.Size() < minValidDownloadBytes {
		return fail(errors.New("artistbotsapi returned an invalid or empty file"))
	}

	return path, nil
}

func artistbotsGet(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	return artistbotsHTTP.Do(req)
}

// parseArtistbotsLink extracts the download link from the API's JSON reply.
func parseArtistbotsLink(raw []byte, apiBase string) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", errors.New("artistbotsapi: unexpected response")
	}

	if ok, has := m["success"].(bool); has && !ok {
		return "", errors.New("artistbotsapi: request rejected")
	}
	if st, has := m["status"].(string); has {
		switch strings.ToLower(st) {
		case "error", "failed", "fail":
			return "", errors.New("artistbotsapi: request failed")
		}
	}

	pick := func(mm map[string]any) string {
		for _, k := range []string{"download_url", "url", "link", "stream_url"} {
			if s, ok := mm[k].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}

	link := pick(m)
	if link == "" {
		if data, ok := m["data"].(map[string]any); ok {
			link = pick(data)
		}
	}
	if link == "" {
		return "", errors.New("artistbotsapi: no download link in response")
	}

	if strings.HasPrefix(link, "/") {
		link = strings.TrimRight(apiBase, "/") + link
	}

	safe, err := sanitizeMediaURL(link)
	if err != nil {
		return "", errors.New("artistbotsapi: unsafe download link")
	}
	return safe, nil
}

// scrubArtistbotsErr removes the API key and base URL from an error message.
func scrubArtistbotsErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, s := range []string{
		config.ArtistbotsAPIKey,
		url.QueryEscape(config.ArtistbotsAPIKey),
		config.ArtistbotsAPIURL,
	} {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "***")
		}
	}
	return errors.New(msg)
}
