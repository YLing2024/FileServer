package server

import (
	"archive/zip"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleZip GET /api/zip?path= 将目录流式打包为 zip 下载
func (s *Server) handleZip(w http.ResponseWriter, r *http.Request) {
	dir, err := s.safePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, errToStatus(err), err.Error())
		return
	}
	// --hidden 未开启时，隐藏目录（含隐藏祖先）与列表/搜索一致地拒绝打包
	if s.hiddenBlocked(dir) {
		writeErr(w, http.StatusNotFound, "路径不存在")
		return
	}
	fi, err := os.Stat(dir)
	if err != nil {
		writeErr(w, http.StatusNotFound, "路径不存在")
		return
	}
	if !fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "仅支持打包目录")
		return
	}

	name := filepath.Base(dir)
	w.Header().Set("Content-Type", "application/zip")
	if cd := mime.FormatMediaType("attachment", map[string]string{"filename": sanitizeFilename(name) + ".zip"}); cd != "" {
		w.Header().Set("Content-Disposition", cd)
	}
	w.Header().Set("X-Accel-Buffering", "no")

	zw := zip.NewWriter(w)
	defer zw.Close()

	top := filepath.ToSlash(name)
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		// 保留缓存目录（.FileServer）任何情况不打包；其余点开头条目仅 --hidden
		// 时打包，与列表/搜索/直链下载语义一致
		if isCacheEntry(d.Name()) || (!s.hidden && strings.HasPrefix(d.Name(), ".")) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		zipName := top + "/" + filepath.ToSlash(rel)

		if d.IsDir() {
			_, cerr := zw.Create(zipName + "/")
			return cerr
		}
		// 跳过符号链接（不跟随，防环与逃逸）
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		// Store 模式：局域网媒体文件已压缩，避免无谓的 CPU 开销
		hdr := &zip.FileHeader{Name: zipName, Method: zip.Store}
		hdr.SetMode(info.Mode())
		hdr.SetModTime(info.ModTime())
		fw, cerr := zw.CreateHeader(hdr)
		if cerr != nil {
			return cerr
		}
		// 在闭包内打开/关闭，避免大目录下句柄泄漏
		if err := func() error {
			f, oerr := os.Open(p)
			if oerr != nil {
				return oerr
			}
			defer f.Close()
			_, cerr := io.Copy(fw, f)
			return cerr
		}(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		// 响应头已发出，只能截断；客户端会收到损坏的 zip（只读源下极少发生）
		return
	}
}
