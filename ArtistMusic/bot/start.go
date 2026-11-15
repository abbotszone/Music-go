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
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"ArtistMusic/ArtistMusic/config"
	"ArtistMusic/ArtistMusic/queue"
	"ArtistMusic/ArtistMusic/store"
	"ArtistMusic/ArtistMusic/voice"
	"ArtistMusic/internal/locales"
	"ArtistMusic/internal/logger"
	"ArtistMusic/internal/utils"
	td "github.com/ArtistMusic/Artist"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

// ───────────── start ─────────────

func startHandler(c *td.Client, m *td.Message) error {
	if isChannel(c, m) {
		return nil
	}

	if !m.IsPrivate() {
		store.AddServedChat(m.ChatID())
		_, err := m.ReplyText(c, F(m.ChatID(), "start_group"), nil)
		return err
	}

	store.AddServedUser(m.ChatID())
	sender, _ := m.GetUser(c)

	if m.Args() == "pm_help" {
		if err := helpHandler(c, m); err != nil {
			return err
		}
	} else if err := handlePmStart(c, m, sender); err != nil {
		return err
	}

	logStart(c, m, sender)
	return nil
}

func startCaption(c *td.Client, chatID int64, sender *td.User, fallbackID int64) string {
	return F(chatID, "start_private", locales.Arg{
		"user": mentionOf(sender, fallbackID),
		"bot":  td.Mention(c.Me.FirstName, c.Me.Id, true, true),
	})
}

func handlePmStart(c *td.Client, m *td.Message, sender *td.User) error {
	caption := startCaption(c, m.ChatID(), sender, m.SenderID())

	if image := config.StartImage(); image != "" {
		markup := GetStartMarkup(m.ChatID())
		var err error
		if isAnimationURL(image) {
			_, err = c.SendAnimation(m.ChatID(), td.InputFileRemote{Id: image}, &td.SendAnimationOpts{
				Caption:     caption,
				ParseMode:   td.ParseModeHTML,
				ReplyMarkup: markup,
			})
		} else {
			_, err = c.SendPhoto(m.ChatID(), td.InputFileRemote{Id: image}, &td.SendPhotoOpts{
				Caption:     caption,
				ReplyMarkup: markup,
			})
		}
		if err == nil {
			return nil
		}
		c.Logger.Error("start image send failed, falling back to text", "error", err)
	}

	_, err := c.SendTextMessage(m.ChatID(), caption, &td.SendTextMessageOpts{
		ReplyMarkup: GetStartMarkup(m.ChatID()),
	})
	return err
}

func startCB(c *td.Client, cb *td.UpdateNewCallbackQuery) error {
	cb.Answer(c, 0, false, "", "")

	msg, err := cb.GetMessage(c)
	if err != nil {
		return err
	}
	sender, _ := c.GetUser(cb.SenderUserId)

	caption := startCaption(c, cb.ChatId, sender, cb.SenderUserId)
	markup := GetStartMarkup(cb.ChatId)

	if config.StartImage() == "" {
		_, err = cb.EditMessageText(c, caption, &td.EditTextMessageOpts{
			ReplyMarkup: markup,
		})
		return err
	}

	formattedCaption, err := c.GetFormattedText(caption, nil, "HTML")
	if err != nil {
		return err
	}

	content := mediaContent(config.StartImage(), formattedCaption)

	_, err = c.EditMessageMedia(cb.ChatId, content, msg.Id, &td.EditMessageMediaOpts{
		ReplyMarkup: markup,
	})
	return err
}

// isPhotoMessage reports whether m is a media message that carries a caption
// (photo, animation/GIF or video) instead of plain text.
func isPhotoMessage(m *td.Message) bool {
	switch m.Content.(type) {
	case *td.MessagePhoto, *td.MessageAnimation, *td.MessageVideo:
		return true
	}
	return false
}

// isAnimationURL reports whether the URL points to a GIF/MP4-style animation.
func isAnimationURL(u string) bool {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	u = strings.ToLower(u)
	for _, ext := range []string{".mp4", ".gif", ".webm", ".mov"} {
		if strings.HasSuffix(u, ext) {
			return true
		}
	}
	return false
}

// mediaContent builds the right input content (animation or photo) for a
// remote start/ping image URL.
func mediaContent(url string, caption *td.FormattedText) td.InputMessageContent {
	if isAnimationURL(url) {
		return &td.InputMessageAnimation{
			Animation: &td.InputAnimation{Animation: td.InputFileRemote{Id: url}},
			Caption:   caption,
		}
	}
	return &td.InputMessagePhoto{
		Photo:   &td.InputPhoto{Photo: td.InputFileRemote{Id: url}},
		Caption: caption,
	}
}

