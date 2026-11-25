# Bot de WhatsApp para Railway (Go + whatsmeow + ffmpeg + img2webp)
FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go mod vendor && sh patch-whatsmeow.sh && CGO_ENABLED=0 go build -mod=vendor -o /stickerbot .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg webp ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /app
RUN mkdir -p /data
COPY --from=build /stickerbot /app/stickerbot
# Monta un volumen de Railway en /data para no perder la sesión de WhatsApp
ENV SESSION_DB=/data/session.db OPEN_QR=false ALLOW_SELF=false
CMD ["/app/stickerbot"]
