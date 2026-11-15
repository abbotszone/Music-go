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

package store

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Store struct{ chats, users *mongo.Collection }

func Connect(uri string) (*Store, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cl, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	if err := cl.Ping(ctx, nil); err != nil {
		return nil, err
	}
	db := cl.Database("artistmusic")
	return &Store{chats: db.Collection("chats"), users: db.Collection("users")}, nil
}

func up(c *mongo.Collection, id int64, set bson.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = c.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": set}, options.Update().SetUpsert(true))
}

func (s *Store) SeenChat(id int64)                { up(s.chats, id, bson.M{"seen": time.Now()}) }
func (s *Store) SeenUser(id int64)                { up(s.users, id, bson.M{"seen": time.Now()}) }
func (s *Store) SetAutoplay(id int64, on bool)    { up(s.chats, id, bson.M{"autoplay": on}) }
func (s *Store) SetNoThumb(id int64, off bool)    { up(s.chats, id, bson.M{"nothumb": off}) }

// Settings returns (autoplay, nothumb); autoplay defaults to true.
func (s *Store) Settings(id int64) (bool, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var d struct {
		Autoplay *bool `bson:"autoplay"`
		NoThumb  bool  `bson:"nothumb"`
	}
	if s.chats.FindOne(ctx, bson.M{"_id": id}).Decode(&d) != nil {
		return true, false
	}
	ap := true
	if d.Autoplay != nil {
		ap = *d.Autoplay
	}
	return ap, d.NoThumb
}

func ids(c *mongo.Collection) []int64 {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cur, err := c.Find(ctx, bson.M{})
	if err != nil {
		return nil
	}
	var out []int64
	for cur.Next(ctx) {
		var d struct{ ID int64 `bson:"_id"` }
		if cur.Decode(&d) == nil {
			out = append(out, d.ID)
		}
	}
	return out
}

func (s *Store) ChatIDs() []int64 { return ids(s.chats) }
func (s *Store) UserIDs() []int64 { return ids(s.users) }
