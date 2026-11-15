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

	"ArtistMusic/ArtistMusic/logger"
	"ArtistMusic/ArtistMusic/utils"

	td "github.com/ArtistMusic/Artist"
)

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
