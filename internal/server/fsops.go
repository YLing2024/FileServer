package server

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// ============================================================
// 文件管理底层操作：递归复制、带元数据覆盖、路径检查与回收站尺寸统计。
// HTTP 语义在 manage.go / trash.go，这里只做文件系统动作，便于单测。
// ============================================================

// copyTree 递归复制 src 到 dst，保留目录结构、权限位与修改时间。
// 目标必须不存在（冲突策略由上层先处理）；符号链接与特殊文件一律拒绝，
// 避免复制过程跟随链接逃出服务根目录。失败时由调用方清理半成品目标。
func copyTree(src, dst string) error {
	type dirMeta struct {
		path  string
		perm  os.FileMode
		mtime time.Time
	}
	dirs := make([]dirMeta, 0, 8)
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("包含符号链接，暂不支持复制")
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := dst
		if rel != "." {
			target = filepath.Join(dst, rel)
		}
		if info.IsDir() {
			// 先建可写目录，复制完子项后再恢复源权限位（只读源目录也能复制）。
			if err := os.Mkdir(target, 0o755); err != nil {
				return err
			}
			dirs = append(dirs, dirMeta{path: target, perm: info.Mode().Perm(), mtime: info.ModTime()})
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("包含不支持的文件类型")
		}
		return copyFileWithMeta(p, target, info)
	})
	if err != nil {
		return err
	}
	// 由深到浅恢复目录时间：先写子项会更新父目录 mtime。
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i].path, dirs[i].perm); err != nil {
			return err
		}
		if err := os.Chtimes(dirs[i].path, dirs[i].mtime, dirs[i].mtime); err != nil {
			return err
		}
	}
	return nil
}

// copyFileWithMeta 复制单个普通文件并保留权限位与修改时间。
func copyFileWithMeta(src, dst string, info os.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = out.Close()
			_ = os.Remove(dst)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	perm := info.Mode().Perm()
	if perm == 0 {
		perm = 0o644
	}
	if err := os.Chmod(dst, perm); err != nil {
		return err
	}
	if err := os.Chtimes(dst, info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	ok = true
	return nil
}

// pathSize 统计文件或目录占用字节数（目录递归；符号链接按链接自身大小计）。
func pathSize(abs string) int64 {
	var total int64
	_ = filepath.WalkDir(abs, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 无权限/已消失的子项跳过，不阻断统计
		}
		if info, ierr := d.Info(); ierr == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// backupTarget 在覆盖写前把已存在的目标改名到同目录隐藏备份，失败可回滚。
// 目标不存在时返回空串。备份名以 . 开头，避免与用户文件混淆。
func backupTarget(target string) (string, error) {
	if _, err := os.Lstat(target); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	backup := filepath.Join(filepath.Dir(target), ".fsbak-"+newRequestID())
	if err := os.Rename(target, backup); err != nil {
		return "", err
	}
	return backup, nil
}

// restoreBackup 覆盖失败时把备份改回目标位置（尽力而为）。
func restoreBackup(backup, target string) {
	if backup == "" {
		return
	}
	_ = os.RemoveAll(target)
	_ = os.Rename(backup, target)
}
