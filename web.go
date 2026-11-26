package main

import (
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"os"
	"sync"

	"github.com/skip2/go-qrcode"
)

// Estado del QR de vinculación, compartido con la página /qr.
var qrState = struct {
	sync.RWMutex
	code   string
	status string
}{status: "iniciando"}

func setQR(code, status string) {
	qrState.Lock()
	qrState.code, qrState.status = code, status
	qrState.Unlock()
}

// startHTTP expone:
//
//	GET /health          -> 200 "ok" (healthcheck de Railway)
//	GET /qr?key=QR_KEY   -> página con el QR para vincular el número del bot
//
// La página del QR exige QR_KEY: quien escanee ese QR conecta SU WhatsApp al bot.
func startHTTP() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	key := os.Getenv("QR_KEY")
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	registerLinkRoutes(mux)
	mux.HandleFunc("/qr", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if key == "" || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("key")), []byte(key)) != 1 {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}
		qrState.RLock()
		code, status := qrState.code, qrState.status
		qrState.RUnlock()

		body := ""
		switch {
		case cli != nil && cli.Store.ID != nil:
			body = fmt.Sprintf(`<h1>Vinculado ✓</h1><p>El bot está conectado como <b>%s</b>.</p>`, html.EscapeString(cli.Store.ID.User))
		case code != "":
			png, _ := qrcode.Encode(code, qrcode.Medium, 360)
			body = `<h1>Vincula el número del bot</h1>
<p>WhatsApp → Dispositivos vinculados → Vincular un dispositivo.<br>Si el teléfono pide escanear otra vez, usa el QR nuevo (se actualiza solo).</p>
<img alt="QR" width="360" height="360" src="data:image/png;base64,` + base64.StdEncoding.EncodeToString(png) + `">`
		default:
			body = `<h1>Preparando…</h1><p>Estado: ` + html.EscapeString(status) + `</p>`
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<meta http-equiv="refresh" content="5"><title>wasap · bot</title>
<body style="font-family:system-ui;max-width:440px;margin:40px auto;padding:0 16px;text-align:center;color:#111">%s</body>`, body)
	})
	go func() {
		if err := http.ListenAndServe(":"+port, mux); err != nil {
			log.Errorf("servidor HTTP: %v", err)
		}
	}()
}
