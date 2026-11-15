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

package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	APIID          int32
	APIHash        string
	Token          string
	MongoURI       string
	StringSession  string // userbot session used to join voice chats
	LoggerID       int64
	OwnerID        int64
	ArtistbotsKey  string
	ArtistbotsURL  string
	CookiesLink    string
	DurationLimit  int
	QueueLimit     int
	MaxAuthUsers   int
	LeaveOnDemoted bool
	DefaultLang    string
	StartImage     string
	PingImage      string
	SupportChat    string
	SupportChannel string
}

var C Config

func Load() {
	_ = godotenv.Load() // .env optional (hosts can inject real env vars)
	C = Config{
		APIID:          int32(num("API_ID", 0)),
		APIHash:        os.Getenv("API_HASH"),
		Token:          os.Getenv("TOKEN"),
		MongoURI:       os.Getenv("MONGO_DB_URI"),
		StringSession:  os.Getenv("STRING_SESSIONS"),
		LoggerID:       int64(num("LOGGER_ID", 0)),
		OwnerID:        int64(num("OWNER_ID", 0)),
		ArtistbotsKey:  os.Getenv("ARTISTBOTS_API_KEY"),
		ArtistbotsURL:  strings.TrimRight(os.Getenv("ARTISTBOTS_API_URL"), "/"),
		CookiesLink:    os.Getenv("COOKIES_LINK"),
		DurationLimit:  num("DURATION_LIMIT", 42000),
		QueueLimit:     num("QUEUE_LIMIT", 24),
		MaxAuthUsers:   num("MAX_AUTH_USERS", 25),
		LeaveOnDemoted: os.Getenv("LEAVE_ON_DEMOTED") == "true",
		DefaultLang:    getenv("DEFAULT_LANG", "en"),
		StartImage:     os.Getenv("START_IMAGES"),
		PingImage:      os.Getenv("PING_IMG_URL"),
		SupportChat:    os.Getenv("SUPPORT_CHAT"),
		SupportChannel: os.Getenv("SUPPORT_CHANNEL"),
	}
}

func num(k string, d int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(k))); err == nil {
		return v
	}
	return d
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
