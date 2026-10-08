// Package paste 存手机发过来的图片。
//
// 为什么是「落盘 + 回一个路径」而不是直接把图片塞进终端：
// claude code 认图片的其中一条路，就是输入里出现一个以 .png/.jpg/.jpeg/.gif/.webp
// 结尾的路径——跟把图片文件拖进终端是同一套。所以这里只要把字节写成文件、
// 把路径交回去，插进终端那一步由浏览器做。
package paste

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MaxBytes 是单张图的上限。
//
// 手机端已经压到最长边 1568px，正常也就几百 KB。留到 8MB 是给「手机端没压成」
// 兜底，同时也挡住拿这个接口当通用文件上传通道。
const MaxBytes = 8 << 20

// Info 是缓存目录的现状，给「关于」弹窗显示用。
type Info struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

// Dir 返回图片缓存目录：~/.go-terminal-hub/paste。
//
// 和 store 的 data.json / settings.json 放同一个父目录，用户翻起来只在一处。
func Dir() string {
	if dir := os.Getenv("GO_TERMINAL_HUB_DIR"); dir != "" {
		return filepath.Join(dir, "paste")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".go-terminal-hub", "paste")
	}
	return filepath.Join(".go-terminal-hub", "paste")
}

// Save 把一张图落盘，返回它的完整路径。
func Save(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("图片是空的")
	}
	if len(data) > MaxBytes {
		return "", fmt.Errorf("图片太大（%d MB，上限 %d MB）", len(data)>>20, MaxBytes>>20)
	}

	ext, ok := sniffImage(data)
	if !ok {
		return "", errors.New("这不是能识别的图片")
	}

	dir := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	// 后缀必须是真后缀——claude code 是按后缀认图片的。
	// 文件名带时间戳，人自己翻目录时能看出先后。
	pattern := "paste-" + time.Now().Format("20060102-150405") + "-*" + ext
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	path := f.Name()

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// Stat 数一下缓存目录里有多少文件、占多少字节。目录不存在算空，不算错。
func Stat() (Info, error) {
	entries, err := os.ReadDir(Dir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Info{}, nil
		}
		return Info{}, err
	}

	var info Info
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		info.Files++
		info.Bytes += fi.Size()
	}
	return info, nil
}

// Clear 清空缓存目录，返回删掉了多少。
func Clear() (Info, error) {
	entries, err := os.ReadDir(Dir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Info{}, nil
		}
		return Info{}, err
	}

	dir := Dir()
	var removed Info
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			return removed, err
		}
		removed.Files++
		removed.Bytes += fi.Size()
	}
	return removed, nil
}

// sniffImage 按文件头判断这是不是图片，并给出对应的后缀。
//
// 不解码图片本身：解一遍只为判类型太重，而且 webp 标准库根本不认。
// 这里要挡的是「把任意文件写进来」，认头就够了。
func sniffImage(data []byte) (string, bool) {
	switch {
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return ".png", true
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return ".jpg", true
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return ".gif", true
	case len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return ".webp", true
	}
	return "", false
}
