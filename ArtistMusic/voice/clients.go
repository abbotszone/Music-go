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

package voice

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"ArtistMusic/ArtistMusic/config"
	"ArtistMusic/ArtistMusic/store"
	"ArtistMusic/internal/logger"
	td "github.com/ArtistMusic/Artist"
	gotdlogger "github.com/ArtistMusic/Artist/logger"
	"github.com/amarnathcjd/gogram/telegram"
)

// ───────────── clients ─────────────

var (
	Assistants *AssistantManager
)

// Bot is the main bot client (set by Init).
var Bot *td.Client

// BotUsername returns the main bot's active @username (empty if unknown).
func BotUsername() string {
	if Bot == nil || Bot.Me == nil || Bot.Me.Usernames == nil {
		return ""
	}
	usernames := Bot.Me.Usernames.ActiveUsernames
	if len(usernames) == 0 {
		return ""
	}
	return usernames[0]
}

// Init initializes the bot and assistant clients.
// It returns a shutdown function and an error if initialization fails.
func Init() (func(), error) {
	logger.Info("Starting bot client...")
	if err := initBot(); err != nil {
		return nil, err
	}

	logger.Info("Starting assistant clients...")
	if err := initAssistants(); err != nil {
		return nil, fmt.Errorf("assistants initialization: %w", err)
	}

	shutdown := func() {
		logger.Info("Stopping bot...")
		Bot.Close()

		logger.Info("Shutting down assistants...")
		Assistants.ForEach(func(a *Assistant) {
			a.Ntg.Close()
			a.Client.Stop()
		})

		logger.Info("Shutdown complete.")
	}

	return shutdown, nil
}

