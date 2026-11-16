package library

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
)

var errBadICO = errors.New("icon: unsupported ICO data")

// ICO 是 Windows 图标容器，很多网站的 /favicon.ico 就是它，mygo 的
// ui.DecodeImage 不认。这里只解最常见的两种条目：内嵌 PNG 和 32bpp 的
// BMP 老格式（open.feishu.cn 的就是这种）；其余报错，让界面用回落图标。

var icoPNGSig = []byte("\x89PNG\r\n\x1a\n")

const (
	icoMaxEntries   = 64
	icoMaxDimension = 4096
)

func decodeICO(data []byte) (image.Image, error) {
	if len(data) < 6 || binary.LittleEndian.Uint16(data[0:2]) != 0 {
		return nil, errBadICO
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 || count > icoMaxEntries || len(data) < 6+16*count {
		return nil, errBadICO
	}

	// 挑面积最大的条目；并列取先出现的。
	var best struct {
		score int
		off   int64
		size  int64
		png   bool
	}
	for i := range count {
		e := data[6+16*i : 6+16*(i+1)]
		w, h := int(e[0]), int(e[1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		size := int64(binary.LittleEndian.Uint32(e[8:12]))
		off := int64(binary.LittleEndian.Uint32(e[12:16]))
		if size <= 0 || off < 0 || off+size > int64(len(data)) {
			return nil, errBadICO
		}
		score := w * h
		if best.size == 0 || score > best.score {
			best = struct {
				score int
				off   int64
				size  int64
				png   bool
			}{score, off, size, bytes.HasPrefix(data[off:off+size], icoPNGSig)}
		}
	}
	body := data[best.off : best.off+best.size]
	if best.png {
		return png.Decode(bytes.NewReader(body))
	}
	return decodeICOBMP(body)
}

// decodeICOBMP 解 32bpp 的 BMP 条目：40 字节 BITMAPINFOHEADER，自底向上的
// BGRA 像素，底下再垫一层 1bpp 的 AND 掩码（ICO 里头文件高度是两倍）。
// 32bpp 的 alpha 通常齐全；全是零时才退回掩码（老图标只有轮廓没有 alpha）。
func decodeICOBMP(body []byte) (image.Image, error) {
	if len(body) < 40 || binary.LittleEndian.Uint32(body[0:4]) != 40 {
		return nil, errBadICO
	}
	w := int(int32(binary.LittleEndian.Uint32(body[4:8])))
	h := int(int32(binary.LittleEndian.Uint32(body[8:12]))) / 2 // 头里含掩码高度
	if w <= 0 || h <= 0 || w > icoMaxDimension || h > icoMaxDimension {
		return nil, errBadICO
	}
	if binary.LittleEndian.Uint16(body[14:16]) != 32 {
		return nil, errBadICO
	}
	pix := body[40:]
	if int64(len(pix)) < int64(w)*4*int64(h) {
		return nil, errBadICO
	}
	maskRow := (w + 31) / 32 * 4
	var mask []byte
	if rest := int64(len(pix)) - int64(w)*4*int64(h); rest >= int64(maskRow)*int64(h) {
		mask = pix[int64(w)*4*int64(h):]
	}

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	anyAlpha := false
	for y := 0; y < h; y++ {
		src := pix[(h-1-y)*w*4:] // 行序自底向上
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := 0; x < w; x++ {
			b, g, r, a := src[x*4], src[x*4+1], src[x*4+2], src[x*4+3]
			if a != 0 {
				anyAlpha = true
			}
			row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = r, g, b, a
		}
	}
	if mask != nil && !anyAlpha {
		for y := 0; y < h; y++ {
			// 掩码也是自底向上，MSB 在前，0 是不透明。
			mrow := mask[(h-1-y)*maskRow:]
			row := img.Pix[y*img.Stride : y*img.Stride+w*4]
			for x := 0; x < w; x++ {
				if mrow[x/8]&(1<<(7-x%8)) == 0 {
					row[x*4+3] = 255
				}
			}
		}
	}
	return img, nil
}
