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

package utils

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"ArtistMusic/internal/logger"
	td "github.com/ArtistMusic/Artist"
	"resty.dev/v3"
)

// ───────────── get_duration ─────────────

func GetDurationByFFProbe(filePath string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(
		ctx,
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		filePath,
	)

	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return 0, err
	}

	result := strings.TrimSpace(out.String())
	seconds, err := strconv.ParseFloat(result, 64)
	if err != nil {
		return 0, err
	}

	return int(seconds), nil
}

func GetDuration(m *td.Message) int {
	if m == nil || m.Content == nil {
		return 0
	}
	switch c := m.Content.(type) {
	case *td.MessageAudio:
		if c.Audio != nil {
			return int(c.Audio.Duration)
		}
	case *td.MessageVideo:
		if c.Video != nil {
			return int(c.Video.Duration)
		}
	case *td.MessageVoiceNote:
		if c.VoiceNote != nil {
			return int(c.VoiceNote.Duration)
		}
	case *td.MessageVideoNote:
		if c.VideoNote != nil {
			return int(c.VideoNote.Duration)
		}
	case *td.MessageDocument:
		if c.Document != nil {
			return int(GetDurationByFFProbeSafe(c.Document.FileName))
		}
	}
	return 0
}

func GetDurationByFFProbeSafe(fileName string) int {
	if fileName == "" {
		return 0
	}
	dur, err := GetDurationByFFProbe(fileName)
	if err != nil {
		return 0
	}
	return dur
}

// ───────────── get_progress ─────────────

var progressBarCache = [9]string{
	"◉———————",
	"—◉——————",
	"——◉—————",
	"———◉————",
	"————◉———",
	"—————◉——",
	"——————◉—",
	"———————◉",
	"————————◉",
}

func GetProgressBar(playedSec, durationSec int) string {
	if durationSec <= 0 || playedSec <= 0 {
		return progressBarCache[0]
	}

	if playedSec >= durationSec {
		return progressBarCache[8]
	}

	index := min((playedSec*9)/durationSec, 8)

	return progressBarCache[index]
}

// ───────────── batbin ─────────────

const batbinBaseURL = "https://batbin.me/"

var httpClient = resty.New().SetTimeout(10 * time.Second)

type batbinResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func CreatePaste(content string) (string, error) {
	var result batbinResponse

	resp, err := httpClient.R().
		SetBody(content).
		SetResult(&result).
		Post(batbinBaseURL + "api/v2/paste")
	if err != nil {
		logger.Error("batbin request error: " + err.Error())
		return "", err
	}

	if resp.StatusCode() != 200 {
		logger.Error("batbin bad response: " + resp.String())
		return "", fmt.Errorf("batbin returned status %d", resp.StatusCode())
	}

	if !result.Success {
		err := fmt.Errorf("batbin paste failed")
		logger.Error(err.Error())
		return "", err
	}

	return batbinBaseURL + result.Message, nil
}
