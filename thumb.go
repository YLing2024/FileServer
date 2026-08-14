package main

import (
	"container/list"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/gif" // 注册 GIF 解码器
	_ "image/png" // 注册 PNG 解码器
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw" // CatmullRom 高质量缩放
	_ "golang.org/x/image/bmp"  // 注册 BMP 解码器
	_ "golang.org/x/image/tiff" // 注册 TIFF 解码器
	_ "golang.org/x/image/webp" // 注册 WEBP 解码器
)

const (
	thumbMaxDim  = 1024 // 客户端可请求的最大缩略图边长
	thumbGiantPx = 6000 // 超过此尺寸的原图不做解码缩放，直接返回原图
	thumbMaxAge  = 7 * 24 * time.Hour
)

// ThumbCache 缩略图磁盘缓存 + 内存 LRU
type ThumbCache struct {
	dir string
	mu  sync.Mutex
	mem map[string][]byte
	ll  *list.List
	max int // 内存缓存条目上限
}

func NewThumbCache() *ThumbCache {
	dir := os.Getenv("LOCALAPPDATA")
	if dir == "" {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "FileServer", "thumb")
	os.MkdirAll(dir, 0o755)
	c := &ThumbCache{dir: dir, mem: make(map[string][]byte), ll: list.New(), max: 512}
	go c.cleanup()
	return c
}

// cleanup 删除超过 7 天的缓存文件
func (c *ThumbCache) cleanup() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-thumbMaxAge)
	for _, e := range entries {
		if e.IsDir() || len(e.Name()) != 44 || !strings.HasSuffix(e.Name(), ".jpg") {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(c.dir, e.Name()))
		}
	}
}

func (c *ThumbCache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	if data, ok := c.mem[key]; ok {
		c.mu.Unlock()
		return data, true
	}
	c.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(c.dir, key+".jpg"))
	if err != nil {
		return nil, false
	}
	c.mu.Lock()
	c.putMem(key, data)
	c.mu.Unlock()
	return data, true
}

func (c *ThumbCache) Put(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putMem(key, data)
	os.WriteFile(filepath.Join(c.dir, key+".jpg"), data, 0o644)
}

func (c *ThumbCache) putMem(key string, data []byte) {
	if _, ok := c.mem[key]; ok {
		return
	}
	c.mem[key] = data
	c.ll.PushBack(key)
	if c.ll.Len() > c.max {
		front := c.ll.Front()
		if front != nil {
			delete(c.mem, front.Value.(string))
			c.ll.Remove(front)
		}
	}
}

// handleThumb GET /api/thumb?path=&w=&h= 返回方形缩略图 JPEG
func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	abs, err := s.safePath(q.Get("path"))
	if err != nil {
		writeErr(w, errToStatus(err), err.Error())
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		writeErr(w, http.StatusNotFound, "无法生成缩略图")
		return
	}

	wq := clampDim(q.Get("w"), 256)
	hq := clampDim(q.Get("h"), 256)

	kind := fileKind(fi.Name(), false)
	switch kind {
	case "image":
		s.serveImageThumb(w, r, abs, fi, wq, hq)
	case "video":
		s.serveVideoThumb(w, r, abs, fi, wq, hq)
	default:
		writeErr(w, http.StatusNotFound, "该类型不支持缩略图")
	}
}

func clampDim(v string, def int) int {
	n := 0
	for _, ch := range v {
		if ch < '0' || ch > '9' {
			n = 0
			break
		}
		n = n*10 + int(ch-'0')
	}
	if n <= 0 {
		return def
	}
	if n > thumbMaxDim {
		return thumbMaxDim
	}
	return n
}

// thumbKey 缓存键：真实路径|大小|修改时间|尺寸 的 SHA1
func thumbKey(abs string, fi os.FileInfo, w, h int) string {
	hsh := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d|%dx%d", abs, fi.Size(), fi.ModTime().UnixNano(), w, h)))
	return hex.EncodeToString(hsh[:])
}

// serveCached 处理 ETag/304 并输出缓存内容
func serveCached(w http.ResponseWriter, r *http.Request, key string, data []byte, contentType string) {
	etag := `"` + key + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Write(data)
}

