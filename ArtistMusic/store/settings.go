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
	"fmt"
	"sync"
	"time"

	"ArtistMusic/ArtistMusic/config"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ───────────── chat_settings ─────────────

type ChatSettings struct {
	ChatID                int64     `bson:"_id"`
	ChannelPlayID         int64     `bson:"cplay_id"`
	AuthUsers             []int64   `bson:"auth_users"`
	AdminMode             AdminMode `bson:"admin_mode,omitempty"`
	Language              string    `bson:"language"`
	AssistantIndex        int       `bson:"ass_index,omitempty"`
	ThumbnailsDisabled    bool      `bson:"no_thumb"`
	PlayModeAdminsOnly    bool      `bson:"play_mode"`
	CommandDelete         bool      `bson:"cmd_delete"`
	CleanMode             bool      `bson:"clean_mode"`
	CleanModeDurationMins int       `bson:"clean_mode_duration_mins"`
	Autoplay              bool      `bson:"autoplay"`
}

func defaultChatSettings(chatID int64) *ChatSettings {
	return &ChatSettings{
		ChatID:                chatID,
		AuthUsers:             []int64{},
		CleanModeDurationMins: 15,
	}
}

func getChatSettings(chatID int64) (*ChatSettings, error) {
	if settings, found := chatSettingsCache.Get(chatID); found {
		return settings, nil
	}

	ctx, cancel := ctx()
	defer cancel()

	var settings ChatSettings
	err := chatSettingsColl.FindOne(ctx, bson.M{"_id": chatID}).
		Decode(&settings)

	if err == mongo.ErrNoDocuments {
		def := defaultChatSettings(chatID)
		chatSettingsCache.Set(chatID, def)
		return def, nil
	}

	if err != nil {
		return nil, fmt.Errorf(
			"failed to get chat settings for %d: %w",
			chatID,
			err,
		)
	}

	chatSettingsCache.Set(chatID, &settings)
	return &settings, nil
}

func updateChatSettings(settings *ChatSettings) error {
	ctx, cancel := ctx()
	defer cancel()

	_, err := chatSettingsColl.UpdateOne(
		ctx,
		bson.M{"_id": settings.ChatID},
		bson.M{"$set": settings},
		upsertOpt,
	)
	if err != nil {
		return fmt.Errorf(
			"failed to update chat settings for %d: %w",
			settings.ChatID,
			err,
		)
	}

	chatSettingsCache.Set(settings.ChatID, settings)
	return nil
}

func modifyChatSettings(chatID int64, fn func(*ChatSettings) bool) error {
	settings, err := getChatSettings(chatID)
	if err != nil {
		return err
	}

	if fn(settings) {
		return updateChatSettings(settings)
	}

	return nil
}

// Autoplay reports whether autoplay is persistently enabled for the chat.
func Autoplay(chatID int64) (bool, error) {
	s, err := getChatSettings(chatID)
	if err != nil {
		return false, err
	}
	return s.Autoplay, nil
}

// SetAutoplay updates the persistent autoplay setting for the chat.
func SetAutoplay(chatID int64, enabled bool) error {
	return modifyChatSettings(chatID, func(s *ChatSettings) bool {
		if s.Autoplay == enabled {
			return false
		}
		s.Autoplay = enabled
		return true
	})
}

// ───────────── language ─────────────

func Language(chatID int64) (string, error) {
	settings, err := getChatSettings(chatID)
	if err != nil {
		return config.DefaultLang, err
	}
	if settings.Language == "" {
		return config.DefaultLang, nil
	}
	return settings.Language, nil
}

// ───────────── play_mode ─────────────

func PlayModeAdminsOnly(chatID int64) (bool, error) {
	settings, err := getChatSettings(chatID)
	if err != nil {
		return false, err
	}
	return settings.PlayModeAdminsOnly, nil
}

// ───────────── thumbnail ─────────────

// ThumbnailsDisabled returns whether thumbnails are disabled for the chat.
// Returns false by default (thumbnails enabled).
func ThumbnailsDisabled(chatID int64) (bool, error) {
	settings, err := getChatSettings(chatID)
	if err != nil {
		return false, err
	}
	return settings.ThumbnailsDisabled, nil
}

