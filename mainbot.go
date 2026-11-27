package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// Bot principal (número propio que responde a "STK-…"), encendido y apagado desde /admin de la web
// sin redeploy. El interruptor se guarda en la tabla `settings` (clave bot_enabled, la escribe la web);
// el bot publica su estado en bot_state ({connected, phone}) para que la web muestre "Recibir al instante"
// solo cuando de verdad está conectado.
var mainBot = struct {
	sync.Mutex
	container *sqlstore.Container
	cancel    context.CancelFunc
	enabled   bool
}{}

func sessionPath() string {
	p := os.Getenv("SESSION_DB")
	if p == "" {
		p = "session.db"
	}
	return p
}

func openContainer(ctx context.Context) (*sqlstore.Container, error) {
	if mainBot.container != nil {
		return mainBot.container, nil
	}
	p := sessionPath()
	if dir := filepath.Dir(p); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", "file:"+p+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	c := sqlstore.NewWithDB(db, "sqlite3", waLog.Stdout("DB", "WARN", true))
	if err := c.Upgrade(ctx); err != nil {
		return nil, err
	}
	mainBot.container = c
	return c, nil
}

// startMainBot conecta el número del bot; si no hay sesión, genera QRs (se ven en el admin y en /qr).
func startMainBot() error {
	mainBot.Lock()
	defer mainBot.Unlock()
	mainBot.enabled = true
	if cli != nil {
		return nil // ya está encendido
	}
	ctx, cancel := context.WithCancel(context.Background())
	c, err := openContainer(ctx)
	if err != nil {
		cancel()
		return err
	}
	device, err := c.GetFirstDevice(ctx)
	if err != nil {
		cancel()
		return err
	}
	client := whatsmeow.NewClient(device, waLog.Stdout("WA", "WARN", true))
	client.AddEventHandler(func(e any) {
		switch v := e.(type) {
		case *events.Message:
			go handleMessage(v)
		case *events.Connected:
			log.Infof("Conectado como %s — esperando códigos STK-…", client.Store.ID)
			setQR("", "conectado")
			publishState(true, client)
		case *events.Disconnected:
			publishState(false, client)
		case *events.LoggedOut:
			// cerrado desde el teléfono (o WhatsApp lo desvinculó): se borra la sesión y, si sigue activado,
			// vuelve a mostrar un QR para vincular de nuevo
			log.Errorf("WhatsApp cerró la sesión del bot (%v)", v.Reason)
			go func() {
				client.Store.Delete(context.Background())
				restartMainBot()
			}()
		}
	})
	cli = client
	mainBot.cancel = cancel
	if client.Store.ID == nil {
		setQR("", "generando QR")
		go pairWithQR(ctx, client)
	} else {
		setQR("", "conectando")
		go func() {
			if err := client.Connect(); err != nil {
				log.Errorf("conectando el bot: %v", err)
				setQR("", "error al conectar")
			}
		}()
	}
	return nil
}

// stopMainBot desconecta el bot sin cerrar la sesión (al encenderlo otra vez no pide QR).
func stopMainBot() {
	mainBot.Lock()
	defer mainBot.Unlock()
	mainBot.enabled = false
	stopLocked()
	setQR("", "apagado")
}

func stopLocked() {
	if mainBot.cancel != nil {
		mainBot.cancel()
		mainBot.cancel = nil
	}
	if cli != nil {
		c := cli
		cli = nil
		c.Disconnect()
		publishState(false, nil)
	}
}

func restartMainBot() {
	mainBot.Lock()
	enabled := mainBot.enabled
	stopLocked()
	mainBot.Unlock()
	if enabled {
		if err := startMainBot(); err != nil {
			log.Errorf("reiniciando el bot: %v", err)
		}
	} else {
		setQR("", "apagado")
	}
}

// unlinkMainBot cierra la sesión en WhatsApp (el teléfono deja de verlo en "Dispositivos vinculados").
func unlinkMainBot() error {
	mainBot.Lock()
	c := cli
	mainBot.Unlock()
	if c != nil && c.Store.ID != nil {
		if err := c.Logout(context.Background()); err != nil {
			// sin conexión no se puede avisar a WhatsApp: al menos se borra la sesión local
			c.Store.Delete(context.Background())
		}
	}
	restartMainBot()
	return nil
}

