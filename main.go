// Bot de WhatsApp: recibe un código (STK-XXXX) y responde con el sticker con sonido.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

var (
	codeRe  = regexp.MustCompile(`(?i)\bSTK-[A-Z0-9]{4,8}\b`)
	supa    *Supa
	cli     *whatsmeow.Client
	log     waLog.Logger
	limiter = struct {
		sync.Mutex
		m map[string][]time.Time
	}{m: map[string][]time.Time{}}
	busy sync.Map // códigos en proceso
)

const maxPerHour = 15

func loadEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

func allowed(jid string) bool {
	limiter.Lock()
	defer limiter.Unlock()
	now := time.Now()
	var keep []time.Time
	for _, t := range limiter.m[jid] {
		if now.Sub(t) < time.Hour {
			keep = append(keep, t)
		}
	}
	if len(keep) >= maxPerHour {
		limiter.m[jid] = keep
		return false
	}
	limiter.m[jid] = append(keep, now)
	return true
}

func reply(chat types.JID, text string) {
	cli.SendMessage(context.Background(), chat, &waE2E.Message{Conversation: proto.String(text)})
}

func textOf(m *waE2E.Message) string {
	if t := m.GetConversation(); t != "" {
		return t
	}
	return m.GetExtendedTextMessage().GetText()
}

func handleMessage(evt *events.Message) {
	own := cli.Store.ID
	if own == nil {
		return
	}
	isSelfChat := evt.Info.Chat.User == own.User || (cli.Store.LID.User != "" && evt.Info.Chat.User == cli.Store.LID.User)
	if evt.Info.IsFromMe && !(os.Getenv("ALLOW_SELF") == "true" && isSelfChat) {
		return
	}
	if evt.Info.IsGroup || time.Since(evt.Info.Timestamp) > 10*time.Minute {
		return
	}
	code := strings.ToUpper(codeRe.FindString(textOf(evt.Message)))
	if code == "" {
		return
	}
	if _, loaded := busy.LoadOrStore(code, true); loaded {
		return
	}
	defer busy.Delete(code)
	chat := evt.Info.Chat
	if !allowed(chat.String()) {
		reply(chat, "⏳ Pediste muchos stickers seguidos. Intenta de nuevo en un rato.")
		return
	}
	if err := deliver(code, chat); err != nil {
		log.Errorf("pedido %s: %v", code, err)
		supa.Patch("orders", "code=eq."+code, map[string]any{"status": "failed", "error": err.Error()})
		reply(chat, "😕 No pudimos preparar tu sticker. Vuelve a intentarlo desde la web.")
	}
}

func deliver(code string, chat types.JID) error {
	order, err := supa.GetOrder(code)
	if err != nil {
		return err
	}
	if order == nil {
		reply(chat, "🤔 No encontramos ese código. Revisa que esté bien escrito o crea tu sticker en la web.")
		return nil
	}
	supa.Patch("orders", "code=eq."+code, map[string]any{"status": "processing", "requester_jid": chat.String()})
	reply(chat, "🎨 ¡Preparando tu sticker con sonido!")

	st := order.Sticker
	webp, ogg, secs, animated, err := ensureProcessed(&st)
	if err != nil {
		return err
	}
	if err := sendSoundSticker(chat, webp, ogg, secs, animated); err != nil {
		return err
	}
	supa.Patch("orders", "code=eq."+code, map[string]any{"status": "sent", "sent_at": time.Now().UTC().Format(time.RFC3339)})
	supa.Patch("stickers", "id=eq."+st.ID, map[string]any{"sends": st.Sends + 1})
	reply(chat, "✅ ¡Listo! Toca el 🔊 para escucharlo.\n⭐ Para guardarlo: mantenlo presionado → *Añadir a favoritos*.")
	return nil
}

// ensureProcessed genera (una sola vez) el WebP y el OGG, y los guarda en el bucket público.
// Imagen -> sticker estático + audio ≤10 s. Video -> sticker animado + audio ≤5 s.
// Los archivos van en "<id>/v3/": versiones anteriores (animadas o con metadatos) se regeneran.
func ensureProcessed(st *Sticker) (webp, ogg []byte, secs uint32, animated bool, err error) {
	if st.WebpPath != nil && st.OggPath != nil && strings.HasPrefix(*st.WebpPath, st.ID+"/v3/") {
		w, err1 := supa.Download("stickers", *st.WebpPath)
		o, err2 := supa.Download("stickers", *st.OggPath)
		if err1 == nil && err2 == nil {
			dir, _ := os.MkdirTemp("", "stk")
			defer os.RemoveAll(dir)
			p := filepath.Join(dir, "a.ogg")
			os.WriteFile(p, o, 0o644)
			return w, o, durationSeconds(p), strings.HasSuffix(*st.WebpPath, "anim.webp"), nil
		}
	}
	dir, err := os.MkdirTemp("", "stk")
	if err != nil {
		return
	}
	defer os.RemoveAll(dir)
	img, err := supa.Download("uploads", st.ImagePath)
	if err != nil {
		return
	}
	aud, err := supa.Download("uploads", st.AudioPath)
	if err != nil {
		return
	}
	imgFile := filepath.Join(dir, "in"+filepath.Ext(st.ImagePath))
	audFile := filepath.Join(dir, "in_audio"+filepath.Ext(st.AudioPath))
	os.WriteFile(imgFile, img, 0o644)
	os.WriteFile(audFile, aud, 0o644)
	ct := http.DetectContentType(img)
	animated = strings.HasPrefix(ct, "video/") || strings.Contains(".mp4.mov.webm.m4v", strings.ToLower(filepath.Ext(st.ImagePath)))

	if webp, err = makeWebP(imgFile, animated, dir); err != nil {
		return
	}
	maxSecs := maxAudioStatic
	if animated {
		maxSecs = maxAudioAnimated
	}
	if ogg, secs, err = makeOgg(audFile, dir, maxSecs); err != nil {
		return
	}
	kind := "static"
	if animated {
		kind = "anim"
	}
	wp, op := st.ID+"/v3/sticker-"+kind+".webp", st.ID+"/v3/audio.ogg"
	if err = supa.Upload("stickers", wp, "image/webp", webp); err != nil {
		return
	}
	if err = supa.Upload("stickers", op, "audio/ogg", ogg); err != nil {
		return
	}
	supa.Patch("stickers", "id=eq."+st.ID, map[string]any{"webp_path": wp, "ogg_path": op})
	return webp, ogg, secs, animated, nil
}

