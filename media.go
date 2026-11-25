package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// makeWebP convierte cualquier imagen o video en un sticker WebP animado 512x512 (<500 KB).
// Imagen fija -> animación de "rebote"; video -> hasta 6 s a 12 fps.
func makeWebP(input string, isVideo bool, workdir string) ([]byte, error) {
	frames := filepath.Join(workdir, "f")
	os.MkdirAll(frames, 0o755)
	var args []string
	fit := "scale=512:512:force_original_aspect_ratio=decrease,pad=512:512:(ow-iw)/2:(oh-ih)/2:color=0x00000000"
	if isVideo {
		args = []string{"-y", "-loglevel", "error", "-i", input, "-t", "6", "-vf", "fps=12," + fit + ",format=rgba", filepath.Join(frames, "%03d.png")}
	} else {
		bounce := "format=rgba,scale=440:440:force_original_aspect_ratio=decrease," +
			"scale=w='trunc(iw*(1+0.07*sin(2*PI*n/16))/2)*2':h=-2:eval=frame," +
			"pad=512:512:(ow-iw)/2:(oh-ih)/2:color=0x00000000"
		args = []string{"-y", "-loglevel", "error", "-loop", "1", "-i", input, "-frames:v", "16", "-vf", bounce, filepath.Join(frames, "%03d.png")}
	}
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg frames: %v %s", err, out)
	}
	list, _ := filepath.Glob(filepath.Join(frames, "*.png"))
	if len(list) == 0 {
		return nil, fmt.Errorf("no se generaron cuadros")
	}
	delay := "60"
	if isVideo {
		delay = "83"
	}
	out := filepath.Join(workdir, "sticker.webp")
	for _, q := range []string{"70", "50", "35", "20"} { // baja calidad hasta que pese < 500 KB
		a := []string{"-loop", "0", "-lossy", "-q", q, "-m", "4", "-d", delay}
		a = append(a, list...)
		a = append(a, "-o", out)
		if o, err := exec.Command("img2webp", a...).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("img2webp: %v %s", err, o)
		}
		if st, _ := os.Stat(out); st != nil && st.Size() < 490_000 {
			break
		}
	}
	return os.ReadFile(out)
}

// makeOgg recorta a 10 s y convierte a OGG Opus mono 48 kHz (formato de nota de voz de WhatsApp).
func makeOgg(input, workdir string) ([]byte, uint32, error) {
	out := filepath.Join(workdir, "audio.ogg")
	if o, err := exec.Command("ffmpeg", "-y", "-loglevel", "error", "-i", input, "-vn", "-t", "10",
		"-af", "loudnorm=I=-16:TP=-1.5", "-c:a", "libopus", "-b:a", "48k", "-ac", "1", "-ar", "48000", out).CombinedOutput(); err != nil {
		return nil, 0, fmt.Errorf("ffmpeg audio: %v %s", err, o)
	}
	b, err := os.ReadFile(out)
	return b, durationSeconds(out), err
}

func durationSeconds(path string) uint32 {
	o, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		return 5
	}
	f, _ := strconv.ParseFloat(strings.TrimSpace(string(o)), 64)
	if f < 1 {
		return 1
	}
	return uint32(f + 0.5)
}

// ---------- EXIF del sticker (incluye "sound-sha256") ----------

func addExif(webp []byte, meta map[string]string) ([]byte, error) {
	if len(webp) < 12 || string(webp[0:4]) != "RIFF" || string(webp[8:12]) != "WEBP" {
		return nil, fmt.Errorf("no es un WebP")
	}
	type chunk struct {
		cc   string
		data []byte
	}
	var chunks []chunk
	for o := 12; o+8 <= len(webp); {
		cc := string(webp[o : o+4])
		size := int(binary.LittleEndian.Uint32(webp[o+4:]))
		if o+8+size > len(webp) {
			return nil, fmt.Errorf("WebP corrupto")
		}
		if cc != "EXIF" {
			chunks = append(chunks, chunk{cc, append([]byte(nil), webp[o+8:o+8+size]...)})
		}
		o += 8 + size + size%2
	}
	if len(chunks) == 0 || chunks[0].cc != "VP8X" {
		return nil, fmt.Errorf("el WebP no tiene cabecera VP8X")
	}
	chunks[0].data[0] |= 0x20
	payload, _ := json.Marshal(meta)
	exif := make([]byte, 26+len(payload))
	copy(exif, "II")
	binary.LittleEndian.PutUint16(exif[2:], 0x2a)
	binary.LittleEndian.PutUint32(exif[4:], 8)
	binary.LittleEndian.PutUint16(exif[8:], 1)
	binary.LittleEndian.PutUint16(exif[10:], 0x5741)
	binary.LittleEndian.PutUint16(exif[12:], 7)
	binary.LittleEndian.PutUint32(exif[14:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(exif[18:], 26)
	copy(exif[26:], payload)
	chunks = append(chunks, chunk{"EXIF", exif})

	var body []byte
	for _, c := range chunks {
		h := make([]byte, 8)
		copy(h, c.cc)
		binary.LittleEndian.PutUint32(h[4:], uint32(len(c.data)))
		body = append(body, h...)
		body = append(body, c.data...)
		if len(c.data)%2 == 1 {
			body = append(body, 0)
		}
	}
	out := make([]byte, 12, 12+len(body))
	copy(out, "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(4+len(body)))
	copy(out[8:], "WEBP")
	return append(out, body...), nil
}
