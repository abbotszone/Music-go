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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/elevenyts/ArtistMusic/config"
	"github.com/elevenyts/ArtistMusic/models"
)

const (
	PlatformYouTube models.PlatformName = "YouTube"
	cookiesFile                         = "cookies.txt"
)

var ytRe = regexp.MustCompile(`(?i)^(?:https?://)?(?:www\.|m\.|music\.)?(?:youtube\.com|youtu\.be)/\S+`)
var idRe = regexp.MustCompile(`(?i)(?:v=|youtu\.be/|shorts/|live/|embed/)([A-Za-z0-9_-]{11})`)

// YtDlp resolves YouTube links / searches and downloads through yt-dlp.
type YtDlp struct{}

func init() { Register(&YtDlp{}) }

func (y *YtDlp) Name() models.PlatformName { return PlatformYouTube }
func (y *YtDlp) Priority() int             { return 60 }

// Any text that is not another platform's URL is treated as a YouTube search.
func (y *YtDlp) CanGet(q string) bool { return true }

func (y *YtDlp) CanDownload(s models.PlatformName) bool { return s == PlatformYouTube }

func (y *YtDlp) Download(ctx context.Context, t *models.Track) (string, error) {
	return y.download(ctx, t, cookiesAvailable())
}

type ytInfo struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Duration  float64  `json:"duration"`
	Thumbnail string   `json:"thumbnail"`
	URL       string   `json:"webpage_url"`
	Thumbs    []struct{ URL string `json:"url"` } `json:"thumbnails"`
}

func (y *YtDlp) Get(q string, video bool) ([]*models.Track, error) {
	target := q
	if !ytRe.MatchString(q) {
		target = "ytsearch1:" + q
	}
	args := []string{"--dump-json", "--no-warnings", "--flat-playlist", "--playlist-end", fmt.Sprint(config.C.QueueLimit)}
	if cookiesAvailable() {
		args = append(args, "--cookies", cookiesFile)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "yt-dlp", append(args, target)...).Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp metadata failed: %w", err)
	}
	return parseInfos(out, video), nil
}

func parseInfos(out []byte, video bool) []*models.Track {
	var tracks []*models.Track
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var i ytInfo
		if json.Unmarshal(sc.Bytes(), &i) != nil || i.ID == "" {
			continue
		}
		if config.C.DurationLimit > 0 && int(i.Duration) > config.C.DurationLimit {
			continue
		}
		art := i.Thumbnail
		if art == "" && len(i.Thumbs) > 0 {
			art = i.Thumbs[len(i.Thumbs)-1].URL
		}
		tracks = append(tracks, &models.Track{
			ID: i.ID, Title: i.Title, Artwork: art, Duration: int(i.Duration),
			URL: "https://www.youtube.com/watch?v=" + i.ID, Source: PlatformYouTube, Video: video,
		})
	}
	return tracks
}

// Related returns autoplay candidates (YouTube Mix of the given video).
func Related(videoID string, limit int) ([]*models.Track, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "yt-dlp", "--dump-json", "--flat-playlist", "--no-warnings",
		"--playlist-end", fmt.Sprint(limit+1),
		"https://www.youtube.com/watch?v="+videoID+"&list=RD"+videoID).Output()
	if err != nil {
		return nil, err
	}
	var res []*models.Track
	for _, t := range parseInfos(out, false) {
		if t.ID != videoID {
			res = append(res, t)
		}
	}
	return res, nil
}

func (y *YtDlp) download(ctx context.Context, t *models.Track, cookies bool) (string, error) {
	dir := dirOf(ctx)
	_ = os.MkdirAll(dir, 0o755)
	format := "bestaudio[ext=m4a]/bestaudio/best"
	if t.Video {
		format = "bestvideo[height<=720]+bestaudio/best[height<=720]/best"
	}
	args := []string{"-f", format, "--no-playlist", "--no-warnings", "--concurrent-fragments", "4",
		"-o", filepath.Join(dir, prefix(t)+".%(ext)s")}
	if t.Video {
		args = append(args, "--merge-output-format", "mp4")
	}
	if cookies && cookiesAvailable() {
		args = append(args, "--cookies", cookiesFile)
	}
	cmd := exec.CommandContext(ctx, "yt-dlp", append(args, t.URL)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("yt-dlp: %w (%s)", err, tail(string(out)))
	}
	files, _ := filepath.Glob(filepath.Join(dir, prefix(t)+".*"))
	for _, f := range files {
		if validFile(f) {
			return f, nil
		}
	}
	return "", errors.New("yt-dlp produced no file")
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[len(s)-200:]
	}
	return s
}

func cookiesAvailable() bool {
	st, err := os.Stat(cookiesFile)
	return err == nil && st.Size() > 0
}

// FetchCookies downloads the cookies file from COOKIES_LINK (raw text URL) once at startup.
func FetchCookies() {
	if config.C.CookiesLink == "" {
		return
	}
	resp, err := http.Get(config.C.CookiesLink)
	if err != nil || resp.StatusCode >= 400 {
		return
	}
	defer resp.Body.Close()
	if f, err := os.Create(cookiesFile); err == nil {
		_, _ = io.Copy(f, resp.Body)
		f.Close()
	}
}

// ExtractVideoID pulls the 11-char id out of a YouTube URL.
func ExtractVideoID(u string) string {
	if m := idRe.FindStringSubmatch(u); len(m) > 1 {
		return m[1]
	}
	return ""
}
