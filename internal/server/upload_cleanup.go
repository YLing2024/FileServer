package server

import (
	"os"
	"path/filepath"
	"time"

	"github.com/YLing2024/FileServer/internal/platform"
)

// cleanupUploadStore 删除超过保留期的残缺会话，并回收没有用户链接的 blob。
// 返回 (清理的上传会话数, 回收的 blob 数)。
func (s *Server) cleanupUploadStore(now time.Time) (int, int) {
	removedUploads := 0
	cutoff := now.Add(-uploadRetention)
	des, err := os.ReadDir(s.uploadsRoot())
	if err == nil {
		for _, de := range des {
			if !de.IsDir() {
				continue
			}
			id, ok := normalizeHex64(de.Name())
			if !ok {
				continue
			}
			created := cutoff.Add(uploadRetention)
			if meta, ok := s.readUploadMeta(id); ok {
				created = time.Unix(meta.CreatedAt, 0)
			} else if info, ierr := de.Info(); ierr == nil {
				created = info.ModTime()
			}
			if created.Before(cutoff) {
				if os.RemoveAll(filepath.Join(s.uploadsRoot(), de.Name())) == nil {
					removedUploads++
				}
			}
		}
	}
	return removedUploads, s.pruneOrphanBlobs()
}

// cleanupUploadLoop 每日清理一次；服务关闭时退出。
func (s *Server) cleanupUploadLoop() {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.cleanupUploadStore(time.Now())
		case <-s.cleanupStop:
			return
		}
	}
}

// blobFileSize 返回 blob 的大小；不存在或不是普通文件时返回 false。
func (s *Server) blobFileSize(path string) (int64, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return 0, false
	}
	return fi.Size(), true
}

// linkCountSafe 读取硬链接计数；平台不支持时返回 false。
func linkCountSafe(path string) (uint64, bool) {
	n, err := platform.LinkCount(path)
	if err != nil {
		return 0, false
	}
	return n, true
}
