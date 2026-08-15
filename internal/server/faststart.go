package server

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// fastStartMaxAge 缓存文件最长保留时长；超时由清理协程删除
const fastStartMaxAge = 7 * 24 * time.Hour

// FastStartCache 非 faststart MP4 的重封装缓存：
// 把「moov 缺失/在尾部」的视频先用 ffmpeg -c copy 重封装为分片 MP4
// （moov 前置、可即时起播），缓存到磁盘后统一按文件服务。
// 关键作用：让初始请求与后续 seek 的 Range 请求共用同一份字节布局，
// 消除「流式 remux 输出 + 原始文件 Range」两种表示混用导致的 seek 错位。
type FastStartCache struct {
	dir    string
	mu     sync.Mutex
	builds map[string]*fastStartBuild // 构建单飞（key -> 进行中/完成的构建）
}

type fastStartBuild struct {
	done chan struct{}
	err  error
}

func NewFastStartCache() *FastStartCache {
	dir := os.Getenv("LOCALAPPDATA")
	if dir == "" {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "FileServer", "faststart")
	os.MkdirAll(dir, 0o755)
	c := &FastStartCache{dir: dir, builds: make(map[string]*fastStartBuild)}
	go c.cleanupLoop()
	return c
}

// cachePath 返回 key 对应的最终缓存文件路径（与构建结果一一对应）
func (c *FastStartCache) cachePath(key string) string {
	return filepath.Join(c.dir, key+".mp4")
}

// Do 以 singleflight 方式构建 key 对应的 faststart 文件：
// 同一视频的并发请求只有一个执行 build，其余等待完成后复用。
// 返回缓存文件路径与错误。
func (c *FastStartCache) Do(key string, build func() error) (string, error) {
	c.mu.Lock()
	if c.builds == nil {
		c.builds = make(map[string]*fastStartBuild)
	}
	if b, ok := c.builds[key]; ok {
		c.mu.Unlock()
		<-b.done
		if b.err != nil {
			return "", b.err
		}
		return c.cachePath(key), nil
	}
	b := &fastStartBuild{done: make(chan struct{})}
	c.builds[key] = b
	c.mu.Unlock()

	b.err = build()
	if b.err != nil {
		os.Remove(c.cachePath(key))
	}
	c.mu.Lock()
	delete(c.builds, key)
	c.mu.Unlock()
	close(b.done)
	return c.cachePath(key), b.err
}

// cleanupLoop 定期清理过期缓存（含构建失败残留的 .tmp）
func (c *FastStartCache) cleanupLoop() {
	for {
		time.Sleep(fastStartMaxAge / 24) // 每 7h 一次
		c.cleanup()
	}
}

// cleanup 删除超过 fastStartMaxAge 的缓存文件与残留 .tmp
func (c *FastStartCache) cleanup() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-fastStartMaxAge)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".mp4") && !strings.HasSuffix(name, ".tmp") {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(c.dir, name))
		}
	}
}

// fastStartKey 缓存键：真实路径|大小|修改时间 的 SHA1
func fastStartKey(abs string, fi os.FileInfo) string {
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", abs, fi.Size(), fi.ModTime().UnixNano())))
	return hex.EncodeToString(h[:])
}

// fastStartVideo 返回 abs 对应的 faststart 缓存文件路径；不存在则构建。
func (s *Server) fastStartVideo(abs string, fi os.FileInfo) (string, error) {
	key := fastStartKey(abs, fi)
	return s.fastStart.Do(key, func() error {
		// 已构建则直接复用（并发竞争时另一请求刚完成）
		if info, err := os.Stat(s.fastStart.cachePath(key)); err == nil && info.Size() > 0 {
			return nil
		}
		tmp, err := os.CreateTemp(s.fastStart.dir, key+"-*.tmp")
		if err != nil {
			return err
		}
		tmpPath := tmp.Name()
		// 构建不依赖客户端连接：用独立 context，仅受「无写入进度超时」约束
		if err := s.ff.RemuxFastStart(nil, abs, tmp); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return err
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpPath)
			return err
		}
		if err := os.Rename(tmpPath, s.fastStart.cachePath(key)); err != nil {
			os.Remove(tmpPath)
			return err
		}
		return nil
	})
}
