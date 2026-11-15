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

package store

import (
	"context"
	"slices"
	"time"

	"ArtistMusic/ArtistMusic/config"
	"ArtistMusic/internal/logger"
	"ArtistMusic/internal/utils"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ───────────── database ─────────────

var (
	client           *mongo.Client
	database         *mongo.Database
	settingsColl     *mongo.Collection
	chatSettingsColl *mongo.Collection

	logr              = logger.GetLogger("Database")
	dbCache           = utils.NewCache[string, any](60 * time.Minute)
	chatSettingsCache = utils.NewCache[int64, *ChatSettings](60 * time.Minute)
)

func Init(mongoURL string) (func(), error) {
	var err error
	logr.Debug("Initializing MongoDB...")
	client, err = mongo.Connect(options.Client().ApplyURI(mongoURL))
	if err != nil {
		return nil, err
	}

	logr.Debug("Successfully connected to MongoDB.")

	database = client.Database(config.DBName)
	settingsColl = database.Collection("bot_settings")
	chatSettingsColl = database.Collection("chat_settings")

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Disconnect(ctx); err != nil {
			logr.Errorf("Error while disconnecting MongoDB: %v", err)
		} else {
			logr.Info("MongoDB disconnected successfully")
		}
	}, nil
}

// ───────────── helpers ─────────────

var upsertOpt = options.UpdateOne().SetUpsert(true)

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// addUnique adds an element to a slice if it's not already present.
// Returns the new slice and true if the element was added.
func addUnique[T comparable](slice []T, element T) ([]T, bool) {
	if slices.Contains(slice, element) {
		return slice, false
	}
	return append(slice, element), true
}

// removeElement removes an element from a slice if it's present.
// Returns the new slice and true if the element was removed.
func removeElement[T comparable](slice []T, element T) ([]T, bool) {
	for i, v := range slice {
		if v == element {
			return append(slice[:i], slice[i+1:]...), true
		}
	}
	return slice, false
}

// contains checks if a slice contains an element.
func contains[T comparable](slice []T, element T) bool {
	return slices.Contains(slice, element)
}

// ───────────── maintenance ─────────────

func IsMaintenanceEnabled() (bool, error) {
	state, err := getBotState()
	if err != nil {
		return false, err
	}
	return state.Maintenance.Enabled, nil
}

// ───────────── logger ─────────────

func IsLoggerEnabled() (bool, error) {
	state, err := getBotState()
	if err != nil {
		return false, err
	}
	return state.LoggerEnabled, nil
}
