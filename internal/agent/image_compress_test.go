package agent

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"golang.org/x/image/bmp"
)

// encodePNG renders a w×h PNG of deterministic pseudo-random pixels. Noise
// defeats PNG's own compression (a smooth gradient would make the PNG smaller
// than any JPEG and trip the keep-whichever-is-smaller rule), mimicking a
// photographic capture where JPEG re-encoding wins.
func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	rng := uint32(0x12345678)
	next := func() uint8 {
		rng ^= rng << 13
		rng ^= rng >> 17
		rng ^= rng << 5
		return uint8(rng >> 24)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: next(), G: next(), B: next(), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// A large capture (retina screenshot class) is downscaled to the edge cap and
// re-encoded as JPEG — smaller, within provider limits, and marked as JPEG.
func TestCompressImageData_LargePNGIsDownscaled(t *testing.T) {
	data := encodePNG(t, 3000, 2000)
	mime, out := compressImageData("image/png", data)

	if mime != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", mime)
	}
	if len(out) >= len(data) {
		t.Errorf("compressed %d >= original %d", len(out), len(data))
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("DecodeConfig(compressed): %v", err)
	}
	if format != "jpeg" {
		t.Errorf("format = %q, want jpeg", format)
	}
	if cfg.Width > imageCompressMaxEdge || cfg.Height > imageCompressMaxEdge {
		t.Errorf("dims %dx%d exceed cap %d", cfg.Width, cfg.Height, imageCompressMaxEdge)
	}
	// Aspect ratio preserved: 3000×2000 → 1568×~1045.
	if cfg.Width != imageCompressMaxEdge {
		t.Errorf("width = %d, want %d (long edge capped)", cfg.Width, imageCompressMaxEdge)
	}
}

// A small image within both thresholds passes through byte-identical —
// re-encoding a small screenshot only adds JPEG artifacts to sharp text.
func TestCompressImageData_SmallUntouched(t *testing.T) {
	data := encodePNG(t, 320, 200)
	mime, out := compressImageData("image/png", data)
	if mime != "image/png" || !bytes.Equal(out, data) {
		t.Errorf("small image should pass through unchanged, got mime=%q bytes-changed=%v", mime, !bytes.Equal(out, data))
	}
}

// GIFs keep their animation — a single-frame JPEG pass would destroy it.
func TestCompressImageData_GIFUntouched(t *testing.T) {
	garbage := []byte("GIF89a-not-really-a-gif-but-mime-says-so")
	mime, out := compressImageData("image/gif", garbage)
	if mime != "image/gif" || !bytes.Equal(out, garbage) {
		t.Errorf("GIF should pass through unchanged, got mime=%q", mime)
	}
}

// Corrupt bytes (or an undecodable format) pass through — never error, never
// eat the attachment.
func TestCompressImageData_UndecodableUntouched(t *testing.T) {
	raw := []byte{0x89, 'P', 'N', 'G', 1, 2, 3}
	mime, out := compressImageData("image/png", raw)
	if mime != "image/png" || !bytes.Equal(out, raw) {
		t.Errorf("undecodable bytes should pass through unchanged, got mime=%q", mime)
	}
}

// A decompression bomb (tiny file, absurd dimensions) is refused the decode
// pass and returned as-is — the provider's rejection beats an OOM here.
func TestCompressImageData_PixelBombUntouched(t *testing.T) {
	// Hand-craft a PNG header claiming 40000×40000 (1.6 GP) — DecodeConfig
	// succeeds on the header alone, the pixel guard must kick in before any
	// full decode is attempted.
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	// IHDR: length 13, "IHDR", width, height, bitdepth 8, colour 2, rest 0.
	ihdr := []byte{0, 0, 0, 13, 'I', 'H', 'D', 'R', 0, 0, 0x9C, 0x40, 0, 0, 0x9C, 0x40, 8, 2, 0, 0, 0, 0, 0, 0, 0}
	buf.Write(ihdr)
	data := buf.Bytes()
	mime, out := compressImageData("image/png", data)
	if mime != "image/png" || !bytes.Equal(out, data) {
		t.Errorf("pixel bomb should pass through unchanged, got mime=%q", mime)
	}
}

