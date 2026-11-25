#!/bin/sh
# whatsmeow aún no clasifica el envoltorio AudioStickerMessage (campo 134) como sticker al enviar.
# Este parche lo añade en vendor/ (ejecutar después de `go mod vendor`).
set -e
F=vendor/go.mau.fi/whatsmeow/send.go
grep -q "AudioStickerMessage" "$F" && exit 0
perl -0pi -e 's/(\tcase msg.LottieStickerMessage != nil:\n\t\treturn getTypeFromMessage\(msg.LottieStickerMessage.Message\)\n)/$1\tcase msg.AudioStickerMessage != nil:\n\t\treturn getTypeFromMessage(msg.AudioStickerMessage.Message)\n/; s/(\tcase msg.LottieStickerMessage != nil:\n\t\treturn getMediaTypeFromMessage\(msg.LottieStickerMessage.Message\)\n)/$1\tcase msg.AudioStickerMessage != nil:\n\t\treturn getMediaTypeFromMessage(msg.AudioStickerMessage.Message)\n/' "$F"
grep -c "AudioStickerMessage" "$F"