func getTdjsonPath() string {
	if p := os.Getenv("TDJSON_PATH"); p != "" {
		return p
	}
	candidates := []string{
		"./libtdjson.so.1.8.66",
		"/app/libtdjson.so.1.8.66",
		"./libtdjson.so",
		"/app/libtdjson.so",
		"libtdjson.so.1.8.66",
		"libtdjson.so",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "./libtdjson.so.1.8.66"
}

func initBot() error {
	opts := td.DefaultClientConfig()
	opts.LibraryPath = getTdjsonPath()
	opts.ParseMode = td.ParseModeHTML
	opts.PanicHandler = clientPanicHandler
	opts.ErrorHandler = clientErrorHandler
	opts.AutoRetry = &td.AutoRetry{
		ChatNotFound:    true,
		MessageNotFound: true,
		MaxFloodWait:    30 * time.Second,
	}
	opts.DatabaseDirectory = "database"
	opts.Logger = gotdlogger.New(gotdlogger.WithHandler(
		logger.NewHandler(os.Stderr, logger.InfoLevel),
	))

	client, err := td.NewClient(config.APIID, config.APIHash, config.Token, opts)
	if err != nil {
		return fmt.Errorf("failed to create bot client: %w", err)
	}

	err = client.Start()
	if err != nil {
		return fmt.Errorf("failed to start bot: %w", err)
	}
	Bot = client

	if config.LoggerID != 0 {
		_, _ = client.SendTextMessage(
			config.LoggerID,
			fmt.Sprintf(
				"➲ <b>ʙσᴛ sᴛᴧʀᴛєᴅ</b>\n\n<b>ʙσᴛ:</b> @%s\n<b>ᴛɪᴍє:</b> %s",
				BotUsername(),
				time.Now().Format("02 Jan 2006 • 15:04"),
			),
			&td.SendTextMessageOpts{ParseMode: "HTML"},
		)
	}

	logger.Infof("Bot started as @%s", BotUsername())

	return nil
}

// clientPanicHandler marshals the raw update that caused a handler panic and
// forwards it to the logger chat together with the panic value and stack trace.
func clientPanicHandler(c *td.Client, update td.TlObject, r any) {
	sendErrorReport(c, "panic", update, r, string(debug.Stack()))
}

// clientErrorHandler does the same for handler errors, then lets the library
// continue dispatching the remaining handlers.
func clientErrorHandler(c *td.Client, update td.TlObject, err error) error {
	sendErrorReport(c, "error", update, err, "")
	return nil
}

func sendErrorReport(c *td.Client, kind string, update td.TlObject, detail any, stack string) {
	if c == nil || config.LoggerID == 0 {
		return
	}

	var raw string
	if update != nil {
		if b, err := json.MarshalIndent(update, "", "  "); err == nil {
			raw = string(b)
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("<b>Bot %s:</b> %s\n\n", strings.ToUpper(kind), html.EscapeString(fmt.Sprint(detail))))

	if raw != "" {
		b.WriteString("<pre>")
		b.WriteString("Raw update:\n")
		b.WriteString(html.EscapeString(raw))
		b.WriteString("</pre>\n\n")
	}

	if stack != "" {
		b.WriteString("<pre>")
		b.WriteString("Stack trace:\n")
		b.WriteString(html.EscapeString(stack))
		b.WriteString("</pre>")
	}

	text := b.String()

	go func() {
		defer func() { _ = recover() }()
		if len(text) > 4000 {
			sendErrorReportFile(c, text)
			return
		}
		_, _ = c.SendTextMessage(config.LoggerID, text, &td.SendTextMessageOpts{
			ParseMode: "HTML",
		})
	}()
}

func sendErrorReportFile(c *td.Client, text string) {
	f, err := os.CreateTemp("", "bot_error_*.txt")
	if err != nil {
		return
	}
	name := f.Name()

	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return
	}
	_ = f.Close()

	// TDLib uploads the file asynchronously, so keep it around for a while
	// instead of deleting it as soon as the send call returns.
	time.AfterFunc(5*time.Minute, func() { _ = os.Remove(name) })

	_, _ = c.SendMessage(config.LoggerID, &td.InputMessageDocument{
		Document: &td.InputDocument{Document: &td.InputFileLocal{Path: name}},
	}, nil)
}

func initAssistants() error {
	assistantList := make([]*Assistant, 0, len(config.StringSessions))

	for i, sessionStr := range config.StringSessions {
		logger.Infof("Initializing assistant[%d]...", i)

		assistant, err := initAssistant(sessionStr, i)
		if err != nil {
			return fmt.Errorf("failed to initialize assistant[%d]: %w", i, err)
		}

		assistantList = append(assistantList, assistant)

		if assistant.Self.Username != "" {
			logger.Infof(
				"Assistant[%d] started as @%s",
				i,
				assistant.Self.Username,
			)
		} else {
			logger.Infof("Assistant[%d] started as %s", i, assistant.Self.FirstName)
		}
	}

	Assistants = &AssistantManager{
		list: assistantList,
	}
	return nil
}

func initAssistant(
	sessionStr string,
	index int,
) (*Assistant, error) {
	stringSession, err := resolveSession(sessionStr)
	if err != nil {
		return nil, fmt.Errorf("resolving session: %w", err)
	}

	client, err := telegram.NewClient(telegram.ClientConfig{
		AppID:         config.APIID,
		AppHash:       config.APIHash,
		LogLevel:      telegram.LogError,
		ParseMode:     "HTML",
		StringSession: stringSession,
		Session:       fmt.Sprintf("ass_%d.session", index),
	})
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}

	user, err := client.GetMe()
	if err != nil {
		return nil, fmt.Errorf("fetching identity: %w", err)
	}

	client.SetCommandPrefixes(".")

	return &Assistant{
		Index:  index,
		Client: client,
		Self:   user,
		Ntg:    NewContext(client),
	}, nil
}

func resolveSession(session string) (string, error) {
	switch strings.ToLower(config.SessionType) {
	case "pyrogram", "pyro":
		sess, err := decodePyrogramSessionString(session)
		if err != nil {
			return "", fmt.Errorf("decoding Pyrogram session: %w", err)
		}
		return sess.Encode(), nil

	case "telethon":
		sess, err := decodeTelethonSessionString(session)
		if err != nil {
			return "", fmt.Errorf("decoding Telethon session: %w", err)
		}
		return sess.Encode(), nil

	case "gogram":
		return session, nil

	default:
		return "", fmt.Errorf("invalid SESSION_TYPE: %s", config.SessionType)
	}
}

