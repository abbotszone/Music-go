<div align="center">

# 🎧 ArtistMusic

**A fast, lag-free Telegram voice chat music bot.**
Stream audio and video from YouTube, Telegram files, direct links and the Artistbots API.

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white)
![MongoDB](https://img.shields.io/badge/MongoDB-required-47A248?style=flat-square&logo=mongodb&logoColor=white)
![License](https://img.shields.io/badge/License-GPLv3-blue?style=flat-square)

</div>

<br/>

## ✨ Features

| | |
| :-- | :-- |
| 🎵 **Playback** | Audio and video streaming in group voice chats, queue, autoplay |
| 🎛️ **Control** | Pause, resume, skip, stop, seek, loop, replay, mute, unmute |
| 🖼️ **Thumbnail** | Per-chat toggle for album-art display |
| 📣 **Broadcast** | Send or forward a message to every served chat and user |

<br/>

## 🚀 Quick start

You need an **API ID/Hash** ([my.telegram.org](https://my.telegram.org)), a **bot token** ([@BotFather](https://t.me/BotFather)), a **MongoDB URI** and at least one **assistant session string**.

### 🐳 Docker

```bash
cp sample.env .env && nano .env
docker build -t artistmusic .
docker run -d --env-file .env --name artistmusic artistmusic
```

### 🖥️ VPS

```bash
sudo apt-get update && sudo apt-get install -y git curl ffmpeg build-essential
bash scripts/install.sh                 # dependencies + ntgcalls
cp sample.env .env && nano .env         # fill in your values
bash scripts/start.sh                   # build, run, auto-restart on crash
```

Run it as a service:

```bash
sudo bash scripts/setup_service.sh
sudo systemctl start artistmusic
```

> [!IMPORTANT]
> Set **`OWNER_ID`** to your own Telegram user ID. It decides who can use owner-only commands such as `/broadcast` and `/restart`.

<br/>

## ⚙️ Configuration

Copy `sample.env` to `.env`. Variables marked ✅ are required.

| Variable | | Default | Description |
| :-- | :-: | :-- | :-- |
| `API_ID` / `API_HASH` | ✅ | | Telegram API credentials |
| `TOKEN` | ✅ | | Bot token from @BotFather |
| `MONGO_DB_URI` | ✅ | | MongoDB connection string |
| `STRING_SESSIONS` | ✅ | | Assistant session strings, separated by space or comma |
| `OWNER_ID` | ✅ | | Telegram user ID of the owner |
| `LOGGER_ID` | | `0` | Chat ID for logs and error reports (`0` turns it off) |
| `DURATION_LIMIT` | | `4200` | Max track length in seconds |
| `QUEUE_LIMIT` | | `24` | Max tracks per chat queue |
| `MAX_AUTH_USERS` | | `25` | Max authorized users per chat |
| `DEFAULT_LANG` | | `en` | Keep as `en` |
| `LEAVE_ON_DEMOTED` | | `false` | Leave a group when the bot is demoted |
| `DISABLE_COLOUR` | | `false` | Turn off coloured log output |
| `START_IMAGES` | | | Start media URL(s) |
| `PING_IMG_URL` | | | Media shown with `/ping` |
| `SUPPORT_CHAT` / `SUPPORT_CHANNEL` | | `https://t.me/Artistbots` | Support links |
| `COOKIES_LINK` | | | batbin link(s) to YouTube cookies for `yt-dlp` |
| `ARTISTBOTS_API_URL` / `ARTISTBOTS_API_KEY` | | `https://api.artistbotsapi.bond` | Artistbots API (empty key disables it) |
| `PORT` | | `8000` | Health-check HTTP port |

<br/>

## 🎮 Commands

| Group | Commands |
| :-- | :-- |
| **Basic** | `/start` `/help` `/ping` `/queue` `/loop` `/nothumb` `/autoplay` |
| **Play** | `/play` `/playforce` (`/fplay`) `/vplay` `/vplayforce` (`/fvplay`) |
| **Control** | `/stop` (`/end`) `/skip` `/pause` `/resume` `/replay` `/mute` `/unmute` `/seek` `/seekback` |
| **Owner** | `/broadcast` `/reload` `/restart` |

<br/>

## 📁 Project structure

```text
ArtistMusic/
├── cmd/app/              entry point and dependency checks
├── config/               env loader and cookies handling
├── ArtistMusic/
│   ├── core/             rooms, chat state, assistant pool, buttons
│   ├── modules/          command handlers (play, queue, broadcast, settings ...)
│   ├── platforms/        stream resolvers (YouTube, Telegram, APIs, direct links)
│   ├── database/         MongoDB access
│   ├── locales/          texts (en.yml)
│   ├── artist/           Telegram client library (TDLib)
│   └── utils/            helpers
├── ArtistCalls/          voice chat call bindings
├── ArtistUbot/           assistant call handling
├── scripts/              install.sh · build.sh · start.sh · setup_service.sh
├── Dockerfile
└── sample.env
```

<br/>

## 📜 License

Released under the **GNU GPL v3.0**. See [`LICENSE`](./LICENSE).

<div align="center">

Made with ❤️ by [@Artistbots](https://t.me/Artistbots)

</div>
