# wasap — backend (bot de WhatsApp)

Bot en **Go + [whatsmeow](https://github.com/tulir/whatsmeow)** que recibe un código `STK-XXXXX` por WhatsApp
y responde con el **sticker con sonido** creado en la web ([sticker-audio-web](https://github.com/1234566778899/sticker-audio-web)).

## Cómo funciona
1. Busca el pedido en Supabase (`orders` + `stickers`).
2. Si es la primera vez, genera el WebP animado 512×512 (`ffmpeg` + `img2webp`) y el OGG Opus (≤10 s) y los guarda en el bucket `stickers`.
3. Envía `audioStickerMessage` → `StickerMessage.audioMessage` con el EXIF `sound-sha256` del audio.

## Requisitos
- Go 1.27+, `ffmpeg`, `img2webp` (`brew install ffmpeg webp`)
- Proyecto de Supabase con `supabase/001_init.sql` y `supabase/002_grants.sql` aplicados

## Correr en local
```bash
cp .env.example .env        # completa SUPABASE_URL y SUPABASE_SECRET_KEY
go mod vendor && sh patch-whatsmeow.sh
go build -mod=vendor -o stickerbot . && ./stickerbot
```
La primera vez muestra un QR (`qr.png`): escanéalo desde WhatsApp → Dispositivos vinculados.

## Railway
1. Usa el `Dockerfile` (detectado automáticamente).
2. Agrega un **volumen montado en `/data`** (ahí vive `session.db`; sin él se pierde la vinculación en cada deploy).
3. Variables: `SUPABASE_URL`, `SUPABASE_SECRET_KEY` y `QR_KEY` (una clave larga inventada por ti).
4. Healthcheck path: `/health`.
5. Abre `https://<tu-servicio>.up.railway.app/qr?key=<QR_KEY>` y escanea el QR con el WhatsApp **del número del bot**.

> `patch-whatsmeow.sh` agrega a whatsmeow la clasificación del envoltorio `AudioStickerMessage` como sticker.

## ⚠️ Aviso
Cliente **no oficial** de WhatsApp: usa un número dedicado, puede ser bloqueado.
