package agent

import (
	"bytes"
	"image"
	_ "image/gif" // register decoders for DecodeConfig/Decode
	"image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp" // read_file/TUI accept these; providers don't — convert to JPEG
	"golang.org/x/image/draw"
	_ "golang.org/x/image/tiff" // same
	_ "golang.org/x/image/webp" // decode-only in x/image, but decode→JPEG covers it
)

// Image attachments enter the system from many paths — TUI clipboard paste,
// web composer data URLs, IM channel photos, read_file / browser-screenshot
// tool results — and every one of them funnels through NewImageBlock. Raw
// camera/retina captures (5–15 MB PNGs) blow past provider limits (Anthropic
// rejects images over 5 MB) and cost a fortune in vision tokens, so
// NewImageBlock normalizes here, once, for every caller: downscale to the
// provider-optimal edge and re-encode as JPEG. Anything already small enough
// passes through byte-identical.
const (
	// imageCompressMaxEdge matches the composer's canvas cap and Anthropic's
	// recommended upper bound (their API downscales beyond ~1568 anyway, so a
	// larger source buys nothing but tokens).
	imageCompressMaxEdge = 1568
	// imageCompressMinBytes: images below this size AND within the edge cap
	// are sent untouched — re-encoding a small screenshot only adds JPEG
	// artifacts to sharp text.
	imageCompressMinBytes = 1 << 20 // 1 MB
	// imageCompressMaxPixels guards against decompression bombs: a tiny PNG
	// can decode to gigabytes of pixels. Anything larger sails through as-is
	// (the provider will reject it, loudly, which beats an OOM here).
	imageCompressMaxPixels = 50_000_000
	imageJPEGQuality       = 80
)

// compressImageData normalizes an image for provider consumption. It returns
// the input unchanged when the image is already small enough, when its format
// must not be re-encoded (GIF animation, SVG), when it can't be decoded, or
// when re-encoding would make it bigger. The returned MIME reflects the data
// actually returned.
func compressImageData(mime string, data []byte) (string, []byte) {
	// Animated GIFs are destroyed by a single-frame JPEG pass, and SVGs are
	// vectors — re-encoding either is a loss, so exempt them up front.
	if mime == "image/gif" || mime == "image/svg+xml" {
		return mime, data
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return mime, data // not a decodable raster (or corrupt): pass through
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > imageCompressMaxPixels {
		return mime, data
	}
	longEdge := max(cfg.Width, cfg.Height)
	if len(data) <= imageCompressMinBytes && longEdge <= imageCompressMaxEdge {
		return mime, data
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return mime, data
	}
	dst := src
	if longEdge > imageCompressMaxEdge {
		scale := float64(imageCompressMaxEdge) / float64(longEdge)
		w := max(1, int(float64(cfg.Width)*scale+0.5))
		h := max(1, int(float64(cfg.Height)*scale+0.5))
		scaled := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(scaled, scaled.Bounds(), src, src.Bounds(), draw.Over, nil)
		dst = scaled
	}
	// JPEG has no alpha channel: flatten onto white or transparent regions
	// (screenshot PNGs) come out black.
	flat := image.NewRGBA(dst.Bounds())
	draw.Draw(flat, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(flat, dst.Bounds(), dst, dst.Bounds().Min, draw.Over)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flat, &jpeg.Options{Quality: imageJPEGQuality}); err != nil {
		return mime, data
	}
	// A pathological input (already-optimal small JPEG at max edge) can grow
	// under re-encoding; keep whichever is smaller.
	if buf.Len() >= len(data) {
		return mime, data
	}
	return "image/jpeg", buf.Bytes()
}
