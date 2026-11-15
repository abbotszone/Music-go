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

package main

/*
#cgo CFLAGS: -I../../
#cgo linux LDFLAGS: -L ../../ -lntgcalls -lm -lz
#cgo darwin LDFLAGS: -L ../../ -lntgcalls -lc++ -lz -lbz2 -liconv -framework AVFoundation -framework AudioToolbox -framework CoreAudio -framework QuartzCore -framework CoreMedia -framework VideoToolbox -framework AppKit -framework Metal -framework MetalKit -framework OpenGL -framework IOSurface -framework ScreenCaptureKit

// Currently is supported only dynamically linked library on Windows due to
// https://github.com/golang/go/issues/63903
#cgo windows LDFLAGS: -L../../ -lntgcalls
#include "ArtistCalls/ntgcalls.h"
#if defined(__linux__)
#include "glibc_compatibility.h"
#endif
*/
import "C"

import (
	"io"
	"net/http"
	"os"
	"time"

	"ArtistMusic/ArtistMusic/core"
	"ArtistMusic/ArtistMusic/database"
	"ArtistMusic/ArtistMusic/locales"
	"ArtistMusic/ArtistMusic/logger"
	"ArtistMusic/ArtistMusic/modules"
	"ArtistMusic/config"
)

func main() {
	f, err := os.OpenFile("logs.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		logger.Fatalf("failed to open logs.txt: %v", err)
	}
	defer f.Close()

	logger.SetOutput(io.MultiWriter(os.Stderr, f))

	if err := config.Load(); err != nil {
		logger.Fatalf("failed to load config: %v", err)
	}

	if err := checkDependencies(); err != nil {
		logger.Fatalf("dependency check failed: %v", err)
	}

	if err := refreshDirs(); err != nil {
		logger.Fatalf("failed to refresh directories: %v", err)
	}

	logger.Debug("Initializing MongoDB...")

	closeDB, err := database.Init(config.MongoURI)
	if err != nil {
		logger.Fatalf("failed to initialize database: %v", err)
	}
	defer closeDB()

	logger.Info("Database connected successfully")

	if err := locales.Load(); err != nil {
		logger.Fatalf("failed to load locales: %v", err)
	}

	logger.Debug("Initializing clients...")

	shutdownCore, err := core.Init()
	if err != nil {
		logger.Fatalf("failed to initialize core: %v", err)
	}
	defer shutdownCore()

	core.F = modules.F

	if err := database.InitAssistantIndexes(core.Assistants.Count()); err != nil {
		logger.Fatalf("failed to init assistants: %v", err)
	}

	modules.Init(core.Bot, core.Assistants)

	startHTTPServer()

	core.Bot.Idle()
}

func startHTTPServer() {
	// "/" and "/health" answer 200 so host health checks and uptime
	// pingers see a healthy app (the default mux returned 404 for "/").
	mux := http.NewServeMux()
	healthy := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
	mux.HandleFunc("/", healthy)
	mux.HandleFunc("/health", healthy)

	go func() {
		addr := "0.0.0.0:" + config.Port

		logger.Infof("HTTP server listening on %s", addr)

		if err := http.ListenAndServe(addr, mux); err != nil {
			logger.Errorf("HTTP server failed: %v", err)
		}
	}()

	go keepAlive()
}

// keepAlive pings the app's own public URL every few minutes so free hosts
// (Render, Koyeb, ...) do not spin the instance down for inactivity, which
// looks like the bot "restarting". Set KEEP_ALIVE_URL, or run on Render where
// RENDER_EXTERNAL_URL is provided automatically.
func keepAlive() {
	defer func() { _ = recover() }()

	url := os.Getenv("KEEP_ALIVE_URL")
	if url == "" {
		url = os.Getenv("RENDER_EXTERNAL_URL")
	}
	if url == "" {
		return
	}

	logger.Infof("Keep-alive pings enabled for %s", url)

	client := &http.Client{Timeout: 15 * time.Second}
	ticker := time.NewTicker(4 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		resp, err := client.Get(url)
		if err != nil {
			logger.Warnf("keep-alive ping failed: %v", err)
			continue
		}
		_ = resp.Body.Close()
	}
}

func refreshDirs() error {
	dirs := []string{
		"./cache",
		"./downloads",
	}

	for _, dir := range dirs {

		if err := os.RemoveAll(dir); err != nil {
			return err
		}

		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	return nil
}
