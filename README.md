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
Usa el `Dockerfile`. Agrega un **volumen montado en `/data`** (ahí vive `session.db`) y las variables
`SUPABASE_URL`, `SUPABASE_SECRET_KEY`. El QR sale en los logs como texto (`QR: ...`).

> `patch-whatsmeow.sh` agrega a whatsmeow la clasificación del envoltorio `AudioStickerMessage` como sticker.

## ⚠️ Aviso
Cliente **no oficial** de WhatsApp: usa un número dedicado, puede ser bloqueado.
