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

package models

import "context"

type PlatformName string

type Track struct {
	ID        string
	Title     string
	URL       string
	Artwork   string
	Duration  int // seconds
	Video     bool
	Source    PlatformName
	Requester string
}

// Platform is one source of tracks and/or one way of downloading them.
type Platform interface {
	Name() PlatformName
	Priority() int
	CanGet(query string) bool
	Get(query string, video bool) ([]*Track, error)
	CanDownload(source PlatformName) bool
	Download(ctx context.Context, t *Track) (string, error)
}
