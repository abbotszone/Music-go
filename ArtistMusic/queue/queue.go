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

package queue

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	state "ArtistMusic/ArtistMusic/models"
	"ArtistMusic/ArtistMusic/store"
	"ArtistMusic/ArtistMusic/voice"
	"ArtistMusic/internal/artistcalls"
	"ArtistMusic/internal/logger"
	"ArtistMusic/internal/utils"
	td "github.com/ArtistMusic/Artist"
	"github.com/amarnathcjd/gogram/telegram"
)

// ───────────── room ─────────────

var (
	rooms   = make(map[int64]*RoomState)
	roomsMu sync.RWMutex

	ErrRoomDestroyed = errors.New("room destroyed")

	FileCacheDuration = 1 * time.Minute
)

// RoomState holds all playback state for a single chat.
type RoomState struct {
	mu sync.RWMutex

	// ID is the canonical playback target id (group or linked channel).
	ID int64
	// ChatID is the UI/context chat id where messages and controls are sent.
	ChatID int64

	filePath  string       // currently playing local media file
	track     *state.Track // active track metadata
	playing   bool         // whether playback is active
	paused    bool         // whether playback is paused
	muted     bool         // whether playback is muted
	speed     float64      // playback speed multiplier
	position  int          // current playback position in seconds
	updatedAt int64        // last state-update timestamp (unix seconds)
	loop      int          // loop mode/state value

	queue   []*state.Track // upcoming tracks
	shuffle bool           // queue shuffle mode

	autoplay      bool     // auto-play recommendations when the queue ends
	playedHistory []string // ring buffer of recent track IDs and normalized titles
	engineToken   string   // cryptographic session verification token for autoplay

	statusMsg *td.Message // latest status message in chat

	Assistant *voice.Assistant  // assistant client bound to this room
	destroyed atomic.Bool // whether room cleanup has completed
}

// Room management

func DropRoom(chatID int64) bool {
	_, file, line, _ := runtime.Caller(1)
	logger.Debugf("DeleteRoom called from %s:%d", file, line)

	roomsMu.Lock()
	room, ok := rooms[chatID]
	if !ok || room == nil || room.destroyed.Load() {
		roomsMu.Unlock()
		return false
	}

	delete(rooms, chatID)
	roomsMu.Unlock()

	room.cleanupFile()
	room.Stop()
	room.destroyed.Store(true)
	return true
}

// RoomFor returns the existing room for a chat.
func RoomFor(chatID int64) (*RoomState, bool) {
	roomsMu.RLock()
	room, exists := rooms[chatID]
	roomsMu.RUnlock()
	return room, exists
}

// CreateRoom returns the room for a chat, creating it if it does not exist.
func CreateRoom(chatID int64, ass *voice.Assistant) *RoomState {
	room, _ := createNewRoom(chatID, ass)
	return room
}

func createNewRoom(chatID int64, ass *voice.Assistant) (*RoomState, bool) {
	roomsMu.Lock()
	defer roomsMu.Unlock()

	room, exists := rooms[chatID]
	if !exists {
		persistedAutoplay, _ := store.Autoplay(chatID)
		room = &RoomState{
			ID:            chatID,
			ChatID:        chatID,
			queue:         []*state.Track{},
			speed:         1.0,
			autoplay:      persistedAutoplay,
			playedHistory: make([]string, 0, 100),
			engineToken:   fmt.Sprintf("ap-%d-%d", chatID, time.Now().UnixNano()),
			Assistant:     ass,
		}
		rooms[chatID] = room
	}

	return room, true
}

// SetAssistant rebinds the room to another assistant (used after the
// assistant for the chat was switched).
func (r *RoomState) SetAssistant(ass *voice.Assistant) {
	r.mu.Lock()
	r.Assistant = ass
	r.mu.Unlock()
}

func AllRooms() map[int64]*RoomState {
	roomsMu.RLock()

	out := make(map[int64]*RoomState, len(rooms))
	var dead []int64

	for chatID, room := range rooms {
		if room == nil || room.destroyed.Load() {
			dead = append(dead, chatID)
			continue
		}
		out[chatID] = room
	}

	roomsMu.RUnlock()

	if len(dead) > 0 {
		roomsMu.Lock()
		for _, chatID := range dead {
			if room := rooms[chatID]; room == nil || room.destroyed.Load() {
				delete(rooms, chatID)
			}
		}
		roomsMu.Unlock()
	}

	return out
}

// Helpers

func (r *RoomState) Destroyed() bool {
	return r.destroyed.Load()
}

func (r *RoomState) updatePosition() {
	if r == nil || r.track == nil || r.updatedAt == 0 {
		return
	}

	current := time.Now().Unix()
	elapsed := float64(current - r.updatedAt)

	if r.playing && !r.paused {
		r.position += int(elapsed * r.speed)
		if r.position >= r.track.Duration {
			r.position = r.track.Duration
			r.playing = false
		}
	}
	r.updatedAt = current
}

// Getters

