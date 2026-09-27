// makeico 把 build/windows/icon.ico 里指定尺寸的那几档，换成另一张图缩放后的样子。
//
// 为什么需要它：`wails3 generate icons` 只吃单张输入图（-sizes 只能挑要出哪几档，
// 不能给每档喂不同素材），所以「大尺寸一套、小尺寸一套」它做不到。
//
// 为什么是「改现有的 ico」而不是「从零生成 ico」：ico 容器里的字段约定（planes /
// bitCount / 目录顺序 / PNG 还是 DIB）各写各的。这里只替换图像数据、其余原样搬走，
// 容器格式就继承 `wails3 generate icons` 的产物，不引入新的兼容性风险。
//
// 用法（工作目录不限，路径相对模块根解析）：
//
//	go run ./tools/makeico
//	go run ./tools/makeico -sizes 32,24,16 -input build/appicon-compact.png
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type entry struct {
	w, h   int    // 0 在 ico 目录里表示 256
	fields []byte // 目录里的 colorCount/reserved/planes/bitCount 四个字段，原样保留
	data   []byte // 图像数据，原样保留
}

func main() {
	icoPath := flag.String("ico", "build/windows/icon.ico", "要改的 ico（相对模块根）")
	inputPath := flag.String("input", "build/appicon-compact.png", "小尺寸那套素材（相对模块根）")
	sizesFlag := flag.String("sizes", "32,24,16", "要替换的尺寸，逗号分隔")
	flag.Parse()

	root, err := moduleRoot()
	must(err)

	var sizes []int
	for _, s := range strings.Split(*sizesFlag, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		must(err)
		sizes = append(sizes, n)
	}

	ico := filepath.Join(root, *icoPath)
	entries, err := readICO(ico)
	must(err)

	src, err := readPNG(filepath.Join(root, *inputPath))
	must(err)

	replaced := 0
	for i := range entries {
		if !contains(sizes, entries[i].w) {
			continue
		}
		entries[i].data, err = encodePNG(downscale(src, entries[i].w, entries[i].h))
		must(err)
		replaced++
		fmt.Printf("  %dx%d  <- %s\n", entries[i].w, entries[i].h, *inputPath)
	}
	if replaced != len(sizes) {
		must(fmt.Errorf("要替换 %d 档，实际只替换了 %d 档——ico 里缺尺寸？", len(sizes), replaced))
	}

	must(writeICO(ico, entries))
	fmt.Printf("已更新 %s（共 %d 档，替换 %d 档）\n", ico, len(entries), replaced)
}

// moduleRoot 从工作目录往上找第一个含 go.mod 的目录。
// 这样无论从模块根还是从 build/ 下调用，路径参数都按模块根解析。
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("往上找到根也没找到 go.mod")
		}
		dir = parent
	}
}

func contains(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func readICO(path string) ([]entry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < 6 || binary.LittleEndian.Uint16(raw[0:2]) != 0 || binary.LittleEndian.Uint16(raw[2:4]) != 1 {
		return nil, fmt.Errorf("%s 不是 ico（头 6 字节不对）", path)
	}
	n := int(binary.LittleEndian.Uint16(raw[4:6]))
	if len(raw) < 6+16*n {
		return nil, fmt.Errorf("%s 目录被截断", path)
	}
	entries := make([]entry, 0, n)
	for i := 0; i < n; i++ {
		d := raw[6+16*i : 6+16*(i+1)]
		size := int(binary.LittleEndian.Uint32(d[8:12]))
		off := int(binary.LittleEndian.Uint32(d[12:16]))
		if off+size > len(raw) {
			return nil, fmt.Errorf("%s 第 %d 档数据越界", path, i)
		}
		// 目录里宽度/高度是 1 字节，0 表示 256
		w, h := int(d[0]), int(d[1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		entries = append(entries, entry{
			w: w, h: h,
			fields: append([]byte(nil), d[2:8]...),
			data:   append([]byte(nil), raw[off:off+size]...),
		})
	}
	return entries, nil
}

func writeICO(path string, entries []entry) error {
	buf := make([]byte, 6, 6+16*len(entries))
	binary.LittleEndian.PutUint16(buf[2:4], 1) // type = 1 (ICO)
	binary.LittleEndian.PutUint16(buf[4:6], uint16(len(entries)))

	offset := 6 + 16*len(entries)
	dir := make([]byte, 0, 16*len(entries))
	for _, e := range entries {
		d := make([]byte, 16)
		d[0], d[1] = byte(e.w%256), byte(e.h%256) // 256 存成 0
		copy(d[2:8], e.fields)
		binary.LittleEndian.PutUint32(d[8:12], uint32(len(e.data)))
		binary.LittleEndian.PutUint32(d[12:16], uint32(offset))
		offset += len(e.data)
		dir = append(dir, d...)
	}

	buf = append(buf, dir...)
	for _, e := range entries {
		buf = append(buf, e.data...)
	}
	return os.WriteFile(path, buf, 0o644)
}

// downscale 用面积平均（box filter）把 src 缩到 w x h。
//
// color.Color.RGBA() 返回的已经是**预乘 alpha** 的值，所以这里直接对四个通道加权平均
// 就是正确的预乘平均；最后再除以 alpha 还原成 image.NRGBA 要的非预乘值。
// 不这么做而直接平均非预乘 color，透明边缘会渗出黑色。
func downscale(src image.Image, w, h int) *image.NRGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	sx := float64(sw) / float64(w)
	sy := float64(sh) / float64(h)

	for dy := 0; dy < h; dy++ {
		y0, y1 := float64(dy)*sy, float64(dy+1)*sy
		for dx := 0; dx < w; dx++ {
			x0, x1 := float64(dx)*sx, float64(dx+1)*sx

			var pr, pg, pb, pa, wsum float64
			for y := int(y0); y < int(math.Ceil(y1)); y++ {
				if y >= sh {
					break
				}
				// 该行落在目标像素 [y0,y1) 内的占比
				wy := math.Min(y1, float64(y+1)) - math.Max(y0, float64(y))
				if wy <= 0 {
					continue
				}
				for x := int(x0); x < int(math.Ceil(x1)); x++ {
					if x >= sw {
						break
					}
					wx := math.Min(x1, float64(x+1)) - math.Max(x0, float64(x))
					if wx <= 0 {
						continue
					}
					wt := wx * wy
					r, g, b, a := src.At(sb.Min.X+x, sb.Min.Y+y).RGBA()
					pr += float64(r) / 65535 * wt
					pg += float64(g) / 65535 * wt
					pb += float64(b) / 65535 * wt
					pa += float64(a) / 65535 * wt
					wsum += wt
				}
			}
			if wsum == 0 {
				continue
			}
			pr, pg, pb, pa = pr/wsum, pg/wsum, pb/wsum, pa/wsum

			o := dst.PixOffset(dx, dy)
			if pa <= 0 {
				continue // 全透明，留零值
			}
			dst.Pix[o+0] = clamp8(pr / pa * 255)
			dst.Pix[o+1] = clamp8(pg / pa * 255)
			dst.Pix[o+2] = clamp8(pb / pa * 255)
			dst.Pix[o+3] = clamp8(pa * 255)
		}
	}
	return dst
}

func clamp8(v float64) byte {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	default:
		return byte(math.Round(v))
	}
}

func readPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "makeico:", err)
		os.Exit(1)
	}
}
