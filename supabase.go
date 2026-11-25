package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// Cliente mínimo de Supabase (REST + Storage) usando la clave secreta del servidor.
type Supa struct {
	URL, Key string
	http     *http.Client
}

func newSupa() *Supa {
	return &Supa{URL: os.Getenv("SUPABASE_URL"), Key: os.Getenv("SUPABASE_SECRET_KEY"), http: &http.Client{Timeout: 60 * time.Second}}
}

func (s *Supa) do(method, path string, body io.Reader, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest(method, s.URL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", s.Key)
	req.Header.Set("Authorization", "Bearer "+s.Key)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("supabase %s %s: %d %s", method, path, resp.StatusCode, string(b))
	}
	return b, nil
}

type Sticker struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	ImagePath string  `json:"image_path"`
	AudioPath string  `json:"audio_path"`
	WebpPath  *string `json:"webp_path"`
	OggPath   *string `json:"ogg_path"`
	Sends     int     `json:"sends"`
}

type Order struct {
	Code      string  `json:"code"`
	StickerID string  `json:"sticker_id"`
	Status    string  `json:"status"`
	Sticker   Sticker `json:"stickers"`
}

func (s *Supa) GetOrder(code string) (*Order, error) {
	b, err := s.do("GET", "/rest/v1/orders?select=*,stickers(*)&code=eq."+url.QueryEscape(code), nil, nil)
	if err != nil {
		return nil, err
	}
	var out []Order
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

func (s *Supa) Patch(table, filter string, fields map[string]any) error {
	body, _ := json.Marshal(fields)
	_, err := s.do("PATCH", "/rest/v1/"+table+"?"+filter, bytes.NewReader(body),
		map[string]string{"Content-Type": "application/json", "Prefer": "return=minimal"})
	return err
}

func (s *Supa) Download(bucket, path string) ([]byte, error) {
	return s.do("GET", "/storage/v1/object/"+bucket+"/"+path, nil, nil)
}

func (s *Supa) Upload(bucket, path, contentType string, data []byte) error {
	_, err := s.do("POST", "/storage/v1/object/"+bucket+"/"+path, bytes.NewReader(data),
		map[string]string{"Content-Type": contentType, "x-upsert": "true"})
	return err
}