// isChannelPost reports whether m came from a broadcast channel.
func isChannel(c *td.Client, m *td.Message) bool {
	chat, err := m.GetChat(c)
	if err != nil {
		return false
	}
	sg, ok := chat.Type.(*td.ChatTypeSupergroup)
	return ok && sg.IsChannel
}

// mentionOf builds an HTML mention, falling back to a plain "user" mention
// if sender is nil (e.g. GetUser failed).
func mentionOf(sender *td.User, fallbackId int64) string {
	if sender == nil {
		return mentionOfName("user", fallbackId)
	}
	return mentionOfName(strings.TrimSpace(sender.FirstName+" "+sender.LastName), sender.Id)
}

// mentionOfName builds an HTML mention for a raw name and user ID.
func mentionOfName(name string, id int64) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "user"
	}
	return td.Mention(name, id, true, true)
}

// mentionOfAssistant builds an HTML mention for an assistant account.
func mentionOfAssistant(a *voice.Assistant) string {
	if a == nil || a.Self == nil {
		return "Unknown"
	}
	return mentionOfName(strings.TrimSpace(a.Self.FirstName+" "+a.Self.LastName), a.Self.ID)
}

// logStart reports a bot start to the configured logger chat, if enabled.
func logStart(c *td.Client, m *td.Message, sender *td.User) {
	if config.LoggerID == 0 || !isLoggerEnabled() {
		return
	}

	uName := "N/A"
	if sender != nil && sender.Usernames != nil && len(sender.Usernames.ActiveUsernames) > 0 {
		uName = "@" + sender.Usernames.ActiveUsernames[0]
	}

	msg := F(config.LoggerID, "logger_bot_started", locales.Arg{
		"mention":       mentionOf(sender, m.SenderID()),
		"user_id":       m.SenderID(),
		"user_username": uName,
	})

	if _, err := c.SendTextMessage(config.LoggerID, msg, nil); err != nil {
		c.Logger.Error("Logger send failed", "error", err)
	}
}

// ───────────── help ─────────────

var helpTexts = map[string]string{}

func init() {
	helpTexts["/help"] = fmt.Sprintf(`◇ <b>Help Command</b>
<i>Displays general bot help or detailed information about a specific command.</i>

<u>Usage:</u>
<code>/help</code> — Show the main help menu.  
<code>/help &lt;command&gt;</code> — Show help for a specific command.

For more info, visit our <a href="%s">Support Chat</a>.`, config.SupportChat)
}

func helpHandler(c *td.Client, m *td.Message) error {
	if isChannel(c, m) {
		return nil
	}

	args := strings.Fields(m.Text())
	if len(args) > 1 {
		cmd := args[1]
		if cmd != "pm_help" {
			if !strings.HasPrefix(cmd, "/") {
				cmd = "/" + cmd
			}
			return showHelpFor(c, m, cmd)
		}
	}

	if !m.IsPrivate() {
		_, err := m.ReplyText(c, F(m.ChatID(), "help_private_only"), &td.SendTextMessageOpts{
			ReplyMarkup: GetGroupHelpKeyboard(m.ChatID()),
		})
		return err
	}

	_, err := m.ReplyText(c, F(m.ChatID(), "help_main"), &td.SendTextMessageOpts{
		ReplyMarkup: GetHelpKeyboard(m.ChatID()),
	})
	return err
}

func helpCB(c *td.Client, cb *td.UpdateNewCallbackQuery) error {
	cb.Answer(c, 0, false, "", "")

	msg, err := cb.GetMessage(c)
	if err != nil {
		return err
	}

	markup := GetHelpKeyboard(cb.ChatId)

	if isPhotoMessage(msg) {
		_, err = cb.EditMessageCaption(c, F(cb.ChatId, "help_main"), &td.EditCaptionOpts{
			ReplyMarkup: markup,
		})
	} else {
		_, err = cb.EditMessageText(c, F(cb.ChatId, "help_main"), &td.EditTextMessageOpts{
			ReplyMarkup: markup,
		})
	}
	return err
}

