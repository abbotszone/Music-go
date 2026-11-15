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
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ArtistMusic/ArtistMusic/logger"

	td "github.com/ArtistMusic/Artist"
	"resty.dev/v3"

	state "ArtistMusic/ArtistMusic/core/models"
	"ArtistMusic/ArtistMusic/utils"
	"ArtistMusic/config"
)

type reg struct {
	mu     sync.RWMutex
	sorted []state.Platform
	byName map[state.PlatformName]state.Platform
}

var (
	global = &reg{
		byName: make(map[state.PlatformName]state.Platform),
	}
	rc = resty.New().SetTimeout(20 * time.Second)
)

func Register(p state.Platform) {
	global.mu.Lock()
	defer global.mu.Unlock()

	global.byName[p.Name()] = p
	global.sorted = append(global.sorted, p)
	sort.Slice(global.sorted, func(i, j int) bool {
		return global.sorted[i].Priority() > global.sorted[j].Priority()
	})
}

func GetPlatform(name state.PlatformName) (state.Platform, bool) {
	global.mu.RLock()
	defer global.mu.RUnlock()
	p, ok := global.byName[name]
	return p, ok
}

func ordered() []state.Platform {
	global.mu.RLock()
	defer global.mu.RUnlock()
	out := make([]state.Platform, len(global.sorted))
	copy(out, global.sorted)
	return out
}

func findFor(query string) state.Platform {
	for _, p := range ordered() {
		if p.CanGet(query) {
			return p
		}
	}
	return nil
}

func GetTracks(c *td.Client, m *td.Message, video bool) ([]*state.Track, error) {
	logger.Debug("GetTracks | video:" + strconv.FormatBool(video))

	if urls, _ := utils.ExtractURLs(c, m); len(urls) > 0 {
		tracks, errs := fetchFromURLs(urls, video)
		if len(tracks) > 0 {
			return tracks, nil
		}
		if !hasPlayableReply(c, m) {
			return nil, combineErrs("no supported platform for given URL(s)", errs)
		}
	}

	if q := m.Args(); q != "" {
		tracks, err := searchQuery(q, video)
		if err == nil && len(tracks) > 0 {
			return tracks, nil
		}
	}

	if m.ReplyToMessageID() > 0 {
		return fromReply(c, m)
	}

	return nil, errors.New("no tracks found")
}

// dlRacer is one way of downloading a track.
type dlRacer struct {
	name string
	run  func(ctx context.Context) (string, error)
}

var raceSeq uint64

// Download fetches the given track and returns its local file path.
//
// When more than one method can download the track (for a YouTube track:
// the Artistbots API, yt-dlp with cookies and plain yt-dlp), they all start at
// the same time. The first one that returns a valid file wins; the others are
// cancelled and their partial files are deleted. With a single method it simply
// runs that one.
func Download(
	ctx context.Context,
	track *state.Track,
	msg *td.Message,
) (string, error) {
	if p := findFile(track); p != "" {
		logger.Debug("Download: cache hit " + p)
		return p, nil
	}

	var racers []dlRacer
	for _, p := range ordered() {
		if !p.CanDownload(track.Source) {
			continue
		}
		p := p

		if yt, ok := p.(*YtdlpPlatform); ok && yt.isYouTubeURL(track.URL) {
			if ytdlpCookiesAvailable() {
				racers = append(racers, dlRacer{
					name: "yt-dlp+cookies",
					run: func(c context.Context) (string, error) {
						return p.Download(withCookieMode(c, cookiesOn), track, msg)
					},
				})
			}
			racers = append(racers, dlRacer{
				name: "yt-dlp",
				run: func(c context.Context) (string, error) {
					return p.Download(withCookieMode(c, cookiesOff), track, msg)
				},
			})
			continue
		}

		racers = append(racers, dlRacer{
			name: string(p.Name()),
			run: func(c context.Context) (string, error) {
				return p.Download(c, track, msg)
			},
		})
	}

	switch len(racers) {
	case 0:
		return "", errors.New("no downloader for source: " + string(track.Source))
	case 1:
		path, err := racers[0].run(ctx)
		if err == nil {
			logger.Info("Download ok via " + racers[0].name + " -> " + path)
			return path, nil
		}
		if isDownloadCancelled(err) {
			return "", err
		}
		return "", combineErrs("download failed", []string{racers[0].name + ": " + err.Error()})
	}

	return raceDownload(ctx, track, racers)
}

