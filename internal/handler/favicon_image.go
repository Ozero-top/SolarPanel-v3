package handler

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
)

// faviconMaxPx 卡片图标盒最大 58px（2x 屏按 64px 输出足够清晰），
// 超出就等比缩到 64px 内，显著减小字节数与解码耗时。
const faviconMaxPx = 64

// optimizeFaviconImage 把抓到的位图图标缩到 ≤64px 并以 PNG 落盘；
// SVG / WEBP 以及纯 BMP 的 ICO 标准库无法解码，原样返回（调用方继续存原文件）。
func optimizeFaviconImage(data []byte, ext string) ([]byte, string) {
	var img image.Image
	var err error
	switch ext {
	case ".png":
		img, err = png.Decode(bytes.NewReader(data))
	case ".jpg":
		img, err = jpeg.Decode(bytes.NewReader(data))
	case ".gif":
		img, err = gif.Decode(bytes.NewReader(data))
	case ".ico":
		// 现代 .ico 常内嵌 PNG（Vista 起支持），能解出最大那张；纯 BMP 图标无法用标准库解码
		img = decodeICO(data)
	default:
		return data, ext
	}
	if err != nil || img == nil {
		return data, ext
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 1 || h < 1 {
		return data, ext
	}
	// 小图且体积不大：不动，省一次编解码
	if w <= faviconMaxPx && h <= faviconMaxPx && len(data) <= 24*1024 {
		return data, ext
	}

	nw, nh := w, h
	if w > faviconMaxPx || h > faviconMaxPx {
		if w >= h {
			nw = faviconMaxPx
			nh = h * faviconMaxPx / w
		} else {
			nh = faviconMaxPx
			nw = w * faviconMaxPx / h
		}
		if nw < 1 {
			nw = 1
		}
		if nh < 1 {
			nh = 1
		}
	}

	out := resizeArea(img, nw, nh)
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return data, ext
	}
	// 原图本来就不大、重编码反而更大 → 保留原图，避免帮倒忙
	if w <= faviconMaxPx && h <= faviconMaxPx && buf.Len() >= len(data) {
		return data, ext
	}
	return buf.Bytes(), ".png"
}

// resizeArea 面积加权平均缩放：缩小时比最近邻平滑，且只需目标图大小的内存。
func resizeArea(src image.Image, dstW, dstH int) *image.NRGBA {
	sb := src.Bounds()
	srcW, srcH := sb.Dx(), sb.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))

	for y := 0; y < dstH; y++ {
		sy0 := sb.Min.Y + y*srcH/dstH
		sy1 := sb.Min.Y + (y+1)*srcH/dstH
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < dstW; x++ {
			sx0 := sb.Min.X + x*srcW/dstW
			sx1 := sb.Min.X + (x+1)*srcW/dstW
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var sr, sg, sb2, sa, cnt float64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					c := color.NRGBAModel.Convert(src.At(sx, sy)).(color.NRGBA)
					a := float64(c.A)
					// RGB 按 alpha 加权，避免透明背景边缘出现暗边
					sr += float64(c.R) * a
					sg += float64(c.G) * a
					sb2 += float64(c.B) * a
					sa += a
					cnt++
				}
			}
			if cnt == 0 {
				continue
			}
			var r, g, b, a uint8
			a = uint8(sa / cnt)
			if sa > 0 {
				r = uint8(sr / sa)
				g = uint8(sg / sa)
				b = uint8(sb2 / sa)
			}
			dst.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: a})
		}
	}
	return dst
}

// decodeICO 从 .ico 目录里取面积最大、且以 PNG 形式内嵌的那张图标。
// 纯 BMP 载荷（老式 ICO）标准库无法解码，返回 nil 由调用方保底存原文件。
func decodeICO(data []byte) image.Image {
	if len(data) < 6 || data[0] != 0 || data[1] != 0 || data[2] != 1 || data[3] != 0 {
		return nil
	}
	n := int(binary.LittleEndian.Uint16(data[4:6]))
	var best []byte
	bestPx := -1
	for i := 0; i < n; i++ {
		off := 6 + i*16
		if off+16 > len(data) {
			break
		}
		w, h := int(data[off]), int(data[off+1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		size := int(binary.LittleEndian.Uint32(data[off+8 : off+12]))
		pos := int(binary.LittleEndian.Uint32(data[off+12 : off+16]))
		if size <= 0 || pos < 0 || pos+size > len(data) {
			continue
		}
		if w*h <= bestPx {
			continue
		}
		best = data[pos : pos+size]
		bestPx = w * h
	}
	if best == nil {
		return nil
	}
	// 优先解内嵌 PNG（Vista 起常见），否则按 BMP 载荷解码
	if len(best) >= 8 && best[0] == 0x89 && best[1] == 0x50 && best[2] == 0x4E && best[3] == 0x47 {
		if img, err := png.Decode(bytes.NewReader(best)); err == nil {
			return img
		}
	}
	return decodeICOBMP(best)
}

// decodeICOBMP 解码 ICO 里的 BITMAPINFOHEADER 载荷（仅 32/24 位，老式 8/4/1 位调色板图标跳过）。
func decodeICOBMP(entry []byte) image.Image {
	if len(entry) < 40 {
		return nil
	}
	hdrSize := int(binary.LittleEndian.Uint32(entry[0:4]))
	w := int(int32(binary.LittleEndian.Uint32(entry[4:8])))
	h2 := int(int32(binary.LittleEndian.Uint32(entry[8:12])))
	bpp := int(binary.LittleEndian.Uint16(entry[14:16]))
	comp := binary.LittleEndian.Uint32(entry[16:20])
	if hdrSize < 40 || hdrSize > len(entry) || comp != 0 || w <= 0 || w > 1024 || h2 < 2 {
		return nil
	}
	if bpp != 32 && bpp != 24 {
		return nil
	}
	h := h2 / 2
	rowBytes := ((w*bpp + 31) / 32) * 4
	off := hdrSize
	if off+rowBytes*h > len(entry) {
		return nil
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	// 24 位图标后面跟 1bpp 的 AND 掩码：bit=1 表示透明
	maskOff := off + rowBytes*h
	maskRow := ((w + 31) / 32) * 4
	hasMask := bpp == 24 && maskOff+maskRow*h <= len(entry)
	hasAlpha := false
	for y := 0; y < h; y++ {
		row := entry[off+(h-1-y)*rowBytes : off+(h-y)*rowBytes]
		var mask []byte
		if hasMask {
			mask = entry[maskOff+(h-1-y)*maskRow : maskOff+(h-y)*maskRow]
		}
		for x := 0; x < w; x++ {
			i := x * (bpp / 8)
			if bpp == 32 {
				a := row[i+3]
				if a != 0 {
					hasAlpha = true
				}
				dst.SetNRGBA(x, y, color.NRGBA{R: row[i+2], G: row[i+1], B: row[i], A: a})
				continue
			}
			var a uint8 = 255
			if mask != nil && mask[x/8]&(0x80>>uint(x%8)) != 0 {
				a = 0
			}
			dst.SetNRGBA(x, y, color.NRGBA{R: row[i+2], G: row[i+1], B: row[i], A: a})
		}
	}
	if bpp == 32 && !hasAlpha {
		// 老式 32 位图标常把 alpha 全写成 0，此时按不透明处理
		for i := 3; i < len(dst.Pix); i += 4 {
			dst.Pix[i] = 255
		}
	}
	return dst
}