func helpCallbackHandler(c *td.Client, cb *td.UpdateNewCallbackQuery) error {
	data := cb.DataString()
	cb.Answer(c, 0, false, "", "")
	if data == "" {
		return nil
	}
	chatID := cb.ChatId
	parts := strings.SplitN(data, ":", 2)
	if len(parts) < 2 {
		return nil
	}

	var text string
	btn := GetBackKeyboard(chatID)

	switch parts[1] {
	case "admin":
		text = F(chatID, "help_admin")
	case "bcast":
		text = F(chatID, "help_bcast")
	case "play":
		text = F(chatID, "help_play")
	case "thumb":
		text = F(chatID, "help_thumb")
	case "start":
		text = F(chatID, "help_start")
	case "autoplay":
		text = F(chatID, "help_autoplay")
	case "inline":
		text = F(chatID, "help_inline")
	case "main":
		text = F(chatID, "help_main")
		btn = GetHelpKeyboard(chatID)
	}

	msg, err := cb.GetMessage(c)
	if err != nil {
		return err
	}

	if isPhotoMessage(msg) {
		// Telegram captions are capped at 1024 UTF-16 units; longer help pages
		// are sent as a plain text message instead of being edited into the photo.
		if len(utf16.Encode([]rune(text))) <= 1024 {
			_, err = cb.EditMessageCaption(c, text, &td.EditCaptionOpts{
				ReplyMarkup: btn,
			})
		} else {
			_ = msg.Delete(c, true)
			_, err = c.SendTextMessage(chatID, text, &td.SendTextMessageOpts{
				ParseMode:   "HTML",
				ReplyMarkup: btn,
			})
		}
	} else {
		_, err = cb.EditMessageText(c, text, &td.EditTextMessageOpts{
			ReplyMarkup: btn,
		})
	}
	return err
}

func showHelpFor(c *td.Client, m *td.Message, cmd string) error {
	helpText, ok := helpTexts[cmd]
	if !ok {
		trimmed := strings.TrimPrefix(cmd, "/")
		if value, exists := helpTexts[trimmed]; exists {
			helpText = value
		}
	}

	if helpText == "" {
		_, err := m.ReplyText(
			c,
			"ϟ <i>No help found for command <code>"+
				html.EscapeString(cmd)+
				"</code></i>",
			nil,
		)
		return err
	}

	_, err := m.ReplyText(
		c,
		"◇ <b>Help for</b> <code>"+cmd+"</code>:\n\n"+helpText,
		nil,
	)
	return err
}

// ───────────── ping ─────────────

func init() {
	helpTexts["/ping"] = `<i>Check bot responsiveness and system stats.</i>

<u>Usage:</u>
<b>/ping</b> — Get bot status

<b>◇ Information Shown:</b>
• Response latency (ms)
• Uptime
• RAM usage
• CPU usage
• Disk usage

<b>◇ Use Case:</b>
Check if bot is responsive and view system health.`
}

func formatUptime(d time.Duration) string {
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute
	d -= minutes * time.Minute
	seconds := d / time.Second

	result := ""
	if days > 0 {
		result += fmt.Sprintf("%dd ", days)
	}
	if hours > 0 {
		result += fmt.Sprintf("%dh ", hours)
	}
	if minutes > 0 {
		result += fmt.Sprintf("%dm ", minutes)
	}
	result += fmt.Sprintf("%ds", seconds)
	return result
}

func pingHandler(c *td.Client, m *td.Message) error {
	if isChannel(c, m) {
		return nil
	}

	chatID := m.ChatID()

	if m.IsPrivate() {
		m.Delete(c, true)
		store.AddServedUser(chatID)
	} else {
		store.AddServedChat(chatID)
	}

	start := time.Now()
	reply, err := m.ReplyText(c, F(chatID, "ping_start"), nil)
	if err != nil {
		return err
	}

	latency := time.Since(start).Milliseconds()
	uptime := time.Since(config.StartTime)
	uptimeStr := formatUptime(uptime)
	ramInfo := "N/A"
	cpuUsage := "N/A"
	diskUsage := "N/A"

	v, err := mem.VirtualMemory()
	if err == nil {
		usedGB := float64(v.Used) / 1024 / 1024 / 1024
		totalGB := float64(v.Total) / 1024 / 1024 / 1024

		ramInfo = fmt.Sprintf("%.2f / %.2f GB", usedGB, totalGB)
	}

	if percentages, err := cpu.Percent(time.Second, false); err == nil &&
		len(percentages) > 0 {
		cpuUsage = fmt.Sprintf("%.2f%%", percentages[0])
	}

	if d, err := disk.Usage("/"); err == nil {
		usedGB := float64(d.Used) / 1024 / 1024 / 1024
		totalGB := float64(d.Total) / 1024 / 1024 / 1024
		diskUsage = fmt.Sprintf("%.2f / %.2f GB", usedGB, totalGB)
	}

	msg := F(chatID, "ping_result", locales.Arg{
		"latency":    latency,
		"bot":        td.Mention(c.Me.FirstName, c.Me.Id, true, true),
		"uptime":     uptimeStr,
		"ram_info":   ramInfo,
		"cpu_usage":  cpuUsage,
		"disk_usage": diskUsage,
	})

	markup := SuppMarkup(chatID)

	edit := func() error {
		if config.PingImage == "" {
			_, err := reply.EditText(c, msg, &td.EditTextMessageOpts{
				ParseMode:   td.ParseModeHTML,
				ReplyMarkup: markup,
			})
			return err
		}

		caption, err := c.GetFormattedText(msg, nil, td.ParseModeHTML)
		if err != nil {
			return err
		}

		_, err = reply.EditMedia(c, mediaContent(config.PingImage, caption), &td.EditMessageMediaOpts{
			ReplyMarkup: markup,
		})
		return err
	}

	if err := edit(); err != nil {
		logger.Errorf("[ping] edit failed: %v", err)

		if config.PingImage != "" {
			_, err = reply.EditText(c, msg, &td.EditTextMessageOpts{
				ParseMode:   td.ParseModeHTML,
				ReplyMarkup: markup,
			})
			if err != nil {
				logger.Errorf("[ping] fallback text edit failed: %v", err)
				return err
			}
		}
	}
	return nil
}