func raceDownload(ctx context.Context, track *state.Track, racers []dlRacer) (string, error) {
	type result struct {
		idx  int
		path string
		err  error
	}

	rctx, cancel := context.WithCancel(ctx)
	defer cancel()

	seq := atomic.AddUint64(&raceSeq, 1)
	results := make(chan result, len(racers))
	dirs := make([]string, len(racers))

	for i, r := range racers {
		dirs[i] = filepath.Join("downloads", ".race_"+strconv.FormatUint(seq, 10)+"_"+strconv.Itoa(i))
		go func(i int, r dlRacer) {
			path, err := r.run(withSlotDir(rctx, dirs[i]))
			results <- result{idx: i, path: path, err: err}
		}(i, r)
	}

	// cleanup deletes the race folders once the remaining racers have stopped.
	cleanup := func(remaining int, keep int) {
		go func() {
			for k := 0; k < remaining; k++ {
				<-results
			}
			for i, d := range dirs {
				if i != keep {
					os.RemoveAll(d)
				}
			}
		}()
	}

	var errs []string
	for n := 0; n < len(racers); n++ {
		res := <-results

		if res.err == nil && fileExists(res.path) {
			cancel() // reject the other methods

			final := res.path
			moved := filepath.Join("downloads", filepath.Base(res.path))
			if os.Rename(res.path, moved) == nil {
				final = moved
				os.RemoveAll(dirs[res.idx])
			}

			logger.Info("Download ok via " + racers[res.idx].name + " (first to finish) -> " + final)
			cleanup(len(racers)-n-1, res.idx)
			return final, nil
		}

		if res.err != nil && ctx.Err() != nil {
			// The caller cancelled the download.
			cancel()
			cleanup(len(racers)-n-1, -1)
			os.RemoveAll(dirs[res.idx])
			return "", res.err
		}

		reason := "no file produced"
		if res.err != nil {
			reason = res.err.Error()
		}
		errs = append(errs, racers[res.idx].name+": "+reason)
	}

	for _, d := range dirs {
		os.RemoveAll(d)
	}
	return "", combineErrs("download failed", errs)
}

func fetchFromURLs(urls []string, video bool) ([]*state.Track, []string) {
	var tracks []*state.Track
	var errs []string

	for _, u := range urls {
		p := findFor(u)
		if p == nil {
			errs = append(errs, "no platform for: "+u)
			continue
		}

		logger.Debug("URL matched " + string(p.Name()) + ": " + u)
		got, err := p.Get(u, video)
		if err != nil {
			if strings.Contains(err.Error(), "failed to extract metadata") {
				continue
			}
			errs = append(errs, string(p.Name())+": "+err.Error())
			continue
		}
		tracks = append(tracks, got...)
	}

	return tracks, errs
}

