package server

import (
	"bytes"
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
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw" // CatmullRom 高质量缩放
	_ "golang.org/x/image/bmp"  // 注册 BMP 解码器
	_ "golang.org/x/image/tiff" // 注册 TIFF 解码器
	_ "golang.org/x/image/webp" // 注册 WEBP 解码器
)

const (
	thumbMaxDim               = 1024 // 客户端可请求的最大缩略图边长
	thumbDirectServeThreshold = 6000 // 超过此尺寸的原图不做解码缩放，直接返回原图
	thumbMaxAge               = 7 * 24 * time.Hour
	thumbImgMaxConc           = 4      // 图片解码/整读并发上限（多客户端批量缩略图提速）
	thumbTmpMaxAge            = time.Hour // .tmp 残留清理阈值
)

// ThumbCache 缩略图磁盘缓存 + 内存 LRU（图片缩略图专用；视频缩略图已 100% 浏览器抽帧）
type ThumbCache struct {
	dir   string
	mu    sync.Mutex
	mem   map[string][]byte
	elem  map[string]*list.Element // key -> 链表节点（O(1) LRU 触达）
	ll    *list.List
	max   int
	genMu sync.Mutex            // 保护 calls（singleflight 生成者判定）
	calls map[string]*thumbCall // key -> 进行中的生成
}

// thumbCall singleflight 状态：等待者 wg.Wait，生成者完成后 Done
type thumbCall struct {
	wg   sync.WaitGroup
	data []byte
	ok   bool
}

// NewThumbCache 创建缩略图缓存（磁盘缓存 + 内存 LRU）。
// 缓存目录：共享根目录下的隐藏文件夹 .FileServer\thumb（不污染系统目录、
// 不在其他位置留文件；--hidden 未开启时该隐藏目录对列表/直链/搜索均不可见）。
// 共享根目录不可写（只读介质/无权限）时回退系统临时目录。
func NewThumbCache(root string) *ThumbCache {
	dir := filepath.Join(root, ".FileServer", "thumb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		dir = filepath.Join(os.TempDir(), "FileServer", "thumb")
		os.MkdirAll(dir, 0o755)
	}
	c := &ThumbCache{dir: dir, mem: make(map[string][]byte), elem: make(map[string]*list.Element), ll: list.New(), max: 512}
	go c.cleanupLoop()
	return c
}

// cleanupLoop 定期清理缓存（每 1h 一次），避免长期运行磁盘缓存无限增长
func (c *ThumbCache) cleanupLoop() {
	for {
		c.cleanup()
		time.Sleep(time.Hour)
	}
}

// cleanup 删除超过 7 天的缓存文件，并清理残留的 .tmp（构建中断留下的半成品）
func (c *ThumbCache) cleanup() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-thumbMaxAge)
	tmpCutoff := time.Now().Add(-thumbTmpMaxAge)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if strings.HasSuffix(name, ".tmp") {
			if info.ModTime().Before(tmpCutoff) {
				os.Remove(filepath.Join(c.dir, name))
			}
			continue
		}
		if len(name) != 44 || !strings.HasSuffix(name, ".jpg") {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(c.dir, name))
		}
	}
}

// Do 以 singleflight 方式执行生成函数：同一 key 的并发请求只有一个生成者，
// 其余等待结果后直接复用。不同 key 之间完全并发执行。
// 用 defer 保证生成函数 panic 时等待者也能被唤醒（否则该 key 永久阻塞，
// 一个坏文件即可打挂缩略图接口）。
func (c *ThumbCache) Do(key string, fn func() ([]byte, bool)) (data []byte, ok bool) {
	c.genMu.Lock()
	if c.calls == nil {
		c.calls = make(map[string]*thumbCall)
	}
	if call, found := c.calls[key]; found {
		c.genMu.Unlock()
		call.wg.Wait() // 等待生成者完成（Done 前的写入对 Wait 返回后可见）
		return call.data, call.ok
	}
	call := &thumbCall{}
	call.wg.Add(1)
	c.calls[key] = call
	c.genMu.Unlock()

	// 本 goroutine 是唯一生成者：panic 时恢复并标记失败，等待者照常返回
	defer func() {
		if p := recover(); p != nil {
			call.data, call.ok = nil, false
		}
		call.wg.Done()
		c.genMu.Lock()
		delete(c.calls, key)
		c.genMu.Unlock()
	}()

	call.data, call.ok = fn()
	return call.data, call.ok
}

