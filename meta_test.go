package main

import (
	"bytes"
	"os"
	"testing"
)

// El OGG no debe arrastrar metadatos del archivo original (ej. MP3 de CapCut).
func TestOggSinMetadatos(t *testing.T) {
	src := os.ExpandEnv("$HOME/Downloads/goku-black-laughing-english0.mp3")
	if _, err := os.Stat(src); err != nil {
		t.Skip("sin archivo de prueba")
	}
	o, secs, err := makeOgg(src, t.TempDir(), maxAudioStatic)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(o, []byte("quicktime")) || bytes.Contains(o, []byte("vicut")) || bytes.Contains(o, []byte("major_brand")) {
		t.Fatal("el OGG todavía tiene metadatos del original")
	}
	t.Logf("ogg limpio: %d KB, %d s", len(o)/1024, secs)
}