// AutoplayTracks fetches non-repeating recommended tracks similar to the last played track.
// It uses multi-tiered recommendations (YouTube mix playlist, related watch-next videos, and search fallback).
// It verifies candidates against the room's playback history to prevent repeating songs.
func AutoplayTracks(last *state.Track, limit int, history ...string) ([]*state.Track, error) {
	if last == nil {
		return nil, errors.New("cannot resolve autoplay for nil track")
	}

	ytRaw, ok := GetPlatform(PlatformYouTube)
	if !ok {
		return nil, errors.New("youtube platform not registered")
	}
	yt := ytRaw.(*YouTubePlatform)

	targetID := last.ID
	if last.Source != PlatformYouTube || targetID == "" {
		// Non-YouTube track: resolve equivalent YouTube track first using title
		tracks, err := yt.VideoSearch(last.Title, true)
		if err == nil && len(tracks) > 0 {
			targetID = tracks[0].ID
		}
	}

	fetchLimit := limit * 3
	if fetchLimit < 15 {
		fetchLimit = 15
	}

	candidates, err := yt.AutoplayCandidates(targetID, last.Title, fetchLimit)
	if err != nil || len(candidates) == 0 {
		return nil, errors.New("failed to fetch autoplay candidates")
	}

	// Filter candidates against history ring buffer and current track
	historyMap := make(map[string]bool)
	for _, h := range history {
		historyMap[h] = true
	}

	var filtered []*state.Track
	for _, t := range candidates {
		if t == nil || t.ID == "" || t.ID == last.ID {
			continue
		}
		if historyMap["id:"+t.ID] {
			continue
		}
		norm := state.NormalizeTrackTitle(t.Title)
		if norm != "" && historyMap["title:"+norm] {
			continue
		}
		filtered = append(filtered, t)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}

	// If everything was filtered out by strict history, fall back to candidates differing from last.ID
	if len(filtered) == 0 {
		for _, t := range candidates {
			if t != nil && t.ID != "" && t.ID != last.ID {
				filtered = append(filtered, t)
				if limit > 0 && len(filtered) >= limit {
					break
				}
			}
		}
	}

	return withVideo(filtered, last.Video), nil
}

// ResolveQuery resolves a plain URL or search query string to track(s),
// without requiring a Telegram message. Used by features that store a raw
// query/URL (e.g. playlist management) rather than a live message.
func ResolveQuery(query string, video bool) ([]*state.Track, error) {
	if p := findFor(query); p != nil {
		if got, err := p.Get(query, video); err == nil && len(got) > 0 {
			return got, nil
		}
	}
	return searchQuery(query, video)
}

func searchQuery(q string, video bool) ([]*state.Track, error) {
	if p := findFor(q); p != nil && p.Name() != PlatformYouTube {
		if got, err := p.Get(q, video); err == nil && len(got) > 0 {
			return got, nil
		}
	}

	yt, ok := GetPlatform(PlatformYouTube)
	if !ok {
		return nil, errors.New("youtube platform not registered")
	}

	tracks, err := yt.Get(q, video)
	if err != nil {
		return nil, err
	}
	if len(tracks) == 0 {
		return nil, nil
	}
	return []*state.Track{tracks[0]}, nil
}

func fromReply(c *td.Client, m *td.Message) ([]*state.Track, error) {
	target, isVideo, _ := playableMedia(c, m)
	if target == nil {
		return nil, errors.New("⚠️ Reply with a valid media (audio/video)")
	}

	tgp, ok := GetPlatform(PlatformTelegram)
	if !ok {
		return nil, errors.New("telegram platform not registered")
	}

	track, err := tgp.(*TelegramPlatform).GetTracksByMessage(c, target)
	if err != nil {
		return nil, err
	}
	track.Video = isVideo
	return []*state.Track{track}, nil
}

func hasPlayableReply(c *td.Client, m *td.Message) bool {
	if m.ReplyToMessageID() <= 0 {
		return false
	}
	msg, _, _ := playableMedia(c, m)
	return msg != nil
}

func combineErrs(prefix string, errs []string) error {
	if len(errs) == 0 {
		return errors.New(prefix)
	}
	return errors.New(prefix + "\n• " + strings.Join(errs, "\n• "))
}

func GetYouTubeMixPlaylist(_ context.Context, playlistID string) ([]*state.Track, error) {
	ytRaw, ok := GetPlatform(PlatformYouTube)
	if !ok {
		return nil, errors.New("youtube platform not registered")
	}
	yt := ytRaw.(*YouTubePlatform)
	limit := config.QueueLimit
	if limit <= 0 {
		limit = 15
	}
	return yt.fetchMixPlaylist(playlistID, limit)
}

func SearchTracks(query string, video bool) ([]*state.Track, error) {
	ytRaw, ok := GetPlatform(PlatformYouTube)
	if !ok {
		return nil, errors.New("youtube platform not registered")
	}
	yt := ytRaw.(*YouTubePlatform)
	return yt.VideoSearch(query)
}