// serveImageThumb 图片缩略图：svg 原样返回；巨图直接返回原图；其余解码裁剪
func (s *Server) serveImageThumb(w http.ResponseWriter, r *http.Request, abs string, fi os.FileInfo, wq, hq int) {
	ext := strings.ToLower(filepath.Ext(abs))

	// SVG：浏览器可直接渲染，返回原文件
	if ext == ".svg" {
		data, err := os.ReadFile(abs)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "读取文件失败")
			return
		}
		serveCached(w, r, thumbKey(abs, fi, 0, 0), data, "image/svg+xml")
		return
	}

	// 先读尺寸：巨图不解码，直接返回原图（防内存尖峰）
	cfg, _, err := decodeConfig(abs)
	if err != nil {
		writeErr(w, http.StatusNotFound, "无法解码图片")
		return
	}
	if cfg.Width > thumbGiantPx || cfg.Height > thumbGiantPx {
		data, err := os.ReadFile(abs)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "读取文件失败")
			return
		}
		ct := "image/" + strings.TrimPrefix(ext, ".")
		serveCached(w, r, thumbKey(abs, fi, 0, 0), data, ct)
		return
	}
	// 原图不大时直接原样输出（浏览器缩放足够清晰）
	if cfg.Width <= 512 && cfg.Height <= 512 {
		data, err := os.ReadFile(abs)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "读取文件失败")
			return
		}
		ct := "image/" + strings.TrimPrefix(ext, ".")
		serveCached(w, r, thumbKey(abs, fi, 0, 0), data, ct)
		return
	}

	key := thumbKey(abs, fi, wq, hq)
	if data, ok := s.thumbs.Get(key); ok {
		serveCached(w, r, key, data, "image/jpeg")
		return
	}

	src, err := decodeImageFile(abs)
	if err != nil {
		writeErr(w, http.StatusNotFound, "无法解码图片")
		return
	}
	out := coverCrop(src, wq, hq)
	// 编码到内存
	var enc bytesBuffer
	if err := jpeg.Encode(&enc, out, &jpeg.Options{Quality: 80}); err != nil {
		writeErr(w, http.StatusInternalServerError, "编码缩略图失败")
		return
	}
	data := enc.Bytes()
	s.thumbs.Put(key, data)
	serveCached(w, r, key, data, "image/jpeg")
}

// bytesBuffer 简单的内存写入缓冲（避免 strings.Builder 的拷贝语义）
type bytesBuffer struct{ b []byte }

func (bb *bytesBuffer) Write(p []byte) (int, error) {
	bb.b = append(bb.b, p...)
	return len(p), nil
}
func (bb *bytesBuffer) Bytes() []byte { return bb.b }

// decodeConfig 读取图片尺寸（自动关闭文件句柄）
func decodeConfig(path string) (image.Config, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return image.Config{}, "", err
	}
	defer f.Close()
	return image.DecodeConfig(f)
}

// decodeImageFile 解码图片文件（gif 取第一帧）
func decodeImageFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

// coverCrop 将图片中心裁剪为 w×h 方形并缩放
func coverCrop(src image.Image, w, h int) image.Image {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if sw <= 0 || sh <= 0 {
		return src
	}
	srcRatio := float64(sw) / float64(sh)
	dstRatio := float64(w) / float64(h)
	var cx, cy, cw, ch int
	if srcRatio > dstRatio {
		cw = int(float64(sh) * dstRatio)
		ch = sh
		cx = (sw - cw) / 2
	} else {
		ch = int(float64(sw) / dstRatio)
		cw = sw
		cy = (sh - ch) / 2
	}
	if cw <= 0 {
		cw = 1
	}
	if ch <= 0 {
		ch = 1
	}
	sub, ok := src.(interface {
		SubImage(image.Rectangle) image.Image
	})
	var region image.Image
	if ok {
		region = sub.SubImage(image.Rect(cx, cy, cx+cw, cy+ch))
	} else {
		region = src
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.Black}, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), region, region.Bounds(), draw.Over, nil)
	return dst
}