func (c *ThumbCache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	if data, ok := c.mem[key]; ok {
		c.touch(key)
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

// Put 写缓存：先写临时文件再原子重命名，避免并发写同一文件时损坏
func (c *ThumbCache) Put(key string, data []byte) {
	c.mu.Lock()
	c.putMem(key, data)
	c.mu.Unlock()
	tmp := filepath.Join(c.dir, key+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err == nil {
		os.Rename(tmp, filepath.Join(c.dir, key+".jpg"))
	}
}

// putMem 插入或刷新内存缓存条目；超出上限时淘汰最久未使用的条目
func (c *ThumbCache) putMem(key string, data []byte) {
	if c.elem[key] != nil {
		c.touch(key)
		return
	}
	c.mem[key] = data
	c.elem[key] = c.ll.PushBack(key)
	for c.ll.Len() > c.max {
		front := c.ll.Front()
		if front == nil {
			break
		}
		k := front.Value.(string)
		delete(c.mem, k)
		delete(c.elem, k)
		c.ll.Remove(front)
	}
}

// touch 把 key 移动到链表尾部（最近使用）
func (c *ThumbCache) touch(key string) {
	if el := c.elem[key]; el != nil {
		c.ll.MoveToBack(el)
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
	// --hidden 未开启时与列表一致地拒绝隐藏文件缩略图
	if s.hiddenBlocked(abs) {
		writeErr(w, http.StatusNotFound, "无法生成缩略图")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		writeErr(w, http.StatusNotFound, "无法生成缩略图")
		return
	}

	wq := parseIntSafe(q.Get("w"), 256, thumbMaxDim)
	hq := parseIntSafe(q.Get("h"), 256, thumbMaxDim)

	kind := fileKind(fi.Name(), false)
	switch kind {
	case "image":
		s.serveImageThumb(w, r, abs, fi, wq, hq)
	case "video":
		// 视频缩略图已 100% 改为浏览器抽帧（服务端 ffmpeg 抽帧已废弃）：
		// 返回 404 让旧版前端/直接请求降级为图标。
		writeErr(w, http.StatusNotFound, "视频缩略图由浏览器抽帧生成")
	default:
		writeErr(w, http.StatusNotFound, "该类型不支持缩略图")
	}
}

// ============================================================
// /api/thumb-src：浏览器抽帧专用源。
//
// 背景：Chromium 的 <video preload=metadata> 对源发出开区间 Range
// （bytes=0-），服务端若整段回源，抽一帧缩略图会把整个 500MB~1.4GB 文件
// 传一遍——磁盘/网络/内存全被打爆（用户核心痛点）。
//
// 方案：只返回一个「截短的合法 MP4」：
//   - moov 在头部（含怪封装巨 moov）：返回 [0, 16MB)——moov + 开头 1~2s
//     样本都在其中，浏览器元数据秒读、seek 1s 命中样本，抽帧毫秒级完成；
//   - moov 在尾部（正常录制片）：返回 [0, 16MB) + [moov] 拼接——头部样本区
//     的字节与原文件逐字节一致，moov 内部 stco 偏移表依然有效，拼接后仍是
//     合法 MP4；
//   - 非 MP4 容器/解析失败/小文件：整段返回（带 Range 支持，行为同原文件）。
//
// 无论哪种情况，单次抽帧的传输量上限 16MB，且只读文件头尾（顺序读，快）。
// ============================================================
const thumbSrcLimit = 16 << 20 // 抽帧源头部截取上限 16MB

// layoutEntry MP4 顶层布局缓存条目（moov 尾部扫描要遍历全部 mdat 头，
// 在碎片盘上就是几百上千次寻道，必须缓存）
type layoutEntry struct {
	l   mp4Layout
	t   time.Time
}

// serveThumbSrc GET /api/thumb-src?path=
func (s *Server) serveThumbSrc(w http.ResponseWriter, r *http.Request) {
	abs, err := s.safePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, errToStatus(err), err.Error())
		return
	}
	if s.hiddenBlocked(abs) {
		writeErr(w, http.StatusNotFound, "路径不存在")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		writeErr(w, http.StatusNotFound, "无法访问该文件")
		return
	}
	if fileKind(fi.Name(), false) != "video" {
		writeErr(w, http.StatusNotFound, "该类型不支持抽帧源")
		return
	}
	ext := strings.ToLower(filepath.Ext(fi.Name()))

	// 优先 faststart 缓存文件（moov 前置、无碎片，顺序读最快）；无缓存用原文件
	src := abs
	if fp := s.faststartCachePath(abs, fi); fp != "" {
		src = fp
	}
	f, err := os.Open(src)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "打开文件失败")
		return
	}
	defer f.Close()
	size := fi.Size()
	if st, err := f.Stat(); err == nil {
		size = st.Size()
	}

	// 非 MP4 容器（MKV/AVI/…）无法截取（顶层结构不同）：整段返回
	if ext != ".mp4" && ext != ".m4v" && ext != ".mov" {
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
		return
	}
	if size <= thumbSrcLimit {
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f) // 小文件整段
		return
	}

	layout := s.mp4LayoutCached(src, size)
	switch {
	case layout.moovOffset < 0 || layout.moovSize < 8:
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f) // 解析失败：整段兜底
	case layout.moovOffset+layout.moovSize <= thumbSrcLimit:
		// moov 完全在截取区内（moov 前置片/怪封装片）：头部即 moov + 样本
		serveThumbSrcParts(w, r, f, []part{{0, thumbSrcLimit}})
	case layout.moovOffset >= thumbSrcLimit:
		// moov 在截取区之后（moov 后置片）：头部样本区 + 尾部 moov 拼接
		serveThumbSrcParts(w, r, f, []part{{0, thumbSrcLimit}, {layout.moovOffset, layout.moovSize}})
	default:
		// moov 跨截取边界（超 16MB 的巨 moov）：整段兜底
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
	}
}

