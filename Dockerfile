FROM golang:1.26.2-bookworm AS builder

WORKDIR /build

# hadolint ignore=DL3015
RUN apt-get update && \
    apt-get install -y \
        unzip \
        curl \
        zlib1g-dev && \
    rm -rf /var/lib/apt/lists/*

COPY . .
RUN go mod tidy

RUN mkdir -p /build/config/cookies && chmod +x scripts/install.sh

# Not quiet on purpose: if a download fails, the reason shows in the build log.
RUN ./scripts/install.sh -n -t --skip-summary \
    || (echo "=== install.sh failed; log follows ===" && cat /tmp/install_*.log; exit 1)

RUN ls -la /build/libntgcalls* /build/libtdjson* /build/internal/artistcalls/ntgcalls.h

RUN CGO_ENABLED=1 go build -v -trimpath -ldflags="-w -s" -o app .


FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ffmpeg \
        curl \
        unzip \
        ca-certificates \
        libssl3 \
        zlib1g && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /etc/ssl/certs /etc/ssl/certs

RUN curl -fL \
      https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_linux \
      -o /usr/local/bin/yt-dlp && \
    chmod 0755 /usr/local/bin/yt-dlp && \
    curl -fsSL https://deno.land/install.sh -o /tmp/deno-install.sh && \
    DENO_INSTALL=/usr/local sh /tmp/deno-install.sh -y --no-modify-path && \
    chmod 0755 /usr/local/bin/deno && \
    rm -f /tmp/deno-install.sh

ENV LD_LIBRARY_PATH=/app:$LD_LIBRARY_PATH
# Workaround for "fatal error: traceback did not unwind completely" (GC stack
# scan crash while native TDLib/ntgcalls code is running): stop the Go runtime
# from sending async-preemption signals into native frames.
ENV GODEBUG=asyncpreemptoff=1
ENV GOGC=100
ENV TDJSON_PATH=/app/libtdjson.so.1.8.66

RUN useradd -r -u 10001 appuser && \
    mkdir -p /app && \
    chown -R appuser:appuser /app

WORKDIR /app

COPY --from=builder /build/app /app/app
COPY --from=builder /build/libtdjson.so* /app/
COPY --from=builder /build/config/cookies /app/config/cookies
RUN chmod 0755 /app/libtdjson.so* && chown -R appuser:appuser /app

USER appuser

ENTRYPOINT ["/app/app"]