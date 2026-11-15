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

package bot

import (
	"strings"

	"ArtistMusic/ArtistMusic/config"
	"ArtistMusic/ArtistMusic/store"
	"ArtistMusic/internal/logger"
	"ArtistMusic/internal/utils"
	td "github.com/ArtistMusic/Artist"
)

// ───────────── filters ─────────────

func getCommand(m *td.Message) string {
	parts := strings.Fields(m.Text())
	if len(parts) == 0 {
		return ""
	}
	cmd, _, _ := strings.Cut(parts[0], "@")
	return cmd
}

func checkOwner(c *td.Client, m *td.Message) bool {
	if config.OwnerID == 0 || m.SenderID() != config.OwnerID {
		if _, err := m.ReplyText(c, F(m.ChatID(), "only_owner"), nil); err != nil {
			logger.Error(err)
		}
		return false
	}
	return true
}

func isSuperGroup(c *td.Client, m *td.Message) bool {
	if m.IsPrivate() {
		if _, err := m.ReplyText(c, F(m.ChatID(), "only_supergroup"), nil); err != nil {
			logger.Error(err)
		}
		store.AddServedUser(m.ChatID())
		return false
	}

	chat, err := m.GetChat(c)
	if err != nil {
		return false
	}

	sg, ok := chat.Type.(*td.ChatTypeSupergroup)
	if !ok || sg.IsChannel {
		return false
	}

	store.AddServedChat(m.ChatID())
	return true
}

func filterAuthUsers(c *td.Client, m *td.Message) bool {
	if canUseAdminCommand(c, m.ChatID(), m.SenderID()) {
		return true
	}

	mode, err := store.GetAdminMode(m.ChatID())
	if err == nil && mode == store.AdminModeAdminsOnly {
		if _, err := m.ReplyText(c, F(m.ChatID(), "only_admin"), nil); err != nil {
			logger.Error(err)
		}
	} else {
		if _, err := m.ReplyText(c, F(m.ChatID(), "only_admin_or_auth"), nil); err != nil {
			logger.Error(err)
		}
	}
	return false
}

func canUseAdminCommand(c *td.Client, chatID, userID int64) bool {
	if isOwnerOrSudo(userID) {
		return true
	}

	mode, err := store.GetAdminMode(chatID)
	if err != nil {
		mode = store.AdminModeAdminAuth
	}

	if mode == store.AdminModeEveryone {
		return true
	}

	isAdmin, err := utils.IsChatAdmin(c, chatID, userID)
	if err == nil && isAdmin {
		return true
	}

	if mode == store.AdminModeAdminsOnly {
		return false
	}

	isAuth, err := store.IsAuthorized(chatID, userID)
	return err == nil && isAuth
}

func isOwnerOrSudo(userID int64) bool {
	if config.OwnerID != 0 && userID == config.OwnerID {
		return true
	}
	isSudo, err := store.IsSudo(userID)
	return err == nil && isSudo
}
