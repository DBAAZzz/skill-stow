package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func solid(c color.Color) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	for y := range 1024 {
		for x := range 1024 {
			img.Set(x, y, c)
		}
	}
	return img
}

// The smallest sizes show the simplified drawing and the rest the full one,
// so Finder lists get the legible version without touching the Dock icon.
func TestSmallSizesUseTheSimplifiedDrawing(t *testing.T) {
	full, simple := solid(color.NRGBA{0, 0, 255, 255}), solid(color.NRGBA{255, 0, 0, 255})
	data, err := build(full, simple, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("icns")) || binary.BigEndian.Uint32(data[4:8]) != uint32(len(data)) {
		t.Fatalf("bad icns header")
	}
	got := map[string]color.NRGBA{}
	for off := 8; off < len(data); {
		kind, n := string(data[off:off+4]), int(binary.BigEndian.Uint32(data[off+4:off+8]))
		img, err := png.Decode(bytes.NewReader(data[off+8 : off+n]))
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		got[kind] = color.NRGBAModel.Convert(img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2)).(color.NRGBA)
		off += n
	}
	if len(got) != len(entries) {
		t.Fatalf("got %d entries, want %d", len(got), len(entries))
	}
	if got["ic11"].R != 255 {
		t.Errorf("ic11 (32px) should come from the simplified drawing, got %v", got["ic11"])
	}
	for _, kind := range []string{"ic12", "ic07", "ic10"} {
		if got[kind].B != 255 {
			t.Errorf("%s should come from the full drawing, got %v", kind, got[kind])
		}
	}
}