// pairWithQR muestra QRs hasta que se escanee uno; si caducan sin escanear, vuelve a empezar.
func pairWithQR(ctx context.Context, c *whatsmeow.Client) {
	for c.Store.ID == nil && ctx.Err() == nil {
		qrChan, err := c.GetQRChannel(ctx)
		if err != nil {
			log.Errorf("QR: %v", err)
			setQR("", "error al generar el QR")
			return
		}
		if err := c.Connect(); err != nil {
			log.Errorf("conectando para el QR: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
			continue
		}
		for evt := range qrChan {
			if ctx.Err() != nil {
				return
			}
			if evt.Event == "code" {
				setQR(evt.Code, "esperando escaneo")
				if os.Getenv("OPEN_QR") != "false" {
					qrcode.WriteFile(evt.Code, qrcode.Medium, 480, "qr.png")
					exec.Command("open", "qr.png").Run()
				}
				log.Infof("QR nuevo disponible")
			} else if evt.Event == "success" {
				setQR("", "vinculado")
			} else {
				setQR("", evt.Event)
				log.Infof("QR: %s", evt.Event)
			}
		}
		if c.Store.ID == nil && ctx.Err() == nil {
			c.Disconnect()
			time.Sleep(3 * time.Second)
		}
	}
}

// ---------- ajustes en Supabase ----------

func getSetting(key string, out any) (bool, error) {
	b, err := supa.do("GET", "/rest/v1/settings?select=value&key=eq."+url.QueryEscape(key), nil, nil)
	if err != nil {
		return false, err
	}
	var rows []struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(b, &rows); err != nil || len(rows) == 0 {
		return false, err
	}
	return true, json.Unmarshal(rows[0].Value, out)
}

func putSetting(key string, value any) error {
	body, _ := json.Marshal(map[string]any{"key": key, "value": value, "updated_at": time.Now().UTC().Format(time.RFC3339)})
	_, err := supa.do("POST", "/rest/v1/settings?on_conflict=key", bytes.NewReader(body),
		map[string]string{"Content-Type": "application/json", "Prefer": "resolution=merge-duplicates,return=minimal"})
	return err
}

func publishState(connected bool, c *whatsmeow.Client) {
	phone := ""
	if c != nil && c.Store.ID != nil {
		phone = c.Store.ID.User
	}
	if err := putSetting("bot_state", map[string]any{"connected": connected, "phone": phone}); err != nil {
		log.Warnf("guardando bot_state: %v", err)
	}
}

// mainBotWanted: el interruptor del admin (bot_enabled); si nunca se tocó, manda MAIN_BOT.
func mainBotWanted() bool {
	var v struct {
		Enabled bool `json:"enabled"`
	}
	found, err := getSetting("bot_enabled", &v)
	if err != nil {
		log.Warnf("leyendo bot_enabled: %v (se usa MAIN_BOT)", err)
	}
	if found {
		return v.Enabled
	}
	return os.Getenv("MAIN_BOT") != "false"
}

// ---------- API para el admin de la web (Authorization: Bearer LINK_API_KEY) ----------

type botStatus struct {
	Enabled bool   `json:"enabled"`
	Status  string `json:"status"` // off | qr | connecting | connected
	Detail  string `json:"detail,omitempty"`
	Phone   string `json:"phone,omitempty"`
	QR      string `json:"qr,omitempty"` // data URL PNG
}

func currentBotStatus() botStatus {
	mainBot.Lock()
	enabled, c := mainBot.enabled, cli
	mainBot.Unlock()
	qrState.RLock()
	code, detail := qrState.code, qrState.status
	qrState.RUnlock()
	s := botStatus{Enabled: enabled, Detail: detail}
	switch {
	case c == nil:
		s.Status = "off"
	case c.Store.ID != nil && c.IsLoggedIn():
		s.Status, s.Phone = "connected", c.Store.ID.User
	case code != "":
		png, _ := qrcode.Encode(code, qrcode.Medium, 512)
		s.Status, s.QR = "qr", "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png)
	default:
		s.Status = "connecting"
		if c.Store.ID != nil {
			s.Phone = c.Store.ID.User
		}
	}
	return s
}

func registerBotRoutes(mux *http.ServeMux) {
	key := os.Getenv("LINK_API_KEY")
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if key == "" || subtle.ConstantTimeCompare([]byte(got), []byte(key)) != 1 {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return false
		}
		return true
	}
	write := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /bot", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			write(w, 200, currentBotStatus())
		}
	})
	// POST /bot/deliver {"code": "STK-…", "phone": "52…" (opcional), "lang": "es"|"en"}
	// Reenvía un pedido que quedó sin entregar al chat que lo pidió (requester_jid) o al número indicado.
	// Solo para quien escribió primero al bot; el admin lo usa a mano.
	mux.HandleFunc("POST /bot/deliver", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body struct{ Code, Phone, Lang string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			write(w, 400, map[string]string{"error": "bad_request"})
			return
		}
		mainBot.Lock()
		c := cli
		mainBot.Unlock()
		if c == nil || !c.IsLoggedIn() {
			write(w, 409, map[string]string{"error": "bot_offline"})
			return
		}
		code := strings.ToUpper(strings.TrimSpace(body.Code))
		if !codeRe.MatchString(code) {
			write(w, 400, map[string]string{"error": "bad_code"})
			return
		}
		var chat types.JID
		if phone := strings.TrimPrefix(strings.TrimSpace(body.Phone), "+"); phone != "" {
			res, err := c.IsOnWhatsApp(r.Context(), []string{"+" + phone})
			if err != nil || len(res) == 0 || !res[0].IsIn {
				write(w, 404, map[string]string{"error": "not_on_whatsapp"})
				return
			}
			chat = res[0].JID
		} else {
			order, err := supa.GetOrder(code)
			if err != nil || order == nil {
				write(w, 404, map[string]string{"error": "order_not_found"})
				return
			}
			var jidStr string
			if b, err := supa.do("GET", "/rest/v1/orders?select=requester_jid&code=eq."+url.QueryEscape(code), nil, nil); err == nil {
				var rows []struct {
					J *string `json:"requester_jid"`
				}
				if json.Unmarshal(b, &rows) == nil && len(rows) > 0 && rows[0].J != nil {
					jidStr = *rows[0].J
				}
			}
			if jidStr == "" || jidStr == "self" {
				write(w, 400, map[string]string{"error": "no_chat"})
				return
			}
			if chat, err = types.ParseJID(jidStr); err != nil {
				write(w, 400, map[string]string{"error": "bad_jid"})
				return
			}
		}
		if _, loaded := busy.LoadOrStore(code, true); loaded {
			write(w, 409, map[string]string{"error": "busy"})
			return
		}
		defer busy.Delete(code)
		lang := body.Lang
		if msgs[lang] == nil {
			lang = "es"
		}
		if err := deliver(code, chat, msgs[lang], false); err != nil {
			supa.Patch("orders", "code=eq."+code, map[string]any{"status": "failed", "error": err.Error()})
			write(w, 500, map[string]string{"error": err.Error()})
			return
		}
		log.Infof("pedido %s reenviado a %s", code, chat)
		write(w, 200, map[string]string{"status": "sent", "chat": chat.String()})
	})
	// POST /bot {"action": "enable" | "disable" | "unlink"}
	mux.HandleFunc("POST /bot", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body struct{ Action string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			write(w, 400, map[string]string{"error": "bad_request"})
			return
		}
		var err error
		switch body.Action {
		case "enable":
			if err = putSetting("bot_enabled", map[string]bool{"enabled": true}); err == nil {
				err = startMainBot()
			}
		case "disable":
			if err = putSetting("bot_enabled", map[string]bool{"enabled": false}); err == nil {
				stopMainBot()
			}
		case "unlink":
			err = unlinkMainBot()
		default:
			write(w, 400, map[string]string{"error": "bad_action"})
			return
		}
		if err != nil {
			log.Errorf("bot %s: %v", body.Action, err)
			write(w, 500, map[string]string{"error": fmt.Sprint(err)})
			return
		}
		write(w, 200, currentBotStatus())
	})
}
