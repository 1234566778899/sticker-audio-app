package main

import (
	"os"
	"testing"
)

func TestMedia(t *testing.T) {
	for _, c := range []struct {
		in    string
		video bool
	}{{"testdata/img.png", false}, {"testdata/vid.mp4", true}} {
		dir := t.TempDir()
		w, err := makeWebP(c.in, c.video, dir)
		if err != nil {
			t.Fatal(err)
		}
		x, err := addExif(w, map[string]string{"sound-sha256": "abc"})
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(dir+"/../out.webp", x, 0o644)
		t.Logf("%s -> webp %d KB (con exif %d KB)", c.in, len(w)/1024, len(x)/1024)
		limit := 500_000
		if !c.video {
			limit = 100_000
		}
		if len(x) > limit {
			t.Fatalf("pesa demasiado: %d", len(x))
		}
	}
	o, secs, err := makeOgg("testdata/aud.m4a", t.TempDir(), maxAudioStatic)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ogg %d KB, %d s", len(o)/1024, secs)
}
