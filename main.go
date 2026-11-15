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

package main

import (
	"log"
	"os"

	"github.com/elevenyts/ArtistMusic/ArtistMusic/bot"
	"github.com/elevenyts/ArtistMusic/ArtistMusic/config"
	"github.com/elevenyts/ArtistMusic/ArtistMusic/platforms"
	"github.com/elevenyts/ArtistMusic/ArtistMusic/store"
	"github.com/elevenyts/ArtistMusic/ArtistMusic/voice"
)

func main() {
	config.Load()
	if config.C.Token == "" || config.C.APIID == 0 || config.C.APIHash == "" {
		log.Fatal("TOKEN, API_ID and API_HASH are required (see .env.example)")
	}
	_ = os.MkdirAll("downloads", 0o755)
	platforms.FetchCookies()

	db, err := store.Connect(config.C.MongoURI)
	if err != nil {
		log.Fatalf("mongo: %v", err)
	}

	// Swap LogEngine for your ntgcalls-backed engine to get real audio.
	var engine voice.Engine = &voice.LogEngine{}

	b, err := bot.New(db, engine)
	if err != nil {
		log.Fatalf("bot: %v", err)
	}
	log.Println("ArtistMusic is running")
	b.Idle()
}