func sendSoundSticker(to types.JID, rawWebp, audio []byte, secs uint32, animated bool) error {
	ctx := context.Background()
	sum := sha256.Sum256(audio)
	webp, err := addExif(rawWebp, map[string]string{
		"sticker-pack-id":        "wasap",
		"sticker-pack-name":      "wasap",
		"sticker-pack-publisher": "wasap",
		"sound-sha256":           hex.EncodeToString(sum[:]),
	})
	if err != nil {
		return err
	}
	up, err := cli.Upload(ctx, webp, whatsmeow.MediaImage)
	if err != nil {
		return fmt.Errorf("subiendo sticker: %w", err)
	}
	ua, err := cli.Upload(ctx, audio, whatsmeow.MediaAudio)
	if err != nil {
		return fmt.Errorf("subiendo audio: %w", err)
	}
	now := time.Now()
	sticker := &waE2E.StickerMessage{
		URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
		FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: proto.Uint64(up.FileLength),
		Mimetype: proto.String("image/webp"), Width: proto.Uint32(512), Height: proto.Uint32(512),
		IsAnimated: proto.Bool(animated), IsLottie: proto.Bool(false), IsAvatar: proto.Bool(false), IsAiSticker: proto.Bool(false),
		MediaKeyTimestamp: proto.Int64(now.Unix()), StickerSentTS: proto.Int64(now.UnixMilli()),
		Audio: &waE2E.StickerMessage_AudioMessage{AudioMessage: &waE2E.AudioMessage{
			URL: proto.String(ua.URL), DirectPath: proto.String(ua.DirectPath), MediaKey: ua.MediaKey,
			FileEncSHA256: ua.FileEncSHA256, FileSHA256: ua.FileSHA256, FileLength: proto.Uint64(ua.FileLength),
			Mimetype: proto.String("audio/ogg; codecs=opus"), Seconds: proto.Uint32(secs),
			PTT: proto.Bool(false), MediaKeyTimestamp: proto.Int64(now.Unix()),
		}},
	}
	msg := &waE2E.Message{AudioStickerMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{StickerMessage: sticker}}}
	_, err = cli.SendMessage(ctx, to, msg)
	return err
}

func main() {
	loadEnv(".env")
	ctx := context.Background()
	lvl := os.Getenv("LOG")
	if lvl == "" {
		lvl = "INFO"
	}
	log = waLog.Stdout("BOT", lvl, true)
	supa = newSupa()
	if supa.URL == "" || supa.Key == "" {
		panic("faltan SUPABASE_URL / SUPABASE_SECRET_KEY")
	}
	sessionDB := os.Getenv("SESSION_DB")
	if sessionDB == "" {
		sessionDB = "session.db"
	}
	// crea la carpeta de la sesión (ej. /data en Railway) si no existe
	if dir := filepath.Dir(sessionDB); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			panic(fmt.Sprintf("no se pudo crear %s: %v", dir, err))
		}
	}
	if _, err := os.Stat(filepath.Dir(sessionDB)); err == nil && os.Getenv("RAILWAY_VOLUME_MOUNT_PATH") == "" && os.Getenv("RAILWAY_ENVIRONMENT") != "" {
		log.Warnf("No hay un volumen de Railway: la sesión de WhatsApp se perderá en cada despliegue. Monta un volumen en %s", filepath.Dir(sessionDB))
	}
	db, err := sql.Open("sqlite", "file:"+sessionDB+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		panic(err)
	}
	container := sqlstore.NewWithDB(db, "sqlite3", waLog.Stdout("DB", "WARN", true))
	if err := container.Upgrade(ctx); err != nil {
		panic(err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		panic(err)
	}
	cli = whatsmeow.NewClient(device, waLog.Stdout("WA", "WARN", true))
	startHTTP()
	cli.AddEventHandler(func(e any) {
		switch v := e.(type) {
		case *events.Message:
			go handleMessage(v)
		case *events.Connected:
			log.Infof("Conectado como %s — esperando códigos STK-…", cli.Store.ID)
		case *events.LoggedOut:
			log.Errorf("WhatsApp cerró la sesión; borra %s y vuelve a vincular", sessionDB)
		}
	})

	if cli.Store.ID == nil {
		qrChan, _ := cli.GetQRChannel(ctx)
		if err := cli.Connect(); err != nil {
			panic(err)
		}
		go func() {
			for evt := range qrChan {
				if evt.Event == "code" {
					setQR(evt.Code, "esperando escaneo")
					qrcode.WriteFile(evt.Code, qrcode.Medium, 480, "qr.png")
					if os.Getenv("OPEN_QR") != "false" {
						exec.Command("open", "qr.png").Run()
					}
					log.Infof("QR nuevo disponible en /qr?key=…")
				} else {
					setQR("", evt.Event)
					log.Infof("QR: %s", evt.Event)
				}
			}
		}()
	} else if err := cli.Connect(); err != nil {
		panic(err)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	cli.Disconnect()
}
