package main

// Envío con la cuenta del propio usuario ("Enviármelo con mi WhatsApp").
//
// En vez de que el número del bot mande el sticker, el usuario vincula SU WhatsApp
// por unos segundos (código de 8 letras en el celular o QR en la computadora),
// el bot se envía el sticker a su propio chat ("Tú") y cierra la sesión enseguida:
// el dispositivo desaparece de "Dispositivos vinculados" y no queda nada guardado.
//
// Reglas de privacidad:
//   - no se descarga el historial de chats (ManualHistorySyncDownload) ni se leen mensajes;
//   - la sesión vive en una base SQLite temporal que se borra al arrancar y al terminar;
//   - máximo linkTimeout por sesión; al terminar siempre Logout + borrar el dispositivo.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

const (
	linkTimeout   = 3 * time.Minute
	maxLinks      = 15 // sesiones temporales a la vez
	linkKeepAfter = 10 * time.Minute
)

// Estados que ve la web: waiting (mostrar código/QR) → linked → sending → sent | failed | expired
type linkSession struct {
	ID        string    `json:"id"`
	Code      string    `json:"-"` // STK-…
	Mode      string    `json:"mode"`
	Status    string    `json:"status"`
	PairCode  string    `json:"pairCode,omitempty"`
	QR        string    `json:"qr,omitempty"` // data:image/png;base64,…
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"-"`
	cancel    context.CancelFunc
}

var (
	linkStore *sqlstore.Container
	links     = struct {
		sync.Mutex
		m      map[string]*linkSession
		byCode map[string]string
	}{m: map[string]*linkSession{}, byCode: map[string]string{}}
	stkRe = regexp.MustCompile(`^STK-[A-Z0-9]{4,8}$`)
)

// initLinks prepara la base temporal de las sesiones (se borra en cada arranque).
func initLinks(ctx context.Context) error {
	store.SetOSInfo("wasap", [3]uint32{1, 0, 0})
	store.DeviceProps.PlatformType = waCompanionReg.DeviceProps_CHROME.Enum()
	store.DeviceProps.RequireFullSync = proto.Bool(false)
	path := filepath.Join(os.TempDir(), "tuki-links.db")
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		os.Remove(p)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return err
	}
	linkStore = sqlstore.NewWithDB(db, "sqlite3", waLog.Stdout("LINKDB", "WARN", true))
	return linkStore.Upgrade(ctx)
}

func (s *linkSession) set(f func(*linkSession)) {
	links.Lock()
	f(s)
	links.Unlock()
}

func newLinkID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func qrDataURL(code string) string {
	png, err := qrcode.Encode(code, qrcode.Medium, 320)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}

// startLink crea la sesión temporal y arranca la vinculación en segundo plano.
func startLink(code, mode, phone, lang string) (*linkSession, error) {
	order, err := supa.GetOrder(code)
	if err != nil {
		return nil, err
	}
	if order == nil {
		return nil, errNotFound
	}
	links.Lock()
	if id, ok := links.byCode[code]; ok {
		if s := links.m[id]; s != nil && (s.Status == "waiting" || s.Status == "linked" || s.Status == "sending") {
			links.Unlock()
			return nil, errBusy
		}
	}
	active := 0
	for _, s := range links.m {
		if s.Status == "waiting" || s.Status == "linked" || s.Status == "sending" {
			active++
		}
	}
	if active >= maxLinks {
		links.Unlock()
		return nil, errFull
	}
	s := &linkSession{ID: newLinkID(), Code: code, Mode: mode, Status: "waiting", CreatedAt: time.Now()}
	links.m[s.ID] = s
	links.byCode[code] = s.ID
	links.Unlock()

	ready := make(chan error, 1)
	go runLink(s, order, phone, lang, ready)
	// esperamos el primer código/QR para devolverlo de inmediato
	select {
	case err := <-ready:
		if err != nil {
			return nil, err
		}
	case <-time.After(20 * time.Second):
		return nil, errors.New("timeout preparando la vinculación")
	}
	return s, nil
}

var (
	errNotFound = errors.New("not_found")
	errBusy     = errors.New("busy")
	errFull     = errors.New("full")
)

func runLink(s *linkSession, order *Order, phone, lang string, ready chan<- error) {
	ctx, cancel := context.WithTimeout(context.Background(), linkTimeout)
	defer cancel()
	s.set(func(s *linkSession) { s.cancel = cancel }) // DELETE /link/{id} ("Generar otro código")
	device := linkStore.NewDevice()
	c := whatsmeow.NewClient(device, waLog.Stdout("LINK", "WARN", true))
	c.ManualHistorySyncDownload = true // nunca bajamos el historial de chats del usuario
	c.EnableAutoReconnect = true

	connected := make(chan struct{}, 1)
	loggedOut := make(chan struct{}, 1)
	c.AddEventHandler(func(e any) {
		switch v := e.(type) {
		case *events.PairSuccess:
			s.set(func(s *linkSession) { s.Status, s.PairCode, s.QR = "linked", "", "" })
			log.Infof("link %s: vinculado", s.Code)
		case *events.PairError:
			log.Warnf("link %s: error de vinculación: %v", s.Code, v.Error)
		case *events.Connected:
			if c.Store.ID != nil {
				select {
				case connected <- struct{}{}:
				default:
				}
			}
		case *events.LoggedOut:
			select {
			case loggedOut <- struct{}{}:
			default:
			}
		}
	})

	finish := func(status, msg string) {
		s.set(func(s *linkSession) {
			s.Status, s.PairCode, s.QR = status, "", ""
			if msg != "" {
				s.Error = msg
			}
		})
	}
	// pase lo que pase: cerrar sesión en el teléfono del usuario y borrar todo
	defer func() {
		lctx, lcancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer lcancel()
		if c.Store.ID != nil {
			if err := c.Logout(lctx); err != nil {
				log.Warnf("link %s: logout: %v", s.Code, err)
			}
		}
		c.Disconnect()
		if err := linkStore.DeleteDevice(lctx, device); err != nil && c.Store.ID != nil {
			log.Warnf("link %s: borrar dispositivo: %v", s.Code, err)
		}
	}()

	qrChan, err := c.GetQRChannel(ctx)
	if err != nil {
		ready <- err
		finish("failed", "qr")
		return
	}
	if err := c.Connect(); err != nil {
		ready <- err
		finish("failed", "connect")
		return
	}

	sentReady := false
	signalReady := func(err error) {
		if !sentReady {
			sentReady = true
			ready <- err
		}
	}

	for {
		select {
		case <-ctx.Done():
			signalReady(errors.New("timeout"))
			links.Lock()
			cancelled := s.Status == "cancelled"
			links.Unlock()
			if !cancelled {
				finish("expired", "")
			}
			return
		case <-loggedOut:
			signalReady(errors.New("logged out"))
			finish("failed", "logged_out")
			return
		case evt, ok := <-qrChan:
			if !ok {
				qrChan = nil // canal cerrado: ya se vinculó o se acabaron los códigos
				continue
			}
			switch evt.Event {
			case "code":
				if s.Mode == "phone" {
					if s.PairCode == "" {
						pc, err := c.PairPhone(ctx, phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
						if err != nil {
							signalReady(err)
							finish("failed", "pair_phone")
							return
						}
						s.set(func(s *linkSession) { s.PairCode = pc })
						signalReady(nil)
					}
				} else {
					s.set(func(s *linkSession) { s.QR = qrDataURL(evt.Code) })
					signalReady(nil)
				}
			case "success":
				// esperamos *events.Connected
			case "timeout":
				signalReady(errors.New("timeout"))
				finish("expired", "")
				return
			default:
				if evt.Error != nil {
					signalReady(evt.Error)
					finish("failed", evt.Event)
					return
				}
			}
		case <-connected:
			s.set(func(s *linkSession) { s.Status = "sending" })
			if err := sendToSelf(ctx, c, order, lang); err != nil {
				log.Errorf("link %s: %v", s.Code, err)
				supa.Patch("orders", "code=eq."+s.Code, map[string]any{"status": "failed", "error": err.Error()})
				finish("failed", "send")
				return
			}
			finish("sent", "")
			log.Infof("link %s: sticker enviado y sesión cerrada", s.Code)
			return
		}
	}
}

// sendToSelf manda el sticker (y un texto corto) al chat propio del usuario vinculado.
func sendToSelf(ctx context.Context, c *whatsmeow.Client, order *Order, lang string) error {
	supa.Patch("orders", "code=eq."+order.Code, map[string]any{"status": "processing", "requester_jid": "self"}) // no guardamos el número del usuario
	st := order.Sticker
	webp, ogg, secs, animated, err := ensureProcessed(&st)
	if err != nil {
		return err
	}
	// pequeña pausa: recién vinculado, WhatsApp aún reparte las llaves del dispositivo nuevo
	time.Sleep(2 * time.Second)
	self := c.Store.ID.ToNonAD()
	if err := sendSoundSticker(c, self, webp, ogg, secs, animated); err != nil {
		return err
	}
	m := msgs[lang]
	if m == nil {
		m = msgs["es"]
	}
	c.SendMessage(ctx, self, &waE2E.Message{Conversation: proto.String(m["selfDone"])})
	supa.Patch("orders", "code=eq."+order.Code, map[string]any{"status": "sent", "sent_at": time.Now().UTC().Format(time.RFC3339)})
	supa.Patch("stickers", "id=eq."+st.ID, map[string]any{"sends": st.Sends + 1})
	time.Sleep(3 * time.Second) // que termine de salir antes de cerrar la sesión
	return nil
}

// limpieza de sesiones viejas en memoria
func gcLinks() {
	for range time.Tick(time.Minute) {
		links.Lock()
		for id, s := range links.m {
			if time.Since(s.CreatedAt) > linkKeepAfter {
				delete(links.m, id)
				if links.byCode[s.Code] == id {
					delete(links.byCode, s.Code)
				}
			}
		}
		links.Unlock()
	}
}

// API para la web (servidor a servidor, con LINK_API_KEY):
//
//	POST /link            {code, mode: "phone"|"qr", phone?, lang?} -> linkSession
//	GET  /link/{id}       -> linkSession
func registerLinkRoutes(mux *http.ServeMux) {
	key := os.Getenv("LINK_API_KEY")
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if key == "" || subtle.ConstantTimeCompare([]byte(got), []byte(key)) != 1 {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return false
		}
		return true
	}
	writeJSON := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /link", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body struct{ Code, Mode, Phone, Lang string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]string{"error": "bad_request"})
			return
		}
		body.Code = strings.ToUpper(strings.TrimSpace(body.Code))
		phone := notDigits.ReplaceAllString(body.Phone, "")
		if !stkRe.MatchString(body.Code) || (body.Mode != "phone" && body.Mode != "qr") || (body.Mode == "phone" && (len(phone) < 8 || len(phone) > 15 || phone[0] == '0')) {
			writeJSON(w, 400, map[string]string{"error": "bad_request"})
			return
		}
		if body.Lang != "en" {
			body.Lang = "es"
		}
		s, err := startLink(body.Code, body.Mode, phone, body.Lang)
		switch {
		case errors.Is(err, errNotFound):
			writeJSON(w, 404, map[string]string{"error": "not_found"})
		case errors.Is(err, errBusy):
			writeJSON(w, 409, map[string]string{"error": "busy"})
		case errors.Is(err, errFull):
			writeJSON(w, 503, map[string]string{"error": "full"})
		case err != nil:
			log.Warnf("link %s: %v", body.Code, err)
			writeJSON(w, 502, map[string]string{"error": "link_failed"})
		default:
			links.Lock()
			out := *s
			links.Unlock()
			writeJSON(w, 200, out)
		}
	})
	mux.HandleFunc("DELETE /link/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if !cancelLink(r.PathValue("id")) {
			writeJSON(w, 409, map[string]string{"error": "not_cancellable"})
			return
		}
		writeJSON(w, 200, map[string]string{"status": "cancelled"})
	})
	mux.HandleFunc("GET /link/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		links.Lock()
		s, ok := links.m[r.PathValue("id")]
		var out linkSession
		if ok {
			out = *s
		}
		links.Unlock()
		if !ok {
			writeJSON(w, 404, map[string]string{"error": "not_found"})
			return
		}
		writeJSON(w, 200, out)
	})
}

var notDigits = regexp.MustCompile(`[^0-9]`)

// cancelLink corta una vinculación que todavía no se completó (para pedir un código nuevo).
// Si ya se vinculó y está enviando, no se corta.
func cancelLink(id string) bool {
	links.Lock()
	defer links.Unlock()
	s, ok := links.m[id]
	if !ok || s.Status != "waiting" {
		return false
	}
	s.Status, s.PairCode, s.QR = "cancelled", "", ""
	if s.cancel != nil {
		s.cancel()
	}
	return true
}