// SetThumbnailsDisabled sets whether thumbnails should be disabled for the chat.
func SetThumbnailsDisabled(chatID int64, disabled bool) error {
	return modifyChatSettings(chatID, func(s *ChatSettings) bool {
		if s.ThumbnailsDisabled == disabled {
			return false
		}
		s.ThumbnailsDisabled = disabled
		return true
	})
}

// ───────────── admin_mode ─────────────

type AdminMode string

const (
	AdminModeAdminsOnly AdminMode = "admin"
	AdminModeAdminAuth  AdminMode = "adminauth"
	AdminModeEveryone   AdminMode = "everyone"
)

func GetAdminMode(chatID int64) (AdminMode, error) {
	settings, err := getChatSettings(chatID)
	if err != nil {
		return "", err
	}
	if settings.AdminMode == "" {
		return AdminModeAdminAuth, nil
	}
	return settings.AdminMode, nil
}

// ───────────── channel_play ─────────────

func LinkedChannel(chatID int64) (int64, error) {
	settings, err := getChatSettings(chatID)
	if err != nil {
		return 0, err
	}
	return settings.ChannelPlayID, nil
}

// ───────────── assistant ─────────────

var (
	assistantCount int     // total assistants; valid indexes are 1..assistantCount
	assistantUsage []int64 // chats per assistant, index 1..assistantCount
	usageMu        sync.RWMutex
	indexCache     map[int64]int // chatID -> assistant index (1-based)
	cacheMu        sync.RWMutex
)

// InitAssistantIndexes records the assistant count and redistributes all chats
// evenly across the assistant pool. It must be called once at startup, before
// any GetAssistant call.
func InitAssistantIndexes(count int) error {
	if count <= 0 {
		return fmt.Errorf("assistantCount must be positive")
	}

	assistantCount = count

	all, err := fetchAllChatSettings()
	if err != nil {
		return err
	}

	redistributeAssistants(all, count)

	if err := saveChangedSettings(all); err != nil {
		return err
	}

	rebuildAssistantUsage(all, count)
	return nil
}

// GetAssistant returns the 1-based index of the assistant serving chatID.
// It checks the in-memory cache, then the persisted chat settings, and finally
// assigns the least-used assistant, persisting the choice.
func GetAssistant(chatID int64) (int, error) {
	if assistantCount <= 0 {
		return 0, fmt.Errorf("assistants not initialized")
	}

	cacheMu.RLock()
	idx, ok := indexCache[chatID]
	cacheMu.RUnlock()
	if ok && idx >= 1 && idx <= assistantCount {
		return idx, nil
	}

	settings, err := getChatSettings(chatID)
	if err != nil {
		return 0, err
	}

	if settings.AssistantIndex >= 1 && settings.AssistantIndex <= assistantCount {
		cacheAssistant(chatID, settings.AssistantIndex)
		return settings.AssistantIndex, nil
	}

	usageMu.RLock()
	countsCopy := make([]int64, len(assistantUsage))
	copy(countsCopy, assistantUsage)
	usageMu.RUnlock()

	newIndex := pickLeastUsedAssistant(countsCopy)

	settings.AssistantIndex = newIndex
	if err := updateChatSettings(settings); err != nil {
		return 0, err
	}

	usageMu.Lock()
	if newIndex >= 1 && newIndex < len(assistantUsage) {
		assistantUsage[newIndex]++
	}
	usageMu.Unlock()

	cacheAssistant(chatID, newIndex)
	return newIndex, nil
}