func decodePyrogramSessionString(
	encodedString string,
) (*telegram.Session, error) {
	// SESSION_STRING_FORMAT: Big-endian, uint8, uint32, bool, 256-byte array, uint64, bool
	const (
		dcIDSize     = 1 // uint8
		apiIDSize    = 4 // uint32
		testModeSize = 1 // bool (uint8)
		authKeySize  = 256
		userIDSize   = 8 // uint64
		isBotSize    = 1 // bool (uint8)
	)

	// Add padding to the base64 string if necessary
	for len(encodedString)%4 != 0 {
		encodedString += "="
	}

	packedData, err := base64.URLEncoding.DecodeString(encodedString)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 string: %w", err)
	}

	// Pyrogram v1 sessions (">B?256sI?", 263 bytes) carry no api_id and use a
	// 32-bit user id, so the configured API_ID is used for them.
	const legacySize = dcIDSize + testModeSize + authKeySize + 4 + isBotSize
	if len(packedData) == legacySize {
		return &telegram.Session{
			Hostname: telegram.ResolveDC(
				int(packedData[0]),
				packedData[1] != 0,
				false,
			),
			AppID: config.APIID,
			Key:   packedData[2 : 2+authKeySize],
		}, nil
	}

	expectedSize := dcIDSize + apiIDSize + testModeSize + authKeySize + userIDSize + isBotSize
	if len(packedData) != expectedSize {
		return nil, fmt.Errorf(
			"unexpected data length: got %d, want %d",
			len(packedData),
			expectedSize,
		)
	}

	return &telegram.Session{
		Hostname: telegram.ResolveDC(
			int(uint8(packedData[0])),
			packedData[5] != 0,
			false,
		),
		AppID: int32(
			uint32(
				packedData[1],
			)<<24 | uint32(
				packedData[2],
			)<<16 | uint32(
				packedData[3],
			)<<8 | uint32(
				packedData[4],
			),
		),
		Key: packedData[6 : 6+authKeySize],
	}, nil
}

func decodeTelethonSessionString(
	sessionString string,
) (*telegram.Session, error) {
	data, err := base64.URLEncoding.DecodeString(sessionString[1:])
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %v", err)
	}

	ipLen := 4
	if len(data) == 352 {
		ipLen = 16
	}

	expectedLen := 1 + ipLen + 2 + 256
	if len(data) != expectedLen {
		return nil, fmt.Errorf("invalid session string length")
	}

	// ">B{}sH256s"
	offset := 1

	// IP Address (4 or 16 bytes based on IPv4 or IPv6)
	ipData := data[offset : offset+ipLen]
	ip := net.IP(ipData)
	ipAddress := ip.String()
	offset += ipLen

	// Port (2 bytes, Big Endian)
	port := binary.BigEndian.Uint16(data[offset : offset+2])
	offset += 2

	// Auth Key (256 bytes)
	var authKey [256]byte
	copy(authKey[:], data[offset:offset+256])

	return &telegram.Session{
		Hostname: ipAddress + ":" + fmt.Sprint(port),
		Key:      authKey[:],
	}, nil
}

// ───────────── assistant ─────────────

type Assistant struct {
	Index  int
	Client *telegram.Client
	Self   *telegram.UserObj
	Ntg    *Context
}

type AssistantManager struct {
	list []*Assistant
}

func (m *AssistantManager) Count() int {
	if m == nil {
		return 0
	}
	return len(m.list)
}

func (m *AssistantManager) Get(idx int) (*Assistant, error) {
	if m == nil {
		return nil, fmt.Errorf("assistant manager not initialized")
	}
	if idx < 1 || idx > len(m.list) {
		return nil, fmt.Errorf("assistant index out of range: %d", idx)
	}
	return m.list[idx-1], nil
}

func (m *AssistantManager) First() (*Assistant, error) {
	return m.Get(1)
}

func (m *AssistantManager) ForEach(fn func(*Assistant)) {
	if m == nil {
		return
	}
	for _, a := range m.list {
		fn(a)
	}
}

func (m *AssistantManager) WithAssistant(chatID int64, fn func(*Assistant)) {
	if m == nil {
		return
	}

	ass, err := m.ForChat(chatID)
	if err != nil {
		logger.Errorf(
			"Failed to get assistant for chat %d, Error: %v",
			chatID,
			err,
		)
		return
	}

	fn(ass)
}

func (m *AssistantManager) ForChat(chatID int64) (*Assistant, error) {
	if m == nil || len(m.list) == 0 {
		return nil, fmt.Errorf("no assistants available")
	}

	idx, err := store.GetAssistant(chatID)
	if err != nil {
		return nil, err
	}

	return m.Get(idx)
}
