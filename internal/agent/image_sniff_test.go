package agent

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// TestSniffImageType covers the formats a provider will and won't accept.
// bmp/tiff/heic/svg are the interesting rejects: read_file's extension table
// admits all four, and an MCP server can name any of them, so before sniffing
// they reached the provider verbatim and failed the entire request.
func TestSniffImageType(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"png", []byte("\x89PNG\r\n\x1a\n" + "IHDR..."), "image/png"},
		{"jpeg", []byte("\xff\xd8\xff\xe0" + "JFIF"), "image/jpeg"},
		{"gif87a", []byte("GIF87a" + "rest"), "image/gif"},
		{"gif89a", []byte("GIF89a" + "rest"), "image/gif"},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), "image/webp"},

		{"bmp rejected", []byte("BM\x00\x00\x00\x00\x00\x00"), ""},
		{"tiff little-endian rejected", []byte("II*\x00\x08\x00\x00\x00"), ""},
		{"tiff big-endian rejected", []byte("MM\x00*\x00\x00\x00\x08"), ""},
		{"heic rejected", []byte("\x00\x00\x00\x18ftypheic"), ""},
		{"svg rejected", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), ""},
		{"ico rejected", []byte("\x00\x00\x01\x00\x01\x00\x20\x20"), ""},

		{"empty", nil, ""},
		{"truncated png signature", []byte{0x89, 'P', 'N', 'G'}, ""},
		{"riff that is not webp", []byte("RIFF\x00\x00\x00\x00AVI LIST"), ""},
		{"plain text", []byte("this is not an image at all"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sniffImageType(c.data); got != c.want {
				t.Errorf("sniffImageType = %q, want %q", got, c.want)
			}
		})
	}
}

// TestNewImageBlock_RejectsUnsupported: the block is refused rather than built,
// so callers fall back to text instead of failing the turn at the provider.
func TestNewImageBlock_RejectsUnsupported(t *testing.T) {
	for _, c := range []struct {
		name string
		mime string
		data []byte
	}{
		{"svg claiming to be svg", "image/svg+xml", []byte(`<svg/>`)},
		{"bmp claiming to be bmp", "image/bmp", []byte("BM\x00\x00\x00\x00\x00\x00")},
		{"garbage claiming to be png", "image/png", []byte("definitely not a png")},
		{"empty payload", "image/png", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := NewImageBlock(c.mime, c.data); ok {
				t.Error("NewImageBlock accepted bytes no provider can render")
			}
		})
	}
}

// TestNewImageBlock_TrustsBytesOverLabel: the caller's mimeType is a hint from
// an untrustworthy source (a file extension, or an MCP server naming whatever
// it likes). A real PNG mislabelled as SVG is still sent, as a PNG.
func TestNewImageBlock_TrustsBytesOverLabel(t *testing.T) {
	realPNG := encodePNG(t, 32, 32)

	blk, ok := NewImageBlock("image/svg+xml", realPNG)
	if !ok {
		t.Fatal("a real PNG was rejected because of its wrong label")
	}
	if blk.Image.MIMEType != "image/png" {
		t.Errorf("media type = %q, want the sniffed image/png", blk.Image.MIMEType)
	}

	// The same applies when no label is offered at all.
	blk, ok = NewImageBlock("", realPNG)
	if !ok {
		t.Fatal("a real PNG was rejected for having no label")
	}
	if blk.Image.MIMEType != "image/png" {
		t.Errorf("media type = %q, want image/png from an empty label", blk.Image.MIMEType)
	}
}

// TestNewImageBlock_NormalizedTypeStaysSupported: compressImageData re-encodes
// oversized images to JPEG, so the type that leaves must still be one the
// providers accept — otherwise normalization could reintroduce the bug.
func TestNewImageBlock_NormalizedTypeStaysSupported(t *testing.T) {
	blk, ok := NewImageBlock("image/png", encodePNG(t, 2400, 1600))
	if !ok {
		t.Fatal("oversized PNG rejected")
	}
	if !modelImageTypes[blk.Image.MIMEType] {
		t.Errorf("normalized media type %q is not one the providers accept", blk.Image.MIMEType)
	}
}

// encodePNGGray is a second fixture builder kept local to this file so the
// sniff tests don't depend on image_compress_test.go's helper surviving.
func encodePNGGray(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.Gray{Y: 200})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// TestNewImageBlock_SmallImageKeepsBytes pins that a modest image passes
// through untouched, so the sniff didn't accidentally add a re-encode.
func TestNewImageBlock_SmallImageKeepsBytes(t *testing.T) {
	data := encodePNGGray(t, 48, 48)
	blk, ok := NewImageBlock("image/png", data)
	if !ok {
		t.Fatal("small PNG rejected")
	}
	if blk.Image.MIMEType != "image/png" {
		t.Errorf("media type = %q, want image/png", blk.Image.MIMEType)
	}
	if !bytes.Equal(blk.Image.Data, data) {
		t.Error("small PNG bytes were altered")
	}
}
