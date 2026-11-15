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

package config

import (
	"errors"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"

	"ArtistMusic/internal/logger"
	_ "github.com/joho/godotenv/autoload"
)

// ───────────── config ─────────────

var (
	// Required
	APIID          = int32(getEnvInt64("API_ID"))
	APIHash        = getEnv("API_HASH")
	Token          = getEnv("TOKEN")
	MongoURI       = getEnv("MONGO_DB_URI")
	StringSessions = getEnvStrings("STRING_SESSIONS")

	// Optional
	DBName        = "ArtistMusic"
	SessionType   = getEnv("SESSION_TYPE", "pyrogram")
	LoggerID      = getEnvInt64("LOGGER_ID", 0)
	OwnerID       = getEnvInt64("OWNER_ID", 8801522842)
	DevURL        = getEnv("DEV_URL", getEnv("DEVELOPER_URL", ""))
	DisableColour = getEnvBool("DISABLE_COLOUR", false)

	// Artistbots API: GET {URL}/download?url=VIDEO_ID&type=audio|video&api_key=KEY
	ArtistbotsAPIURL = getEnv(
		"ARTISTBOTS_API_URL",
		getEnv("ARTISTBOTS_API", "https://api.artistbotsapi.bond"),
	)
	ArtistbotsAPIKey = getEnv("ARTISTBOTS_API_KEY")

	DefaultLang    = getEnv("DEFAULT_LANG", "en")
	DurationLimit  = getEnvInt("DURATION_LIMIT", 4200)
	LeaveOnDemoted = getEnvBool("LEAVE_ON_DEMOTED", false)
	QueueLimit     = getEnvInt("QUEUE_LIMIT", 24)

	SupportChat    = getEnv("SUPPORT_CHAT", "https://t.me/Artistbots")
	SupportChannel = getEnv("SUPPORT_CHANNEL", "https://t.me/Artistbots")
	CookiesLink    = getEnv("COOKIES_LINK", "")
	MaxAuthUsers   = getEnvInt("MAX_AUTH_USERS", 25)

	StartImages = getEnvStrings("START_IMAGES")

	PingImage = getEnv(
		"PING_IMG_URL",
		"https://artistbox.bond/f/bwifjv9e.mp4",
	)

	RepoImage = getEnv(
		"REPO_IMG_URL",
		"https://artistbox.bond/f/bwifjv9e.mp4",
	)

	Port = getEnv("PORT", "8000")

	StartTime = time.Now()

	logr = logger.GetLogger("config")
)

func Load() error {
	// Legacy compatibility
	if Token == "" {
		Token = getEnv("BOT_TOKEN")
	}

	if LoggerID == 0 {
		LoggerID = getEnvInt64("LOG_GROUP_ID")
	}

	if len(StringSessions) == 0 {
		StringSessions = getEnvStrings("STRING_SESSION")
	}

	if len(StartImages) == 0 {
		if img := getEnv("START_IMG_URL"); img != "" {
			StartImages = []string{img}
		}
	}

	if len(StartImages) == 0 {
		StartImages = []string{DefaultStartImage}
	}

	if err := validateConfig(); err != nil {
		return err
	}

	return nil
}

func validateConfig() error {
	checks := []struct {
		ok  bool
		msg string
	}{
		{APIID != 0, "API_ID is required"},
		{APIHash != "", "API_HASH is required"},
		{Token != "", "TOKEN (or BOT_TOKEN) is required"},
		{MongoURI != "", "MONGO_DB_URI is required"},
		{len(StringSessions) > 0, "STRING_SESSIONS (or STRING_SESSION) is required"},
	}

	for _, check := range checks {
		if !check.ok {
			return errors.New(check.msg)
		}
	}

	return nil
}

// DefaultStartImage is used when START_IMAGES / START_IMG_URL are not set.
const DefaultStartImage = "https://artistbox.bond/f/bwifjv9e.mp4"

func StartImage() string {
	if len(StartImages) == 0 {
		return ""
	}

	return StartImages[rand.IntN(len(StartImages))]
}

// ───────────── parser ─────────────

func fallback[T any](def []T) T {
	if len(def) > 0 {
		return def[0]
	}

	var zero T
	return zero
}

func getEnv(key string, def ...string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback(def)
}

func getEnvBool(key string, def ...bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback(def)
	}

	b, err := strconv.ParseBool(v)
	if err != nil {
		logr.Fatalf("invalid boolean value for %s: %q", key, v)
	}

	return b
}

func getEnvInt(key string, def ...int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback(def)
	}

	n, err := strconv.Atoi(v)
	if err != nil {
		logr.Fatalf("invalid integer value for %s: %q", key, v)
	}

	return n
}

func getEnvInt64(key string, def ...int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback(def)
	}

	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		logr.Fatalf("invalid int64 value for %s: %q", key, v)
	}

	return n
}

func getEnvStrings(key string, def ...[]string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback(def)
	}

	fields := strings.FieldsFunc(v, func(r rune) bool {
		switch r {
		case ',', ';', ' ':
			return true
		default:
			return false
		}
	})

	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}

	if len(out) == 0 {
		return fallback(def)
	}

	return out
}