// SaveAssistant persists the assistant (1-based index) bound to a chat and
// keeps the in-memory cache and usage counters in sync.
func SaveAssistant(chatID int64, idx int) {
	if idx < 1 || idx > assistantCount {
		logr.Errorf("assistant index %d out of range for %d", idx, chatID)
		return
	}

	settings, err := getChatSettings(chatID)
	if err != nil {
		logr.Errorf("failed to save assistant index for %d: %v", chatID, err)
		return
	}

	old := settings.AssistantIndex
	if old != idx {
		settings.AssistantIndex = idx
		if err := updateChatSettings(settings); err != nil {
			logr.Errorf("failed to save assistant index for %d: %v", chatID, err)
			return
		}
	}

	cacheAssistant(chatID, idx)

	usageMu.Lock()
	if old != idx {
		if old >= 1 && old < len(assistantUsage) && assistantUsage[old] > 0 {
			assistantUsage[old]--
		}
		if idx >= 1 && idx < len(assistantUsage) {
			assistantUsage[idx]++
		}
	}
	usageMu.Unlock()
}

func cacheAssistant(chatID int64, idx int) {
	cacheMu.Lock()
	if indexCache == nil {
		indexCache = make(map[int64]int)
	}
	indexCache[chatID] = idx
	cacheMu.Unlock()
}

func fetchAllChatSettings() ([]*ChatSettings, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cursor, err := chatSettingsColl.Find(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch chat settings: %w", err)
	}
	defer cursor.Close(ctx)

	var all []*ChatSettings
	if err := cursor.All(ctx, &all); err != nil {
		return nil, fmt.Errorf("failed to decode chat settings: %w", err)
	}

	return all, nil
}

// redistributeAssistants moves as few chats as possible so each assistant ends
// up with an even share of the chats. Chats whose current index fits the even
// target stay put; the rest are reassigned to fill the gaps.
func redistributeAssistants(all []*ChatSettings, assistantCount int) {
	desired := evenDistribution(len(all), assistantCount)

	kept := make([]int, assistantCount+1)
	var pool []*ChatSettings

	for _, s := range all {
		if s.AssistantIndex >= 1 && s.AssistantIndex <= assistantCount &&
			kept[s.AssistantIndex] < desired[s.AssistantIndex] {
			kept[s.AssistantIndex]++
			continue
		}
		pool = append(pool, s)
	}

	for i := 1; i <= assistantCount; i++ {
		for kept[i] < desired[i] && len(pool) > 0 {
			pool[0].AssistantIndex = i
			pool = pool[1:]
			kept[i]++
		}
	}
}

// evenDistribution returns the target chat count per assistant (1-based) that
// spreads total chats as evenly as possible across assistants.
func evenDistribution(total, assistants int) []int {
	base, rem := total/assistants, total%assistants
	desired := make([]int, assistants+1)
	for i := 1; i <= assistants; i++ {
		desired[i] = base
		if i <= rem {
			desired[i]++
		}
	}
	return desired
}

func saveChangedSettings(all []*ChatSettings) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var models []mongo.WriteModel
	for _, s := range all {
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"_id": s.ChatID}).
			SetUpdate(bson.M{"$set": bson.M{"ass_index": s.AssistantIndex}}))

		chatSettingsCache.Delete(s.ChatID)

		if len(models) >= 500 {
			if _, err := chatSettingsColl.BulkWrite(ctx, models); err != nil {
				return fmt.Errorf("bulk update failed: %w", err)
			}
			models = nil
		}
	}

	if len(models) > 0 {
		if _, err := chatSettingsColl.BulkWrite(ctx, models); err != nil {
			return fmt.Errorf("bulk update failed: %w", err)
		}
	}

	cacheMu.Lock()
	indexCache = make(map[int64]int)
	cacheMu.Unlock()
	return nil
}

func rebuildAssistantUsage(all []*ChatSettings, assistantCount int) {
	counts := make([]int64, assistantCount+1)
	for _, s := range all {
		if s.AssistantIndex >= 1 && s.AssistantIndex <= assistantCount {
			counts[s.AssistantIndex]++
		}
	}

	usageMu.Lock()
	assistantUsage = counts
	usageMu.Unlock()
}

func pickLeastUsedAssistant(counts []int64) int {
	if len(counts) <= 1 {
		return 1
	}
	newIndex := 1
	minCount := counts[1]

	for i := 2; i < len(counts); i++ {
		if counts[i] < minCount {
			minCount = counts[i]
			newIndex = i
		}
	}
	return newIndex
}
