# Fixes applied

Security
- OWNER_ID no longer defaults to a hardcoded Telegram ID (it is now required).
- ffmpeg command: URLs / paths are shell-escaped, so `$ENV_VAR` in a link can no
  longer leak secrets (MONGO_DB_URI, tokens) or run commands.
- yt-dlp: removed `--no-check-certificate`.

Crashes / hangs
- store: bot state shared between handlers is now guarded by a mutex
  (concurrent map write could kill the process); served users/chats use O(1) checks.
- store: chat-settings read-modify-write is serialised.
- voice: connect wait channels are buffered + non-blocking sends (native callback
  thread could hang forever after a join timeout).
- voice: unchecked type assertions replaced by checked ones (no more panics).
- voice: failed group-call connect now cleans up the native call.
- queue: play() snapshots state under lock and nil-checks the track.
- bot/play: nil deref when the "now playing" message could not be sent.

Behaviour
- bot/play: terminal errors (voice timeout, RTMP chat, closed voice chat,
  GROUPCALL_INVALID) now stop the flow; the error message is no longer deleted
  and overwritten by "now playing".
- bot/play: duplicate check ignores empty URL/ID; /play query works with newline;
  FLOOD_WAIT parsing handles trailing text.
- bot/call: next-track download timeout 60s -> 5min; a failed download skips to
  the next queued track instead of dropping the whole queue.
- bot/start: /restart no longer aborts if one chat cannot be messaged.
- error reports sent to LOGGER_ID are HTML-escaped (they silently failed before);
  temp file is kept until TDLib has uploaded it.
- cookies: bad downloads are deleted, non-batbin links supported.
- Pyrogram v1 (263-byte) session strings are now accepted.
- .env.example: DURATION_LIMIT fixed (4200), OWNER_ID blank.

Not compiled here (no Go toolchain) - run `go build` / `go vet ./...` once.
