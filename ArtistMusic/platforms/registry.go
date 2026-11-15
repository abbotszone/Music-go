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
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/elevenyts/ArtistMusic/ArtistMusic/models"
)

var (
	mu      sync.RWMutex
	sorted  []models.Platform
	raceSeq uint64
)

func Register(p models.Platform) {
	mu.Lock()
	defer mu.Unlock()
	sorted = append(sorted, p)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Priority() > sorted[j].Priority() })
}

func ordered() []models.Platform {
	mu.RLock()
	defer mu.RUnlock()
	return append([]models.Platform(nil), sorted...)
}

// GetTracks resolves a URL or a search query into tracks.
func GetTracks(query string, video bool) ([]*models.Track, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("empty query")
	}
	for _, p := range ordered() {
		if p.CanGet(query) {
			if t, err := p.Get(query, video); err == nil && len(t) > 0 {
				return t, nil
			}
		}
	}
	return nil, errors.New("no tracks found")
}

type racer struct {
	name string
	run  func(ctx context.Context) (string, error)
}

// Download starts every method that can fetch the track at the same time
// (Artistbots API, yt-dlp with cookies, plain yt-dlp). First valid file wins,
// the rest are cancelled and their partial folders removed.
func Download(ctx context.Context, t *models.Track) (string, error) {
	if f := findFile(t); f != "" {
		return f, nil
	}

	var racers []racer
	for _, p := range ordered() {
		if !p.CanDownload(t.Source) {
			continue
		}
		p := p
		if yt, ok := p.(*YtDlp); ok {
			if cookiesAvailable() {
				racers = append(racers, racer{"yt-dlp+cookies", func(c context.Context) (string, error) {
					return yt.download(c, t, true)
				}})
			}
			racers = append(racers, racer{"yt-dlp", func(c context.Context) (string, error) {
				return yt.download(c, t, false)
			}})
			continue
		}
		racers = append(racers, racer{string(p.Name()), func(c context.Context) (string, error) {
			return p.Download(c, t)
		}})
	}
	if len(racers) == 0 {
		return "", errors.New("no downloader for source " + string(t.Source))
	}
	return race(ctx, t, racers)
}

func race(ctx context.Context, t *models.Track, racers []racer) (string, error) {
	type result struct {
		idx  int
		path string
		err  error
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()

	seq := atomic.AddUint64(&raceSeq, 1)
	out := make(chan result, len(racers))
	dirs := make([]string, len(racers))
	for i, r := range racers {
		dirs[i] = filepath.Join("downloads", ".race_"+strconv.FormatUint(seq, 10)+"_"+strconv.Itoa(i))
		_ = os.MkdirAll(dirs[i], 0o755)
		go func(i int, r racer) {
			p, err := r.run(withDir(rctx, dirs[i]))
			out <- result{i, p, err}
		}(i, r)
	}

	var errs []string
	for n := 0; n < len(racers); n++ {
		res := <-out
		if res.err == nil && validFile(res.path) {
			cancel()
			final := filepath.Join("downloads", filepath.Base(res.path))
			if os.Rename(res.path, final) != nil {
				final = res.path
			}
			remaining := len(racers) - n - 1
			go func() { // wipe the losers once they have stopped
				for k := 0; k < remaining; k++ {
					<-out
				}
				for _, d := range dirs {
					os.RemoveAll(d)
				}
			}()
			return final, nil
		}
		reason := "no file"
		if res.err != nil {
			reason = res.err.Error()
		}
		errs = append(errs, racers[res.idx].name+": "+reason)
	}
	for _, d := range dirs {
		os.RemoveAll(d)
	}
	return "", errors.New("download failed:\n• " + strings.Join(errs, "\n• "))
}

type ctxKey int

const dirKey ctxKey = 0

func withDir(ctx context.Context, d string) context.Context { return context.WithValue(ctx, dirKey, d) }
func dirOf(ctx context.Context) string {
	if d, ok := ctx.Value(dirKey).(string); ok && d != "" {
		return d
	}
	return "downloads"
}

func prefix(t *models.Track) string {
	if t.Video {
		return "video_" + t.ID
	}
	return "audio_" + t.ID
}

func findFile(t *models.Track) string {
	files, _ := filepath.Glob(filepath.Join("downloads", prefix(t)+".*"))
	for _, f := range files {
		if validFile(f) {
			return f
		}
	}
	return ""
}

func validFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Size() > 10*1024
}
