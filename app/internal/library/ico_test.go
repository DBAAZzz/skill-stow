package library

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"testing"
)

// 真实样本：open.feishu.cn 的 /favicon.ico，48×48 的 32bpp BMP 条目。
func TestDecodeICOBMPEntry(t *testing.T) {
	data, err := os.ReadFile("testdata/feishu.ico")
	if err != nil {
		t.Fatal(err)
	}
	img, err := decodeICO(data)
	if err != nil {
		t.Fatalf("decodeICO: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 48 || b.Dy() != 48 {
		t.Fatalf("bounds = %v, want 48x48", b)
	}
	// 透明和不透明的像素都得有：飞书 logo 有形状也有透明背景。
	transparent, opaque := 0, 0
	for y := range 48 {
		for x := range 48 {
			if _, _, _, a := img.At(x, y).RGBA(); a == 0 {
				transparent++
			} else {
				opaque++
			}
		}
	}
	if opaque == 0 || transparent == 0 {
		t.Errorf("opaque = %d, transparent = %d, 都该有", opaque, transparent)
	}
}

func TestDecodeICOPNGEntry(t *testing.T) {
	// 拼一个条目是内嵌 PNG 的 ICO：很多现代网站的 favicon.ico 其实是 PNG。
	var pngBuf bytes.Buffer
	src := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for i := 0; i < len(src.Pix); i += 4 {
		src.Pix[i], src.Pix[i+3] = 255, 255
	}
	if err := png.Encode(&pngBuf, src); err != nil {
		t.Fatal(err)
	}

	ico := make([]byte, 6+16)
	binary.LittleEndian.PutUint16(ico[4:6], 1)
	ico[6], ico[7] = 16, 16 // 宽、高
	binary.LittleEndian.PutUint16(ico[12:14], 32)
	binary.LittleEndian.PutUint32(ico[14:18], uint32(pngBuf.Len()))
	binary.LittleEndian.PutUint32(ico[18:22], 22)
	ico = append(ico, pngBuf.Bytes()...)

	img, err := decodeICO(ico)
	if err != nil {
		t.Fatalf("decodeICO: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 16 || b.Dy() != 16 {
		t.Fatalf("bounds = %v, want 16x16", b)
	}
}

// 多个条目时取面积最大的那个；小条目坏了也不影响。
func TestDecodeICOPicksLargestEntry(t *testing.T) {
	entry := func(size int, off, dataLen int) []byte {
		e := make([]byte, 16)
		e[0], e[1] = byte(size), byte(size)
		binary.LittleEndian.PutUint16(e[6:8], 32)
		binary.LittleEndian.PutUint32(e[8:12], uint32(dataLen))
		binary.LittleEndian.PutUint32(e[12:16], uint32(off))
		return e
	}
	var big bytes.Buffer
	src := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for i := 0; i < len(src.Pix); i += 4 {
		src.Pix[i+2], src.Pix[i+3] = 255, 255 // 蓝色不透明
	}
	if err := png.Encode(&big, src); err != nil {
		t.Fatal(err)
	}

	// 布局：6 字节头 + 两个 16 字节条目 + 大图数据。小条目指向大图开头
	// 截 16 字节的假数据，解不开；但它面积小，轮不到它。
	dataStart := 6 + 32
	ico := append([]byte{0, 0, 1, 0, 2, 0}, // 保留 0、类型 1、条目数 2
		append(entry(32, dataStart, big.Len()), entry(16, dataStart, 16)...)...)
	ico = append(ico, big.Bytes()...)

	img, err := decodeICO(ico)
	if err != nil {
		t.Fatalf("decodeICO: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 32 {
		t.Fatalf("bounds = %v, want 32 宽", b)
	}
}

func TestDecodeICOErrors(t *testing.T) {
	bad := [][]byte{
		nil,
		[]byte("short"),
		{0, 0, 2, 0, 0, 0}, // 条目数是 0
		{0, 0, 1, 0},       // 头在但条目被截断
	}
	for i, data := range bad {
		if _, err := decodeICO(data); err == nil {
			t.Errorf("case %d: 想要报错", i)
		}
	}
	// 24bpp 的 BMP 条目不支持，报错走回落图标。
	e := make([]byte, 22)
	binary.LittleEndian.PutUint16(e[4:6], 1)
	e[6], e[7] = 8, 8
	binary.LittleEndian.PutUint16(e[12:14], 24)
	binary.LittleEndian.PutUint32(e[14:18], 100)
	binary.LittleEndian.PutUint32(e[18:22], 22)
	bad = append(bad, append(e, make([]byte, 100)...))
	for i, data := range bad {
		if _, err := decodeICO(data); err == nil {
			t.Errorf("case %d: 想要报错", i)
		}
	}
}
