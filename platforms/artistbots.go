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

package platforms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/elevenyts/ArtistMusic/config"
	"github.com/elevenyts/ArtistMusic/models"
)

// ArtistbotsAPI downloads YouTube tracks through
//
//	GET {ARTISTBOTS_API_URL}/download?url=VIDEO_ID&type=audio|video&api_key=KEY
//
// Priority 88 so it is listed before yt-dlp; Download() races them anyway.
type ArtistbotsAPI struct{}

func init() { Register(&ArtistbotsAPI{}) }

func (a *ArtistbotsAPI) Name() models.PlatformName { return "ArtistbotsApi" }
func (a *ArtistbotsAPI) Priority() int             { return 88 }
func (a *ArtistbotsAPI) CanGet(string) bool        { return false }
func (a *ArtistbotsAPI) Get(string, bool) ([]*models.Track, error) {
	return nil, errors.New("not a search platform")
}
func (a *ArtistbotsAPI) CanDownload(s models.PlatformName) bool {
	return s == PlatformYouTube && config.C.ArtistbotsURL != "" && config.C.ArtistbotsKey != ""
}

func (a *ArtistbotsAPI) Download(ctx context.Context, t *models.Track) (string, error) {
	typ := "audio"
	if t.Video {
		typ = "video"
	}
	q := url.Values{"url": {t.ID}, "type": {typ}, "api_key": {config.C.ArtistbotsKey}}
	resp, err := get(ctx, config.C.ArtistbotsURL+"/download?"+q.Encode())
	if err != nil {
		return "", scrub(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("artistbotsapi returned %d", resp.StatusCode)
	}

	body := resp.Body
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "json") || strings.HasPrefix(ct, "text/") {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		link, err := parseLink(raw)
		if err != nil {
			return "", err
		}
		r2, err := get(ctx, link)
		if err != nil {
			return "", scrub(err)
		}
		defer r2.Body.Close()
		if r2.StatusCode >= 400 {
			return "", fmt.Errorf("artistbotsapi file returned %d", r2.StatusCode)
		}
		body = r2.Body
	}

	dir := dirOf(ctx)
	_ = os.MkdirAll(dir, 0o755)
	ext := ".m4a"
	if t.Video {
		ext = ".mp4"
	}
	path := filepath.Join(dir, prefix(t)+ext)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	_, cerr := io.Copy(f, body)
	f.Close()
	if cerr != nil {
		return "", scrub(cerr)
	}
	if !validFile(path) {
		return "", errors.New("artistbotsapi returned an invalid or empty file")
	}
	return path, nil
}

func get(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	return http.DefaultClient.Do(req)
}

func parseLink(raw []byte) (string, error) {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return "", errors.New("artistbotsapi: unexpected response")
	}
	if ok, has := m["success"].(bool); has && !ok {
		return "", errors.New("artistbotsapi: request rejected")
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
		if d, ok := m["data"].(map[string]any); ok {
			link = pick(d)
		}
	}
	if link == "" {
		return "", errors.New("artistbotsapi: no download link in response")
	}
	if strings.HasPrefix(link, "/") {
		link = config.C.ArtistbotsURL + link
	}
	if u, err := url.Parse(link); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("artistbotsapi: unsafe download link")
	}
	return link, nil
}

// scrub removes the API key / base URL from error text so they never reach chat or logs.
func scrub(err error) error {
	msg := err.Error()
	for _, s := range []string{config.C.ArtistbotsKey, url.QueryEscape(config.C.ArtistbotsKey), config.C.ArtistbotsURL} {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "***")
		}
	}
	return errors.New(msg)
}
