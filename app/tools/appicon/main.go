// Command appicon builds the macOS .icns of the app from two drawings: the
// full icon and a simplified one for the smallest sizes, where the full
// drawing blurs into a blob (Finder lists, menus).
//
//	go run ./tools/appicon -small appicon/icon-small.png -o appicon/AppIcon.icns resources/icon.png
//
// mygo.json points macos.icon at the result; mygo renders resources/icon.png
// alone for every other platform.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"

	"golang.org/x/image/draw"
)

// entries are the icns elements mygo writes too. icp4 and icp5 (16 and 32
// pixels at 1x) are left out on purpose: IconServices decodes their PNG
// payloads as legacy ARGB and draws noise; macOS scales those sizes down
// from ic11 and ic12 instead.
var entries = []struct {
	kind string
	size int
}{
	{"ic11", 32}, {"ic12", 64}, {"ic07", 128}, {"ic13", 256}, {"ic08", 256},
	{"ic14", 512}, {"ic09", 512}, {"ic10", 1024},
}

func main() {
	small := flag.String("small", "", "simplified drawing for sizes up to -small-max pixels")
	smallMax := flag.Int("small-max", 32, "largest pixel size that uses the simplified drawing")
	out := flag.String("o", "AppIcon.icns", "output .icns")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: appicon [-small small.png] [-small-max 32] [-o AppIcon.icns] icon.png")
		os.Exit(2)
	}
	full, err := load(flag.Arg(0))
	if err != nil {
		fail(err)
	}
	simple := full
	if *small != "" {
		if simple, err = load(*small); err != nil {
			fail(err)
		}
	}
	data, err := build(full, simple, *smallMax)
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fail(err)
	}
}

func build(full, simple image.Image, smallMax int) ([]byte, error) {
	var body bytes.Buffer
	for _, e := range entries {
		src := full
		if e.size <= smallMax {
			src = simple
		}
		var p bytes.Buffer
		if err := png.Encode(&p, scale(src, e.size)); err != nil {
			return nil, err
		}
		body.WriteString(e.kind)
		_ = binary.Write(&body, binary.BigEndian, uint32(p.Len()+8))
		body.Write(p.Bytes())
	}
	var icns bytes.Buffer
	icns.WriteString("icns")
	_ = binary.Write(&icns, binary.BigEndian, uint32(body.Len()+8))
	icns.Write(body.Bytes())
	return icns.Bytes(), nil
}

func scale(src image.Image, size int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return dst
}

func load(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if b := img.Bounds(); b.Dx() != b.Dy() || b.Dx() < 1024 {
		return nil, fmt.Errorf("%s: need a square image of at least 1024 pixels, got %dx%d", path, b.Dx(), b.Dy())
	}
	return img, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "appicon:", err)
	os.Exit(1)
}
