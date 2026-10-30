package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ============================================================
// 秒传（内容寻址去重）。
//
// 索引位于 .FileServer/blobs/<sha256 前两位>/<sha256>，与用户文件是硬链接关系：
// 上传完成后把目标文件另链到索引，命中秒传时再从索引链到目标位置。删除用户
// 文件只是少一个链接；索引自身也是链接，所以当硬链接计数降到 1（只剩索引）时
// 内容已无任何用户文件引用，清理时回收该 blob。跨设备/不支持硬链接时回退复制。
// ============================================================

// blobsRoot 内容索引根目录。
func (s *Server) blobsRoot() string {
	return filepath.Join(s.cfg.root, cacheDirName, "blobs")
}

// blobPath 返回 sha256 对应的 blob 路径；调用方必须先规范化/校验 sha。
func (s *Server) blobPath(sha string) string {
	return filepath.Join(s.blobsRoot(), sha[:2], sha)
}

// registerBlobFile 把已落位的文件登记进内容索引（硬链接；失败回退复制）。
// 索引已存在时直接复用，不覆盖已有 blob。
func (s *Server) registerBlobFile(path, sha string) error {
	sha, ok := normalizeHex64(sha)
	if !ok {
		return os.ErrInvalid
	}
	blob := s.blobPath(sha)
	if _, exists := s.blobFileSize(blob); exists {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
		return err
	}
	if err := os.Link(path, blob); err == nil {
		return nil
	} else if os.IsExist(err) {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(blob), ".blob-"+newRequestID())
	if err := copyFileWithMeta(path, tmp, fi); err != nil {
		return err
	}
	if err := os.Rename(tmp, blob); err != nil {
		_ = os.Remove(tmp)
		if _, exists := s.blobFileSize(blob); exists {
			return nil
		}
		return err
	}
	return nil
}

// materializeBlob 把 blob 落位到目标路径：优先硬链接，跨设备回退复制；
// 先写到同目录临时名再 rename，避免暴露半截文件。
func (s *Server) materializeBlob(sha, target string) error {
	blob := s.blobPath(sha)
	tmp := filepath.Join(filepath.Dir(target), ".upload-"+newRequestID())
	if err := os.Link(blob, tmp); err != nil {
		fi, statErr := os.Stat(blob)
		if statErr != nil {
			return statErr
		}
		if err := copyFileWithMeta(blob, tmp, fi); err != nil {
			return err
		}
	}
	if err := commitUploadTemp(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// instantReq POST /api/upload/instant 请求体。
type instantReq struct {
	SHA256 string `json:"sha256"`
	Name   string `json:"name"`
	Dir    string `json:"dir"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"`
}

// handleUploadInstant POST /api/upload/instant：命中已有内容时直接落位（不计上传流量）。
func (s *Server) handleUploadInstant(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.uploadAllowed {
		httpError(w, http.StatusForbidden, "上传未启用")
		return
	}
	var req instantReq
	if !decodeManageBody(w, r, &req) {
		return
	}
	sha, ok := normalizeHex64(req.SHA256)
	if !ok {
		httpError(w, http.StatusBadRequest, "sha256 不合法")
		return
	}
	if msg := validUploadName(req.Name); msg != "" {
		httpError(w, http.StatusBadRequest, msg)
		return
	}
	if req.Size < 0 || req.Size > s.uploadRequestMax() {
		httpError(w, http.StatusBadRequest, "文件大小不合法")
		return
	}
	mode := req.Mode
	if mode == "" {
		mode = uploadModeAsk
	}
	if mode != uploadModeAsk && mode != uploadModeOverwrite && mode != uploadModeRename {
		httpError(w, http.StatusBadRequest, "mode 非法")
		return
	}
	dirAbs, status, msg := s.uploadTargetDir(req.Dir)
	if status != 0 {
		httpError(w, status, msg)
		return
	}
	blobSize, hit := s.blobFileSize(s.blobPath(sha))
	if !hit || (req.Size > 0 && blobSize != req.Size) {
		writeJSON(w, http.StatusOK, map[string]any{"instant": false})
		return
	}
	items := []*uploadItem{{name: req.Name, size: blobSize}}
	targets, conflict := planUploadTargets(dirAbs, items, mode)
	if conflict != nil {
		writeJSON(w, conflict.status, map[string]any{
			"error":    conflict.message,
			"code":     conflict.code,
			"existing": conflict.existing,
		})
		return
	}
	target := targets[0]
	if !pathWithin(s.cfg.root, target) {
		httpError(w, http.StatusForbidden, "路径访问被拒绝")
		return
	}
	oldInfo, oldErr := os.Stat(target)
	if err := s.materializeBlob(sha, target); err != nil {
		httpError(w, statusForWriteError(err), "创建文件失败")
		return
	}
	if oldErr == nil && oldInfo.Mode().IsRegular() {
		s.pruneOrphanBlobs()
	}
	s.invalidateTextCaches()
	fi, err := os.Stat(target)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "创建后无法读取文件状态")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instant": true,
		"uploaded": []uploadedFile{{
			Name:  filepath.Base(target),
			Size:  fi.Size(),
			MTime: fi.ModTime().Unix(),
		}},
	})
}

// hashFileSHA256 计算已落位文件的 SHA-256；用于确认前端上报哈希与内容一致。
func hashFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// pruneOrphanBlobs 扫描内容索引，删除只剩索引自身链接的 blob。
// 返回回收数量。任何平台无法读取链接计数时保守跳过，不误删。
func (s *Server) pruneOrphanBlobs() int {
	removed := 0
	prefixes, err := os.ReadDir(s.blobsRoot())
	if err != nil {
		return 0
	}
	for _, p := range prefixes {
		if !p.IsDir() {
			continue
		}
		dir := filepath.Join(s.blobsRoot(), p.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			path := filepath.Join(dir, f.Name())
			sha, ok := normalizeHex64(f.Name())
			if !ok || sha != f.Name() {
				// 崩溃遗留的 .blob-* 临时链接：超过保留期再清，避免误删正在登记的索引。
				if info, ierr := f.Info(); ierr == nil && time.Since(info.ModTime()) > uploadRetention {
					_ = os.Remove(path)
				}
				continue
			}
			n, ok := linkCountSafe(path)
			if !ok || n > 1 {
				continue
			}
			if os.Remove(path) == nil {
				removed++
			}
		}
		_ = os.Remove(dir) // 仅当目录已空时成功
	}
	if removed > 0 {
		slog.Info("回收无引用内容索引", "count", removed)
	}
	return removed
}
