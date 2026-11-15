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

package database

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