type part struct{ off, length int64 }

// serveThumbSrcParts 流式输出若干文件区段拼接成的「虚拟文件」（200 整段响应，
// 不处理 Range——浏览器抽帧元素会缓冲全部 16MB，seek 在缓冲内完成）。
func serveThumbSrcParts(w http.ResponseWriter, r *http.Request, f *os.File, parts []part) {
	var total int64
	for _, p := range parts {
		total += p.length
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	buf := make([]byte, 256<<10)
	for _, p := range parts {
		remaining := p.length
		off := p.off
		for remaining > 0 {
			n := int64(len(buf))
			if remaining < n {
				n = remaining
			}
			rn, err := f.ReadAt(buf[:n], off)
			if rn > 0 {
				w.Write(buf[:rn])
				off += int64(rn)
				remaining -= int64(rn)
			}
			if err != nil {
				return
			}
		}
	}
}

// mp4LayoutCached MP4 顶层布局（带 1 小时缓存：moov 尾部扫描遍历全部 mdat 头，
// 在碎片盘上代价高，同一文件多次抽帧不应重复扫描）
func (s *Server) mp4LayoutCached(src string, size int64) mp4Layout {
	key := fmt.Sprintf("%s|%d", src, size)
	s.layoutMu.Lock()
	defer s.layoutMu.Unlock()
	if e, ok := s.layouts[key]; ok && time.Since(e.t) < time.Hour {
		return e.l
	}
	l := mp4LayoutOf(src)
	if len(s.layouts) >= 256 {
		s.layouts = make(map[string]layoutEntry)
	}
	s.layouts[key] = layoutEntry{l, time.Now()}
	return l
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

	// SVG：浏览器可直接渲染，返回原文件（流式，避免整读进内存）
	if ext == ".svg" {
		s.serveOriginalImage(w, r, abs, fi, "image/svg+xml")
		return
	}

	// 图片解码/整读纳入并发信号量（与 ffmpeg 抽帧对称），
	// 防止多客户端并发请求不同大图导致内存/CPU 不受控
	s.imgSem <- struct{}{}
	defer func() { <-s.imgSem }()

	// 先读尺寸：巨图不解码，直接返回原图（防内存尖峰）
	cfg, _, err := decodeConfig(abs)
	if err != nil {
		writeErr(w, http.StatusNotFound, "无法解码图片")
		return
	}
	if cfg.Width > thumbDirectServeThreshold || cfg.Height > thumbDirectServeThreshold {
		ct := "image/" + strings.TrimPrefix(ext, ".")
		s.serveOriginalImage(w, r, abs, fi, ct)
		return
	}
	// 原图不大时直接原样输出（浏览器缩放足够清晰）
	if cfg.Width <= 512 && cfg.Height <= 512 {
		ct := "image/" + strings.TrimPrefix(ext, ".")
		s.serveOriginalImage(w, r, abs, fi, ct)
		return
	}

	key := thumbKey(abs, fi, wq, hq)
	if data, ok := s.thumbs.Get(key); ok {
		serveCached(w, r, key, data, "image/jpeg")
		return
	}

	// singleflight：同一缩略图的并发请求只生成一次，不同缩略图并发执行
	data, ok := s.thumbs.Do(key, func() ([]byte, bool) {
		if data, ok := s.thumbs.Get(key); ok { // 双检：等待期间可能已被其他请求生成
			return data, true
		}
		src, err := decodeImageFile(abs)
		if err != nil {
			return nil, false
		}
		out := coverCrop(src, wq, hq)
		var enc bytes.Buffer
		if err := jpeg.Encode(&enc, out, &jpeg.Options{Quality: 80}); err != nil {
			return nil, false
		}
		data := enc.Bytes()
		s.thumbs.Put(key, data)
		return data, true
	})
	if !ok {
		writeErr(w, http.StatusNotFound, "无法解码图片")
		return
	}
	serveCached(w, r, key, data, "image/jpeg")
}

// serveOriginalImage 流式返回原图（http.ServeContent 顺带获得 Range/304 支持），
// 替代原先 os.ReadFile 整段读入内存再 w.Write 的写法。
func (s *Server) serveOriginalImage(w http.ResponseWriter, r *http.Request, abs string, fi os.FileInfo, contentType string) {
	f, err := os.Open(abs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取文件失败")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

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