func (r *RoomState) FilePath() string {
	if r.Destroyed() {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.filePath
}

func (r *RoomState) Loop() int {
	if r.Destroyed() {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loop
}

func (r *RoomState) Position() int {
	if r.Destroyed() {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.position
}

func (r *RoomState) Queue() []*state.Track {
	if r.Destroyed() {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	q := make([]*state.Track, len(r.queue))
	copy(q, r.queue)
	return q
}

func (r *RoomState) Shuffle() bool {
	if r.Destroyed() {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.shuffle
}

func (r *RoomState) Autoplay() bool {
	if r.Destroyed() {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.autoplay
}

func (r *RoomState) Speed() float64 {
	if r.Destroyed() {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.speed
}

func (r *RoomState) Track() *state.Track {
	if r.Destroyed() {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.track
}

func (r *RoomState) StatusMsg() *td.Message {
	if r.Destroyed() {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.statusMsg
}

// Setters

func (r *RoomState) SetLoop(loop int) {
	if r.Destroyed() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loop = loop
}

func (r *RoomState) SetShuffle(enabled bool) {
	if r.Destroyed() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shuffle = enabled
}

func (r *RoomState) SetAutoplay(enabled bool) {
	if r.Destroyed() {
		return
	}
	r.mu.Lock()
	r.autoplay = enabled
	chatID := r.ChatID
	r.mu.Unlock()

	_ = store.SetAutoplay(chatID, enabled)
}

func (r *RoomState) addPlayedLocked(t *state.Track) {
	if t == nil {
		return
	}
	if t.ID != "" {
		r.playedHistory = append(r.playedHistory, "id:"+t.ID)
	}
	if norm := state.NormalizeTrackTitle(t.Title); norm != "" {
		r.playedHistory = append(r.playedHistory, "title:"+norm)
	}
	if len(r.playedHistory) > 100 {
		r.playedHistory = r.playedHistory[len(r.playedHistory)-100:]
	}
}

// AddPlayed records a track into the room's history ring buffer.
func (r *RoomState) AddPlayed(t *state.Track) {
	if r == nil || t == nil || r.Destroyed() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addPlayedLocked(t)
}

// HasPlayed checks if a track ID or title was recently played.
func (r *RoomState) HasPlayed(id, title string) bool {
	if r == nil || r.Destroyed() {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	targetID := "id:" + id
	targetTitle := ""
	if title != "" {
		targetTitle = "title:" + state.NormalizeTrackTitle(title)
	}

	for _, item := range r.playedHistory {
		if id != "" && item == targetID {
			return true
		}
		if targetTitle != "" && item == targetTitle {
			return true
		}
	}
	return false
}

// PlayedHistory returns a copy of recently played track identifiers.
func (r *RoomState) PlayedHistory() []string {
	if r == nil || r.Destroyed() {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.playedHistory))
	copy(out, r.playedHistory)
	return out
}

// ClearPlayedHistory resets the room's playback history buffer.
func (r *RoomState) ClearPlayedHistory() {
	if r == nil || r.Destroyed() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.playedHistory = make([]string, 0, 100)
}

// AutoplayToken returns the room's unique autoplay session validation token.
func (r *RoomState) AutoplayToken() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.engineToken
}

// ValidateAutoplayEngine checks if autoplay is active and verified for this room.
func (r *RoomState) ValidateAutoplayEngine() bool {
	if r == nil || r.Destroyed() {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.autoplay && r.engineToken != ""
}

func (r *RoomState) SetStatusMsg(m *td.Message) {
	if r.Destroyed() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statusMsg = m
}

// State checks

func (r *RoomState) Active() bool {
	if r.Destroyed() {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updatePosition()
	return r.track != nil && r.playing
}

func (r *RoomState) Paused() bool {
	if r.Destroyed() {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.paused && r.track != nil && r.playing
}

func (r *RoomState) Muted() bool {
	if r.Destroyed() {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.muted && r.track != nil && r.playing
}

func (r *RoomState) Parse() {
	if r.Destroyed() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updatePosition()
}

// Queue

// NextTrack retrieves and prepares the next track in queue
func (r *RoomState) NextTrack() *state.Track {
	if r.Destroyed() {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.track != nil && r.loop > 0 {
		r.position = 0
		r.playing = false
		r.paused = false
		r.muted = false
		r.loop--
		r.updatedAt = time.Now().Unix()
		return r.track
	}

	r.releaseFile()

	if len(r.queue) == 0 {
		return nil
	}

	index := 0
	if r.shuffle {
		index = rand.Intn(len(r.queue))
	}

	next := r.queue[index]
	r.queue = append(r.queue[:index], r.queue[index+1:]...)

	r.track = next
	r.addPlayedLocked(next)
	r.position = 0
	r.playing = false
	r.paused = false
	r.muted = false
	r.updatedAt = time.Now().Unix()

	return next
}

// RemoveTrack removes track(s) from queue
func (r *RoomState) RemoveTrack(index int) {
	if r.Destroyed() {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if index == -1 {
		r.queue = []*state.Track{}
		return
	}

	if index >= 0 && index < len(r.queue) {
		r.queue = append(r.queue[:index], r.queue[index+1:]...)
	}
}

// MoveTrack moves a track from one position to another
func (r *RoomState) MoveTrack(from, to int) {
	if r.Destroyed() {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if from < 0 || from >= len(r.queue) ||
		to < 0 || to >= len(r.queue) ||
		from == to {
		return
	}

	item := r.queue[from]
	r.queue = append(r.queue[:from], r.queue[from+1:]...)

	if to >= len(r.queue) {
		r.queue = append(r.queue, item)
	} else {
		r.queue = append(r.queue[:to], append([]*state.Track{item}, r.queue[to:]...)...)
	}
}

// AddTracks appends multiple tracks to the queue
func (r *RoomState) AddTracks(tracks []*state.Track) {
	if r.Destroyed() {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.queue = append(r.queue, tracks...)
}

// Files

func isTrackUsed(trackID string, skipChatID int64) bool {
	for _, room := range rooms {
		if room == nil || room.track == nil || room.ChatID == skipChatID {
			continue
		}
		if room.track.ID == trackID {
			return true
		}
		if isTrackInQueue(trackID, room.queue) {
			return true
		}
	}
	return false
}

func isTrackInQueue(trackID string, queue []*state.Track) bool {
	limit := min(len(queue), 2)
	for _, q := range queue[:limit] {
		if q != nil && q.ID == trackID {
			return true
		}
	}
	return false
}

func (r *RoomState) releaseFile() {
	if r == nil || r.track == nil {
		return
	}
	scheduleRemove(r.track, r.ID)
}

func (r *RoomState) cleanupFile() {
	if r == nil {
		return
	}

	tracks := make([]*state.Track, 0, 3)
	if r.track != nil {
		tracks = append(tracks, r.track)
	}
	tracks = append(tracks, r.queue...)
	if len(tracks) > 2 {
		tracks = tracks[:2]
	}

	for _, t := range tracks {
		if t == nil || t.ID == "" {
			continue
		}
		scheduleRemove(t, r.ID)
	}
}

// scheduleRemove deletes the track file after FileCacheDuration,
// but only if no other room is using it at deletion time.
func scheduleRemove(track *state.Track, skipChatID int64) {
	if track == nil {
		return
	}

	if FileCacheDuration <= 0 {
		doRemove(track, skipChatID)
		return
	}

	t := *track
	time.AfterFunc(FileCacheDuration, func() {
		doRemove(&t, skipChatID)
	})

	logger.Debugf(
		"scheduled file removal in %s: %s:%s",
		FileCacheDuration, string(track.Source), track.ID,
	)
}

func doRemove(track *state.Track, skipChatID int64) {
	roomsMu.RLock()
	used := isTrackUsed(track.ID, skipChatID)
	roomsMu.RUnlock()

	if used {
		logger.Debugf(
			"file still in use, skipped remove: %s:%s",
			string(track.Source), track.ID,
		)
		return
	}

	findAndRemove(track)
}

func findAndRemove(track *state.Track) {
	t := "audio"
	if track.Video {
		t = "video"
	}

	files, err := filepath.Glob(filepath.Join("downloads", t+"_"+track.ID+"*"))
	if err != nil {
		return
	}

	for _, f := range files {
		os.Remove(f)
		logger.Debugf("removed file: %s", f)
	}
}

// ───────────── room_control ─────────────

// isCallGoneErr reports whether err indicates the underlying native call no
// longer exists (e.g. the voice chat was ended/discarded on Telegram's side
// but we hadn't yet reconciled our local room state). In that case retrying
// the operation, or trusting Active()/cached flags, will just fail again.
func isCallGoneErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") ||
		strings.Contains(msg, "call not found") ||
		strings.Contains(msg, "no call")
}

const (
	minSpeed         = 0.50
	maxSpeed         = 4.0
	seekEndThreshold = 10
	seekSafetyMargin = 5
)

// Play starts playback of a track
func (r *RoomState) Play(t *state.Track, path string, force ...bool) error {
	if r.Destroyed() {
		return ErrRoomDestroyed
	}

	forcePlay := len(force) > 0 && force[0]

	r.mu.Lock()
	shouldQueue := !forcePlay && r.playing && r.track != nil
	if shouldQueue {
		r.queue = append(r.queue, t)
		r.mu.Unlock()
		return nil
	}

	if r.track != t {
		r.loop = 0
	}
	r.track = t
	r.addPlayedLocked(t)
	r.playing = true
	r.filePath = path
	r.position = 0
	r.paused = false
	r.muted = false
	r.updatedAt = time.Now().Unix()
	r.mu.Unlock()

	err := r.play()
	if err != nil {
		r.mu.Lock()
		r.track = nil
		r.playing = false
		r.filePath = ""
		r.mu.Unlock()
		return err
	}

	return nil
}

// Pause pauses playback with optional auto-resume
func (r *RoomState) Pause() (bool, error) {
	if r.Destroyed() {
		return false, ErrRoomDestroyed
	}

	r.mu.RLock()
	alreadyPaused := r.paused
	r.mu.RUnlock()

	if alreadyPaused {
		return true, nil
	}

	paused, err := r.Assistant.Ntg.Pause(r.ID)
	if err != nil {
		return false, err
	}

	r.mu.RLock()
	isMuted := r.muted
	r.mu.RUnlock()

	if isMuted {
		r.Unmute()
	}

	r.mu.Lock()
	r.updatePosition()
	r.paused = true
	r.muted = false
	r.mu.Unlock()

	return paused, nil
}

// Resume resumes playback
func (r *RoomState) Resume() (bool, error) {
	if r.Destroyed() {
		return false, ErrRoomDestroyed
	}

	if !r.Active() {
		return false, fmt.Errorf("there are no active music playing")
	}

	r.mu.RLock()
	alreadyPlaying := !r.paused
	r.mu.RUnlock()

	if alreadyPlaying {
		return true, nil
	}

	resumed, err := r.Assistant.Ntg.Resume(r.ID)
	if err != nil {
		return false, err
	}

	r.mu.Lock()
	r.paused = false
	r.muted = false
	r.playing = true
	r.updatedAt = time.Now().Unix()
	r.mu.Unlock()

	return resumed, nil
}

// Replay restarts the current track
func (r *RoomState) Replay() error {
	if r.Destroyed() {
		return ErrRoomDestroyed
	}

	r.mu.RLock()
	hasTrack := r.track != nil && r.filePath != ""
	r.mu.RUnlock()

	if !hasTrack {
		return fmt.Errorf("no track to replay")
	}

	r.mu.Lock()
	oldPos := r.position
	r.position = 0
	r.mu.Unlock()

	err := r.play()
	if err != nil {
		r.mu.Lock()
		r.position = oldPos
		r.mu.Unlock()
		return err
	}

	r.mu.Lock()
	r.position = 0
	r.paused = false
	r.muted = false
	r.playing = true
	r.updatedAt = time.Now().Unix()
	r.mu.Unlock()

	return nil
}

// Stop stops playback completely
func (r *RoomState) Stop() error {
	if r.Destroyed() {
		return ErrRoomDestroyed
	}

	_, file, line, _ := runtime.Caller(1)
	logger.Debugf("Stop Called from %s:%d", file, line)

	err := r.Assistant.Ntg.Stop(r.ID)

	r.mu.Lock()
	r.track = nil
	r.position = 0
	r.playing = false
	r.paused = false
	r.muted = false
	r.updatedAt = 0
	r.autoplay = false
	r.mu.Unlock()

	return err
}

// Seek moves playback position by specified seconds
func (r *RoomState) Seek(seconds int) error {
	if r.Destroyed() {
		return ErrRoomDestroyed
	}

	r.mu.Lock()
	if r.track == nil || r.filePath == "" {
		r.mu.Unlock()
		return fmt.Errorf("no track to seek")
	}

	r.updatePosition()

	if seconds > 0 && r.track.Duration-r.position <= seekEndThreshold {
		r.mu.Unlock()
		return fmt.Errorf("cannot seek, track is about to end")
	}

	snapshot := struct {
		position int
		paused   bool
		muted    bool
		updated  int64
	}{
		position: r.position,
		paused:   r.paused,
		muted:    r.muted,
		updated:  r.updatedAt,
	}

	newPos := r.position + seconds
	if newPos >= r.track.Duration {
		newPos = r.track.Duration - seekSafetyMargin
	}
	if newPos < 0 {
		newPos = 0
	}

	r.position = newPos
	r.paused = false
	r.muted = false
	r.updatedAt = time.Now().Unix()
	r.mu.Unlock()

	err := r.play()
	if err != nil {
		r.mu.Lock()
		r.position = snapshot.position
		r.paused = snapshot.paused
		r.muted = snapshot.muted
		r.updatedAt = snapshot.updated
		r.mu.Unlock()

		if isCallGoneErr(err) {
			// The native call is already gone (voice chat ended). Drop the
			// room now instead of leaving it "active" so the next command
			// doesn't keep retrying against a dead call.
			DropRoom(r.ID)
			return fmt.Errorf("the voice chat has ended")
		}
		return err
	}

	r.mu.RLock()
	wasMuted := snapshot.muted
	r.mu.RUnlock()

	if wasMuted {
		r.Assistant.Ntg.Unmute(r.ID)
	}

	return nil
}

// SetSpeed adjusts playback speed
func (r *RoomState) SetSpeed(speed float64) error {
	if r.Destroyed() {
		return ErrRoomDestroyed
	}

	r.mu.RLock()
	hasTrack := r.track != nil && r.filePath != ""
	currentSpeed := r.speed
	r.mu.RUnlock()

	if !hasTrack {
		return fmt.Errorf("no track to adjust speed")
	}

	if speed < minSpeed || speed > maxSpeed {
		return fmt.Errorf(
			"invalid speed: must be between %.2fx and %.1fx",
			minSpeed,
			maxSpeed,
		)
	}

	if currentSpeed == speed {
		return nil
	}

	r.mu.Lock()
	r.updatePosition()
	r.speed = speed
	r.playing = true
	r.paused = false
	r.muted = false
	r.updatedAt = time.Now().Unix()
	r.mu.Unlock()

	err := r.play()
	if err != nil {
		if isCallGoneErr(err) {
			DropRoom(r.ID)
			return fmt.Errorf("the voice chat has ended")
		}
		return err
	}

	return nil
}

// Mute mutes playback
func (r *RoomState) Mute() (bool, error) {
	if r.Destroyed() {
		return false, ErrRoomDestroyed
	}

	r.mu.RLock()
	alreadyMuted := r.muted
	r.mu.RUnlock()

	if alreadyMuted {
		return true, nil
	}

	muted, err := r.Assistant.Ntg.Mute(r.ID)
	if err != nil {
		return false, err
	}

	r.mu.RLock()
	isPaused := r.paused
	r.mu.RUnlock()

	if isPaused {
		r.Resume()
	} else {
		r.Parse()
	}

	r.mu.Lock()
	r.muted = true
	r.mu.Unlock()

	return muted, nil
}

// Unmute unmutes playback
func (r *RoomState) Unmute() (bool, error) {
	if r.Destroyed() {
		return false, ErrRoomDestroyed
	}

	unmuted, err := r.Assistant.Ntg.Unmute(r.ID)
	if err != nil {
		return false, err
	}

	r.mu.Lock()
	r.updatePosition()
	r.muted = false
	r.paused = false
	r.mu.Unlock()

	return unmuted, nil
}

func (r *RoomState) play() error {
	// Snapshot under the lock: Stop/DropRoom can clear the track at any time.
	r.mu.RLock()
	track, path, pos, speed, ass := r.track, r.filePath, r.position, r.speed, r.Assistant
	r.mu.RUnlock()

	if track == nil || ass == nil {
		return errors.New("no track to play")
	}

	desc := getMediaDescription(path, pos, speed, track.Video)
	return ass.Ntg.Play(r.ID, desc)
}

// shellQuoteArg escapes a value placed inside double quotes of the ffmpeg
// shell command, so a crafted URL or file name cannot expand environment
// variables ($TOKEN, $MONGO_DB_URI, ...) or run extra commands.
func shellQuoteArg(s string) string {
	s = strings.NewReplacer("\n", "", "\r", "").Replace(s)
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(s, "\"", "")
	}
	return strings.NewReplacer(
		"\\", "\\\\",
		"\"", "\\\"",
		"$", "\\$",
		"`", "\\`",
	).Replace(s)
}

func getMediaDescription(
	url string,
	pos int,
	speed float64,
	isVideo bool,
) artistcalls.MediaDescription {
	if speed < 0.5 {
		speed = 0.5
	} else if speed > 4.0 {
		speed = 4.0
	}

	baseCmd := "ffmpeg "
	if utils.IsStreamURL(url) {
		baseCmd += "-reconnect 1 -reconnect_streamed 1 -reconnect_delay_max 5 "
	}
	if pos > 0 {
		baseCmd += "-ss " + strconv.Itoa(pos) + " "
	}
	baseCmd += "-v warning -i \"" + shellQuoteArg(url) + "\" "

	audio := getAudioPipeline(baseCmd, speed)
	if !isVideo {
		return artistcalls.MediaDescription{
			Microphone: audio,
		}
	}

	video := getVideoPipeline(baseCmd, url, speed)
	return artistcalls.MediaDescription{
		Microphone: audio,
		Camera:     video,
	}
}

func getAudioPipeline(
	baseCmd string,
	speed float64,
) *artistcalls.AudioDescription {
	audio := &artistcalls.AudioDescription{
		MediaSource:  artistcalls.MediaSourceShell,
		SampleRate:   96000,
		ChannelCount: 2,
	}

	audioCmd := baseCmd
	audioCmd += "-filter:a \"atempo=" + strconv.FormatFloat(
		speed,
		'f',
		2,
		64,
	) + "\" "
	audioCmd += "-f s16le -ac " + strconv.Itoa(int(audio.ChannelCount)) + " "
	audioCmd += "-ar " + strconv.Itoa(int(audio.SampleRate)) + " "
	audioCmd += "pipe:1"
	audio.Input = audioCmd

	return audio
}

func getVideoPipeline(
	baseCmd string,
	url string,
	speed float64,
) *artistcalls.VideoDescription {
	w, h, fps, filter := utils.NormalizeVideo(url, speed)

	video := &artistcalls.VideoDescription{
		MediaSource: artistcalls.MediaSourceShell,
		Width:       int16(w),
		Height:      int16(h),
		Fps:         uint8(fps),
	}

	videoCmd := baseCmd
	videoCmd += "-filter:v \"" + filter + "\" "
	videoCmd += "-f rawvideo -r " + strconv.Itoa(fps) + " -pix_fmt yuv420p "
	videoCmd += "pipe:1"
	video.Input = videoCmd

	return video
}

// ───────────── chat_state ─────────────

// Flood-wait handling while the assistant joins a chat.
const (
	shortFloodWait = 10 // seconds: sleep and retry on the same assistant
	longFloodWait  = 30 // seconds: above this the user is told to wait
)

var (
	ErrAdminPermissionRequired  = errors.New("admin permission required")
	ErrStateFetchFailed         = errors.New("state fetch failed")
	ErrAssistantNotAvailable    = errors.New("assistant unavailable")
	ErrAssistantInviteLinkFetch = errors.New("failed to fetch invite link")
	ErrInviteRequestSent        = errors.New("join request sent")
	ErrJoinFailed               = errors.New("assistant join failed")
)

// FloodWaitError reports a flood wait that could not be worked around, so the
// user has to wait before retrying.
type FloodWaitError struct{ Seconds int }

func (e *FloodWaitError) Error() string {
	return fmt.Sprintf("flood wait: %d seconds", e.Seconds)
}

// StateSnapshot is a cached view of the assistant's membership and the
// voice-chat status in a chat.
type StateSnapshot struct {
	AssistantPresent bool
	AssistantBanned  bool
	VoiceChatActive  *bool
}

// ChatState tracks the per-chat state needed to run the assistant in a voice
// chat: which assistant is bound, whether it has joined, and whether a voice
// chat is active. StateSnapshot is embedded so the snapshot fields are read
// and written directly.
type ChatState struct {
	mu sync.RWMutex

	ChatID int64

	Assistant *voice.Assistant

	StateSnapshot
	inviteLink string
	fetched    bool
}

var (
	chatStatesMu sync.Mutex
	chatStates   = map[int64]*ChatState{}
)

// ChatStateFor returns the cached ChatState for a chat, binding an assistant
// to it on first use.
func ChatStateFor(chatID int64) (*ChatState, error) {
	chatStatesMu.Lock()
	state := chatStates[chatID]
	if state == nil {
		state = &ChatState{ChatID: chatID}
		chatStates[chatID] = state
	}
	chatStatesMu.Unlock()
	if err := state.bindAssistant(); err != nil {
		logger.Errorf("chat_state: bindAssistant failed for %d: %v", chatID, err)
		return nil, err
	}
	return state, nil
}

func DropChatState(chatID int64) {
	chatStatesMu.Lock()
	delete(chatStates, chatID)
	chatStatesMu.Unlock()
}

// FullChat fetches the supergroup full info for a chat ID. TDLib supergroup
// chat IDs carry a -100 prefix, while getSupergroupFullInfo expects the raw
// supergroup id, so no separate getChat lookup is needed.
func FullChat(c *td.Client, chatID int64) (*td.SupergroupFullInfo, error) {
	return c.GetSupergroupFullInfo(-chatID - 1_000_000_000_000)
}

// Snapshot returns the cached state, refreshing it first when none is cached
// yet.
func (s *ChatState) Snapshot() (StateSnapshot, error) {
	if snap, ok := s.cached(); ok {
		return snap, nil
	}
	return s.refresh()
}

// Refresh forces a refresh of the cached state from Telegram.
func (s *ChatState) Refresh() (StateSnapshot, error) {
	return s.refresh()
}

// Join makes sure the assistant is a member of the chat. For public chats the
// resolved username is used as the join target (no admin rights needed); on
// failure it falls back to the real invite-link flow. Short flood waits are
// slept through, longer ones switch to another assistant, and if none can
// join a FloodWaitError is returned so the user can be told to wait.
func (s *ChatState) Join() error {
	if username := s.publicUsername(); username != "" {
		s.setLink("https://t.me/" + username)
	}

	err := s.joinWithRetry()
	if err == nil {
		return nil
	}

	wait := telegram.GetFloodWait(err)
	if wait == 0 {
		// Not a flood error: retry with the real invite link.
		s.setLink("")
		return s.joinWithRetry()
	}
	if wait <= shortFloodWait {
		logger.Errorf("chat_state: flood wait %ds while joining %d, retrying", wait, s.ChatID)
		time.Sleep(time.Duration(wait) * time.Second)
		return s.joinWithRetry()
	}

	// Longer flood waits: join with another assistant instead of blocking.
	if s.switchAssistant() {
		return nil
	}

	if wait < longFloodWait {
		logger.Errorf("chat_state: flood wait %ds while joining %d, retrying", wait, s.ChatID)
		time.Sleep(time.Duration(wait) * time.Second)
		return s.joinWithRetry()
	}

	return &FloodWaitError{Seconds: wait}
}

func (s *ChatState) SetPresent(v bool) {
	s.mu.Lock()
	s.AssistantPresent = v
	s.fetched = true
	s.mu.Unlock()
}

func (s *ChatState) SetBanned(v bool) {
	s.mu.Lock()
	s.AssistantBanned = v
	s.fetched = true
	s.mu.Unlock()
}

func (s *ChatState) SetVoiceChat(v bool) {
	s.mu.Lock()
	s.VoiceChatActive = &v
	s.fetched = true
	s.mu.Unlock()
}

func (s *ChatState) Fetched() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fetched
}

// refresh fetches the assistant membership, voice-chat status and invite link
// from Telegram and stores them as the new snapshot.
func (s *ChatState) refresh() (StateSnapshot, error) {
	logger.Debugf("chat_state: refresh(chat=%d)", s.ChatID)
	if s.Assistant == nil {
		if err := s.bindAssistant(); err != nil {
			return StateSnapshot{}, err
		}
	}
	full, err := FullChat(voice.Bot, s.ChatID)
	if err != nil {
		logger.Errorf("chat_state: FullChat failed for %d: %v", s.ChatID, err)
		if isAdminError(err) {
			return StateSnapshot{}, ErrAdminPermissionRequired
		}
		return StateSnapshot{}, fmt.Errorf("%w: %v", ErrStateFetchFailed, err)
	}

	member, err := voice.Bot.GetChatMember(s.ChatID, &td.MessageSenderUser{UserId: s.Assistant.Self.ID})
	if err != nil {
		if isAdminError(err) {
			logger.Errorf("chat_state: admin permission required for GetChatMember in %d", s.ChatID)
			return StateSnapshot{}, ErrAdminPermissionRequired
		}
		return StateSnapshot{}, fmt.Errorf("%w: %v", ErrStateFetchFailed, err)
	}

	present, banned := membership(member)

	// A bot cannot observe the group call state (getGroupCall is user-only and
	// the group call id is always 0 for bots), so voice chat activity is
	// queried through the assistant like Grogram's fullChannel.Call != nil.
	vcActive := s.VoiceChatActive
	if active, err := s.voiceChatActive(); err != nil {
		logger.Warnf("chat_state: voiceChatActive failed for %d: %v", s.ChatID, err)
	} else {
		vcActive = &active
	}
	s.setState(present, banned, vcActive)
	if full != nil &&
		full.InviteLink != nil &&
		full.InviteLink.InviteLink != "" {
		s.setLink(full.InviteLink.InviteLink)
	}
	return s.current(), nil
}

// voiceChatActive reports whether an active voice/video chat is running in the
// chat. The bot's TDLib client cannot observe this, so it is queried through
// the assistant's MTProto client (getFullChannel -> Call != nil), mirroring
// Grogram's ChannelFull.Call check.
func (s *ChatState) voiceChatActive() (bool, error) {
	if s.Assistant == nil || s.Assistant.Client == nil {
		return false, ErrAssistantNotAvailable
	}

	peer, err := s.Assistant.Client.ResolvePeer(s.ChatID)
	if err != nil {
		return false, fmt.Errorf("resolve peer: %w", err)
	}

	switch p := peer.(type) {
	case *telegram.InputPeerChannel:
		full, err := s.Assistant.Client.ChannelsGetFullChannel(&telegram.InputChannelObj{
			ChannelID:  p.ChannelID,
			AccessHash: p.AccessHash,
		})
		if err != nil {
			return false, fmt.Errorf("get full channel: %w", err)
		}
		ch, ok := full.FullChat.(*telegram.ChannelFull)
		return ok && ch.Call != nil, nil

	case *telegram.InputPeerChat:
		full, err := s.Assistant.Client.MessagesGetFullChat(p.ChatID)
		if err != nil {
			return false, fmt.Errorf("get full chat: %w", err)
		}
		ch, ok := full.FullChat.(*telegram.ChatFullObj)
		return ok && ch.Call != nil, nil
	}

	return false, nil
}

func membership(m *td.ChatMember) (bool, bool) {
	if m == nil {
		return false, false
	}
	switch st := m.Status.(type) {
	case *td.ChatMemberStatusBanned:
		return false, true
	case *td.ChatMemberStatusRestricted:
		if !st.IsMember {
			return false, true
		}
		return true, false
	case *td.ChatMemberStatusMember, *td.ChatMemberStatusAdministrator, *td.ChatMemberStatusCreator:
		return true, false
	}
	return false, false
}

// publicUsername returns the chat's public username, or "" when the chat has
// none (private group/channel). Public chats let the assistant join without
// needing an invite link or admin rights.
func (s *ChatState) publicUsername() string {
	chat, err := voice.Bot.GetChat(s.ChatID)
	if err != nil {
		return ""
	}
	ct, ok := chat.Type.(*td.ChatTypeSupergroup)
	if !ok {
		return ""
	}
	sg, err := voice.Bot.GetSupergroup(ct.SupergroupId)
	if err != nil || sg == nil || sg.Usernames == nil {
		return ""
	}
	if len(sg.Usernames.ActiveUsernames) == 0 {
		return ""
	}
	return sg.Usernames.ActiveUsernames[0]
}

// joinWithRetry joins using the cached invite link, retrying once after
// refreshing the link when it has expired.
func (s *ChatState) joinWithRetry() error {
	err := s.joinByLink()
	if err == nil {
		return nil
	}
	if !telegram.MatchError(err, "INVITE_HASH_EXPIRED") {
		return err
	}
	logger.Errorf("chat_state: invite expired for %d, retrying with refreshed link", s.ChatID)
	s.setLink("")
	return s.joinByLink()
}

func (s *ChatState) joinByLink() error {
	logger.Debugf("chat_state: joinByLink(chat=%d)", s.ChatID)
	link, err := s.resolveLink()
	if err != nil {
		return err
	}
	_, err = s.Assistant.Client.JoinChannel(link)
	if err == nil || telegram.MatchError(err, "USER_ALREADY_PARTICIPANT") {
		s.setState(true, false, s.VoiceChatActive)
		return nil
	}
	if telegram.MatchError(err, "INVITE_REQUEST_SENT") {
		logger.Infof("chat_state: invite request sent for %d, attempting approval", s.ChatID)
		if err := s.approveJoinRequest(); err != nil {
			return ErrInviteRequestSent
		}
		return nil
	}
	if telegram.MatchError(err, "USER_CHANNELS_TOO_MUCH") || telegram.MatchError(err, "CHANNELS_TOO_MUCH") {
		logger.Infof("chat_state: join limit reached for %d, leaving inactive chats", s.ChatID)
		s.leaveInactiveChats(5)
		time.Sleep(1 * time.Second)
		_, retryErr := s.Assistant.Client.JoinChannel(link)
		if retryErr == nil || telegram.MatchError(retryErr, "USER_ALREADY_PARTICIPANT") {
			s.setState(true, false, s.VoiceChatActive)
			return nil
		}
		return retryErr
	}
	if isAdminError(err) {
		return ErrAdminPermissionRequired
	}
	return fmt.Errorf("%w: %v", ErrJoinFailed, err)
}

func (s *ChatState) resolveLink() (string, error) {
	logger.Debugf("chat_state: resolveLink(chat=%d)", s.ChatID)
	s.mu.RLock()
	cached := s.inviteLink
	s.mu.RUnlock()
	if cached != "" {
		return cached, nil
	}
	full, err := FullChat(voice.Bot, s.ChatID)
	if err != nil {
		if isAdminError(err) {
			return "", ErrAdminPermissionRequired
		}
		return "", fmt.Errorf("%w: %v", ErrAssistantInviteLinkFetch, err)
	}
	if full != nil &&
		full.InviteLink != nil &&
		full.InviteLink.InviteLink != "" {
		s.setLink(full.InviteLink.InviteLink)
		return full.InviteLink.InviteLink, nil
	}
	inv, err := voice.Bot.CreateChatInviteLink(s.ChatID, 0, 0, "", nil)
	if err != nil {
		if isAdminError(err) {
			return "", ErrAdminPermissionRequired
		}
		return "", fmt.Errorf("%w: %v", ErrAssistantInviteLinkFetch, err)
	}
	if inv == nil || inv.InviteLink == "" {
		return "", ErrAssistantInviteLinkFetch
	}
	s.setLink(inv.InviteLink)
	return inv.InviteLink, nil
}

func (s *ChatState) approveJoinRequest() error {
	logger.Debugf("chat_state: approveJoinRequest(chat=%d)", s.ChatID)
	err := voice.Bot.ProcessChatJoinRequest(
		s.ChatID,
		s.Assistant.Self.ID,
		&td.ProcessChatJoinRequestOpts{Approve: true},
	)
	if err == nil {
		s.setState(true, false, s.VoiceChatActive)
		return nil
	}
	if isAdminError(err) {
		return ErrAdminPermissionRequired
	}
	return err
}

// switchAssistant tries to join the chat with each other assistant, keeping
// the first one that succeeds. It reports whether the chat was joined.
func (s *ChatState) switchAssistant() bool {
	original := s.Assistant
	if original == nil {
		return false
	}

	for idx := 1; idx <= voice.Assistants.Count(); idx++ {
		if idx == original.Index+1 {
			continue
		}
		ass, err := voice.Assistants.Get(idx)
		if err != nil {
			continue
		}
		s.bind(ass)
		if err := s.joinByLink(); err == nil {
			store.SaveAssistant(s.ChatID, idx)
			s.rebindRoom(ass)
			logger.Infof("chat_state: switched assistant for %d to index %d", s.ChatID, idx)
			return true
		}
	}

	s.bind(original)
	return false
}

// bindAssistant binds the chat to its assistant, caching the choice.
func (s *ChatState) bindAssistant() error {
	s.mu.RLock()
	has := s.Assistant != nil
	s.mu.RUnlock()
	if has {
		return nil
	}
	if voice.Assistants == nil || voice.Assistants.Count() == 0 {
		logger.Errorf("chat_state: no assistants available for %d", s.ChatID)
		return ErrAssistantNotAvailable
	}
	ass, err := voice.Assistants.ForChat(s.ChatID)
	if err != nil {
		logger.Errorf("chat_state: Assistants.ForChat failed for %d: %v", s.ChatID, err)
		return fmt.Errorf("%w: %v", ErrAssistantNotAvailable, err)
	}
	logger.Infof("chat_state: assistant assigned for %d", s.ChatID)
	s.mu.Lock()
	s.Assistant = ass
	s.mu.Unlock()
	return nil
}

// bind switches the chat to a different assistant without touching the cached
// assignment.
func (s *ChatState) bind(ass *voice.Assistant) {
	s.mu.Lock()
	s.Assistant = ass
	s.mu.Unlock()
}

// rebindRoom points the room (if any) at the assistant now serving the chat.
func (s *ChatState) rebindRoom(ass *voice.Assistant) {
	if room, ok := RoomFor(s.ChatID); ok {
		room.SetAssistant(ass)
	}
}

// leaveInactiveChats makes room for joining a new chat by leaving older chats
// that are not currently streaming.
func (s *ChatState) leaveInactiveChats(limit int) {
	logger.Debugf("chat_state: leaveInactiveChats(chat=%d, limit=%d)", s.ChatID, limit)
	if s.Assistant == nil || s.Assistant.Client == nil || limit <= 0 {
		return
	}

	activeRooms := AllRooms()
	leftCount := 0
	err := s.Assistant.Client.IterDialogs(func(d *telegram.TLDialog) error {
		if d == nil || d.IsUser() {
			return nil
		}

		chatID := d.GetChannelID()
		if chatID == 0 || chatID == s.ChatID {
			return nil
		}

		if _, active := activeRooms[chatID]; active {
			return nil
		}

		leaveErr := s.Assistant.Client.LeaveChannel(chatID)
		if leaveErr != nil {
			if wait := telegram.GetFloodWait(leaveErr); wait > 0 {
				time.Sleep(time.Duration(wait) * time.Second)
			}
			return nil
		}

		leftCount++
		if leftCount >= limit {
			return telegram.ErrStopIteration
		}
		return nil
	}, &telegram.DialogOptions{
		Limit: int32(limit * 20),
	})

	if err != nil && err != telegram.ErrStopIteration {
		logger.Warnf("chat_state: IterDialogs failed while auto-leaving chats: %v", err)
	}
}

func (s *ChatState) cached() (StateSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.StateSnapshot, s.fetched
}

func (s *ChatState) current() StateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.StateSnapshot
}

func (s *ChatState) setState(present, banned bool, vcActive *bool) {
	s.mu.Lock()
	s.StateSnapshot = StateSnapshot{
		AssistantPresent: present,
		AssistantBanned:  banned,
		VoiceChatActive:  vcActive,
	}
	s.fetched = true
	s.mu.Unlock()
}

func (s *ChatState) setLink(link string) { s.mu.Lock(); s.inviteLink = link; s.mu.Unlock() }

func isAdminError(err error) bool {
	if telegram.MatchError(err, "CHAT_ID_INVALID") || telegram.MatchError(err, "CHAT_ADMIN_REQUIRED") ||
		telegram.MatchError(err, "CHANNEL_PRIVATE") || telegram.MatchError(err, "CHANNEL_INVALID") {
		return true
	}

	var tde *td.Error
	if errors.As(err, &tde) {
		msg := strings.ToLower(tde.Message)
		return tde.Code == 404 ||
			strings.Contains(msg, "chat not found") ||
			strings.Contains(msg, "chat admin privileges") ||
			strings.Contains(msg, "not enough rights") ||
			strings.Contains(msg, "channel private")
	}
	return false
}