// ───────────── restart ─────────────

func init() {
	helpTexts["/restart"] = `<i>Restart the bot process.</i>

<u>Usage:</u>
<b>/restart</b> — Restart bot

<b>❖ Behavior:</b>
• Stops all active rooms
• Notifies all active chats
• Restarts bot process
• Clears download cache

<b>◆ Restrictions:</b>
• <b>Owner only</b> command

<b>ϟ Warning:</b>
All playback will be interrupted. Bot will be offline for a few seconds.`
}

func handleRestart(c *td.Client, m *td.Message) error {
	if !checkOwner(c, m) {
		return nil
	}
	chatID := m.ChatID()
	r, ok := getActiveRoomForChat(chatID)
	if ok && r.Track() != nil {
		_, err := m.ReplyText(c, F(chatID, "restart_confirm_running"), &td.SendTextMessageOpts{
			ReplyMarkup: GetRestartConfirmMarkup(chatID),
		})
		return err
	}
	return performRestart(c, m, chatID)
}

func performRestart(c *td.Client, m *td.Message, chatID int64) error {
	statusMsg, err := m.ReplyText(c, F(chatID, "restart"), nil)
	if err != nil {
		logger.Error("Failed to send restart message: " + err.Error())
	}
	return executeRestart(c, chatID, statusMsg)
}

func getActiveRoomForChat(chatID int64) (*queue.RoomState, bool) {
	r, ok := queue.RoomFor(chatID)
	if !ok || !r.Active() || r.Track() == nil {
		return nil, false
	}
	return r, true
}

func restartConfirmHandler(c *td.Client, u *td.UpdateNewCallbackQuery) error {
	chatID := u.ChatId

	if u.SenderUserId != config.OwnerID {
		_ = u.Answer(c, 0, true, F(chatID, "only_owner"), "")
		return nil
	}

	action := strings.TrimPrefix(u.DataString(), "restart:")
	switch action {
	case "bot":
		_ = u.Answer(c, 0, false, F(chatID, "restart_confirm_bot"), "")
		statusMsg, _ := u.EditMessageText(c, F(chatID, "restart"), nil)
		return executeRestart(c, chatID, statusMsg)
	case "replay":
		r, ok := getActiveRoomForChat(chatID)
		if !ok {
			_ = u.Answer(c, 0, true, F(chatID, "room_no_active"), "")
			return nil
		}
		if err := r.Replay(); err != nil {
			_ = u.Answer(c, 0, true, F(chatID, "replay_failed", locales.Arg{"error": err}), "")
			return nil
		}
		_, _ = u.EditMessageText(c, F(chatID, "restart_confirm_replay_done"), nil)
		_ = u.Answer(c, 0, true, F(chatID, "restart_confirm_replay_done"), "")
	}

	return nil
}

func executeRestart(
	c *td.Client,
	chatID int64,
	statusMsg *td.Message,
) error {
	exePath, err := os.Executable()
	if err != nil {
		utils.EOR(c, statusMsg, F(chatID, "restart_exepath_fail", locales.Arg{
			"error": err.Error(),
		}), nil)
		return nil
	}

	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		utils.EOR(c, statusMsg, F(chatID, "restart_symlink_fail", locales.Arg{
			"error": err.Error(),
		}), nil)
		return nil
	}

	for roomChatID := range queue.AllRooms() {
		queue.DropRoom(roomChatID)
		if _, err := c.SendTextMessage(roomChatID, F(roomChatID, "restart_service", locales.Arg{
			"bot": mentionOf(c.Me, c.Me.Id),
		}), nil); err != nil {
			return err
		}
		time.Sleep(time.Second)
	}

	utils.EOR(c, statusMsg, F(chatID, "restart_initiated"), nil)

	_ = os.RemoveAll("downloads")
	_ = os.RemoveAll("cache")

	if err := syscall.Exec(exePath, os.Args, os.Environ()); err != nil {
		utils.EOR(c, statusMsg, F(chatID, "restart_fail", locales.Arg{
			"error": err.Error(),
		}), nil)
	}

	return nil
}
