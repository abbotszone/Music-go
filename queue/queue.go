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

package queue

import (
	"sync"

	"github.com/elevenyts/ArtistMusic/models"
)

// Chat holds everything the bot remembers about one group's playback.
type Chat struct {
	mu       sync.Mutex
	Current  *models.Track
	Items    []*models.Track
	Loop     int // remaining repeats of the current track
	Autoplay bool
	NoThumb  bool
	Position int // seconds, updated by the player ticker
	MsgID    int32
}

var (
	mu    sync.Mutex
	chats = map[int64]*Chat{}
)

func Get(chatID int64) *Chat {
	mu.Lock()
	defer mu.Unlock()
	c, ok := chats[chatID]
	if !ok {
		c = &Chat{Autoplay: true}
		chats[chatID] = c
	}
	return c
}

func Drop(chatID int64) {
	mu.Lock()
	delete(chats, chatID)
	mu.Unlock()
}

func (c *Chat) Add(limit int, t ...*models.Track) (added int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, x := range t {
		if limit > 0 && len(c.Items) >= limit {
			break
		}
		c.Items = append(c.Items, x)
		added++
	}
	return
}

func (c *Chat) AddFront(t *models.Track) {
	c.mu.Lock()
	c.Items = append([]*models.Track{t}, c.Items...)
	c.mu.Unlock()
}

// Next picks what plays after the current track (honours /loop).
func (c *Chat) Next() *models.Track {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Loop > 0 && c.Current != nil {
		c.Loop--
		return c.Current
	}
	if len(c.Items) == 0 {
		return nil
	}
	n := c.Items[0]
	c.Items = c.Items[1:]
	return n
}

func (c *Chat) List() []*models.Track {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*models.Track(nil), c.Items...)
}

func (c *Chat) Clear() {
	c.mu.Lock()
	c.Current, c.Items, c.Loop, c.Position = nil, nil, 0, 0
	c.mu.Unlock()
}
