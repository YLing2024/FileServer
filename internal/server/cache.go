package server

import (
	"sync"
	"time"
)

// ============================================================
// 本文件集中承载 Server 的「缓存 / 并发状态」。
// 每种可变状态把「锁 + 它保护的数据」封在同一个类型里：Server 只持有实例，
// 不再把裸 mutex 与 map 并排摊在结构体上，锁范围也就随类型走、不必回头读全文件。
// ============================================================

// semaphores 服务器并发信号量集合（集中一处，便于核对各并发上限）。
type semaphores struct {
	img     chan struct{} // 图片缩略图解码/整读并发上限
	ffThumb chan struct{} // 服务端 ffmpeg 抽帧并发上限（冷门格式缩略图，低并发防占盘）
}

// listCacheEntry 目录列表服务端缓存（按 目录路径|排序 键控）
type listCacheEntry struct {
	dirMod  time.Time // 目录修改时间（变化即失效）
	fetched time.Time
	entries []Entry // 已过滤隐藏、已排序（懒 stat：仅分页到的条目填 Size/ModTime）
}

const (
	listCacheTTL = 3 * time.Second // 短 TTL：返回/加载更多秒开，且 3 秒内目录变化即反映
	listCacheMax = 64              // 缓存目录数上限
)

// listCache 目录列表短缓存：锁与被保护的数据同属一个类型。
type listCache struct {
	mu    sync.Mutex
	items map[string]*listCacheEntry
}

// get 读缓存（未命中返回 nil）。
func (c *listCache) get(key string) *listCacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.items[key]
}

// put 写缓存（超限整体清空重建，防无界增长）。
func (c *listCache) put(key string, dirMod time.Time, entries []Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[string]*listCacheEntry)
	}
	if len(c.items) >= listCacheMax {
		c.items = make(map[string]*listCacheEntry, listCacheMax)
	}
	// 深拷贝一份避免共享底层数组被后续 fillStats 修改
	cp := append([]Entry(nil), entries...)
	c.items[key] = &listCacheEntry{dirMod: dirMod, fetched: time.Now(), entries: cp}
}

// layoutEntry MP4 顶层布局缓存条目（moov 尾部扫描要遍历全部 mdat 头，
// 在碎片盘上就是几百上千次寻道，必须缓存）
type layoutEntry struct {
	l mp4Layout
	t time.Time
}

const (
	layoutCacheTTL = time.Hour // 同一文件布局缓存 1 小时
	layoutCacheMax = 256       // 缓存条目数上限
)

// layoutCache MP4 顶层布局缓存：锁与被保护的数据同属一个类型。
// 命中判定与计算在同一临界区内完成（计算期间持锁），与原实现一致。
type layoutCache struct {
	mu    sync.Mutex
	items map[string]layoutEntry
}

// getOrCompute 返回 key 的布局：TTL 内命中缓存直接返回，否则调用 compute 并缓存。
func (c *layoutCache) getOrCompute(key string, compute func() mp4Layout) mp4Layout {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok && time.Since(e.t) < layoutCacheTTL {
		return e.l
	}
	l := compute()
	if c.items == nil {
		c.items = make(map[string]layoutEntry)
	}
	if len(c.items) >= layoutCacheMax {
		c.items = make(map[string]layoutEntry)
	}
	c.items[key] = layoutEntry{l, time.Now()}
	return l
}
