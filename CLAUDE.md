# Tuki — bot de WhatsApp (Go + whatsmeow)

Responder en español. Repo **público**: nunca versionar `.env`, `session.db` ni claves.
Despliega en **Railway** (servicio `sticker-audio-app`, Dockerfile) con cada push a `main`.

## Archivos
- `main.go` — conexión whatsmeow, recibe `STK-XXXXX` (regex), límite 15 pedidos/hora por chat, ignora mensajes de >10 min y grupos, procesa y envía. `ensureProcessed` cachea en bucket `stickers` bajo `<id>/v3/` (subir la versión si cambia el procesamiento para regenerar).
- `media.go` — `makeWebP` (imagen → **estático** con `cwebp`, <100 KB; video → **animado** con ffmpeg + `img2webp`, ≤6 s, <500 KB), `makeOgg` (Opus mono 48k, `loudnorm`, **`-map_metadata -1`**, recorte 10 s imagen / 5 s video), `addExif` (JSON con `sound-sha256`, crea VP8X si falta).
- `supabase.go` — cliente REST/Storage mínimo con la clave secreta.
- `web.go` — HTTP en `$PORT`: `/health` y `/qr?key=QR_KEY` (QR de vinculación, se refresca solo).
- `patch-whatsmeow.sh` — tras `go mod vendor`, hace que `send.go` trate `AudioStickerMessage` como sticker (mediatype). Obligatorio; el Dockerfile lo ejecuta.

## Idioma de las respuestas
`msgs` (es/en) en `main.go`; `langOf` detecta inglés si el mensaje trae "want my sticker" o empieza con "hi"/"hello" (el texto lo arma la web según el idioma).

## Mensaje enviado
`Message{AudioStickerMessage: FutureProofMessage{Message: {StickerMessage{…, IsAnimated, Audio: StickerMessage_AudioMessage{AudioMessage{mimetype "audio/ogg; codecs=opus", Seconds, PTT:false}}}}}}`

## Variables
`SUPABASE_URL`, `SUPABASE_SECRET_KEY`, `QR_KEY`, `ALLOW_SELF` (true mientras el bot use el número personal), `SESSION_DB` (Docker: `/data/session.db`, volumen), `OPEN_QR=false` en servidor, `LOG`, `DELETE_CHATS` (true = borra el chat en la cuenta del bot 8 s después de entregar; nunca el chat propio. Solo usar con número dedicado).

## Local
```bash
cp .env.example .env
go mod vendor && sh patch-whatsmeow.sh
go build -mod=vendor -o stickerbot . && ./stickerbot      # requiere ffmpeg y webp (brew)
go test -mod=vendor ./...
```
No correr el bot local y el de Railway con el mismo número a la vez (responderían dos veces).

## Lecciones
- Baileys no sirve para vincular (2026); whatsmeow maneja `companion_reg_refresh`.
- Audio con metadatos (MP3 de CapCut) → sticker mudo. Audio >5 s en sticker animado → mudo.
- Mucho intento fallido de vinculación → WhatsApp bloquea temporalmente "vincular nuevos dispositivos".
- Los stickers con sonido respetan el modo silencio del teléfono.