// NewImageBlock wires the normalization: a big image comes out as a JPEG
// block, a small one keeps its bytes.
func TestNewImageBlock_Normalizes(t *testing.T) {
	big := NewImageBlock("image/png", encodePNG(t, 2400, 1600))
	if big.Image.MIMEType != "image/jpeg" {
		t.Errorf("big block mime = %q, want image/jpeg", big.Image.MIMEType)
	}
	small := NewImageBlock("image/png", encodePNG(t, 64, 64))
	if small.Image.MIMEType != "image/png" {
		t.Errorf("small block mime = %q, want unchanged image/png", small.Image.MIMEType)
	}
}

// A source whose JPEG re-encode would be BIGGER (a tiny, solid-colour PNG
// past the edge cap — PNG crushes flat colour, JPEG can't) is kept as-is:
// the keep-whichever-is-smaller rule. This one actually reaches the branch —
// the edge cap is what pushes it past the early pass-through.
func TestCompressImageData_KeepsSmaller(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	// zero value is opaque black, uniformly — PNG paradise, JPEG nightmare.
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	data := buf.Bytes()
	if len(data) > imageCompressMinBytes {
		t.Fatalf("fixture too big (%d) to be a solid-colour PNG — test premise broken", len(data))
	}
	mime, out := compressImageData("image/png", data)
	if mime != "image/png" || !bytes.Equal(out, data) {
		t.Errorf("flat PNG past the edge cap should keep its (smaller) original bytes, got mime=%q changed=%v", mime, !bytes.Equal(out, data))
	}
}

// Transparent regions must flatten to WHITE, not black, when re-encoded to
// JPEG (which has no alpha channel) — a transparent-screenshot regression.
func TestCompressImageData_AlphaFlattensToWhite(t *testing.T) {
	// Noise (so JPEG wins the size comparison and compression actually
	// fires), with a big fully-transparent corner.
	img := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	rng := uint32(99)
	next := func() uint8 {
		rng ^= rng << 13
		rng ^= rng >> 17
		rng ^= rng << 5
		return uint8(rng >> 24)
	}
	for y := 0; y < 1000; y++ {
		for x := 0; x < 2000; x++ {
			if x < 500 && y < 500 {
				img.Set(x, y, color.RGBA{0, 0, 0, 0}) // transparent corner
			} else {
				img.Set(x, y, color.RGBA{next(), next(), next(), 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	mime, out := compressImageData("image/png", buf.Bytes())
	if mime != "image/jpeg" {
		t.Fatalf("mime = %q, want image/jpeg (compression should have fired)", mime)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("jpeg.Decode: %v", err)
	}
	r, g, b, _ := decoded.At(50, 50).RGBA() // well inside the transparent corner
	const minWhite = 0xF000                 // ≈240/255, tolerating JPEG artifacts
	if r < minWhite || g < minWhite || b < minWhite {
		t.Errorf("transparent corner = (%x,%x,%x), want ≈white (flattened), not black", r, g, b)
	}
}

// BMP (accepted by read_file/TUI, rejected by providers) is decoded via
// x/image/bmp and re-encoded to a provider-safe JPEG.
func TestCompressImageData_BMPConverted(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	rng := uint32(7)
	next := func() uint8 {
		rng ^= rng << 13
		rng ^= rng >> 17
		rng ^= rng << 5
		return uint8(rng >> 24)
	}
	for y := 0; y < 1000; y++ {
		for x := 0; x < 2000; x++ {
			img.Set(x, y, color.RGBA{next(), next(), next(), 255})
		}
	}
	var buf bytes.Buffer
	if err := bmp.Encode(&buf, img); err != nil {
		t.Fatalf("bmp.Encode: %v", err)
	}
	mime, out := compressImageData("image/bmp", buf.Bytes())
	if mime != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", mime)
	}
	if len(out) >= buf.Len() {
		t.Errorf("compressed %d >= original %d", len(out), buf.Len())
	}
}
