<!--
==========================================================
Copyright (c) 2026 ArtistBots
All Rights Reserved.

Project      : ArtistBots API Telegram Music Bot
Powered By   : Artist
Type         : API Based Telegram Music Bot

Bot          : @ArtistApibot
Channel      : https://t.me/artistbots
GitHub       : https://github.com/elevenyts/ArtistMusic

Unauthorized copying, modification, or redistribution
of this source code without permission is prohibited.
==========================================================
-->

# ArtistMusic (Go)

Telegram music bot in Go — Artistbots style. `KEEP_ALIVE_URL` hata diya gaya hai.

## Structure
- `main.go` – startup (config → cookies → mongo → bot)
- `config/` – `.env` loader (sab keys `.env.example` jaisi)
- `platforms/` – registry + download race (Artistbots API, yt-dlp+cookies, yt-dlp; jo pehle file de wahi jeetta hai)
- `queue/` – per-chat queue, loop, autoplay state
- `store/` – MongoDB (chat settings, broadcast targets)
- `voice/` – `Engine` interface (VC ka sirf yahi hissa ntgcalls se judta hai)
- `bot/` – commands, inline buttons, help menu, broadcast

## Commands
/start /help /ping /reload · /play /vplay /playforce · /pause /resume /skip /stop /mute /unmute /queue /loop /seek /seekback · /autoplay /nothumb · /broadcast /bcast /gcast

## Run
```bash
cp .env.example .env   # values bharo
go mod tidy
go run .
```
Zaroori: `yt-dlp` aur `ffmpeg` PATH me.

## Abhi kya baaki hai (honest note)
1. `voice/engine.go` me abhi `LogEngine` hai (sirf log karta hai). Asli audio ke liye `voice.Engine` ka ntgcalls wala implementation likh ke `main.go` me `LogEngine` ki jagah lagao. `STRING_SESSIONS` wala userbot call join karega.
2. Code yahan compile nahi kiya ja saka (Go toolchain / internet nahi tha). gogram ke saare calls `bot/tg.go` me hain, version mismatch ho to sirf wahi file theek karni padegi.
3. `-pin / -pinloud` flags parse hote hain par pin karna abhi implement nahi hai.
4. Thumbnail (`/nothumb`) ka flag save hota hai, par now-playing card abhi sirf text bhejta hai (artwork bhejna baaki).
