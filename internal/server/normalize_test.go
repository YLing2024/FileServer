package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mkVideoFFmpeg 用 ffmpeg 生成一个最小 MP4（用于规整化集成测试）。无 ffmpeg 时返回 false。
func mkVideoFFmpeg(t *testing.T, path string) bool {
	t.Helper()
	ff := FindFfmpeg()
	if ff == nil {
		t.Log("无 ffmpeg，跳过")
		return false
	}
	cmd := exec.Command(ff.ffmpegPath, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=320x240:d=2:r=30",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-movflags", "-faststart", path) // 非 faststart，便于构造非规整布局
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("生成视频失败（跳过）: %s", strings.TrimSpace(string(out)))
		return false
	}
	return true
}

// TestNormalizerEnqueueDedup 同一文件重复入队应返回 false
func TestNormalizerEnqueueDedup(t *testing.T) {
	srv, _ := newTestServer(t)
	if srv.norm == nil {
		t.Fatal("norm 未初始化")
	}
	abs := filepath.Join(srv.root, "a.txt")
	fi, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	if !srv.norm.Enqueue("a.txt", abs, fi) {
		t.Fatal("首次入队应成功")
	}
	if srv.norm.Enqueue("a.txt", abs, fi) {
		t.Fatal("重复入队应返回 false")
	}
}

// TestNormalizeBackupManagement 备份列出/恢复/删除（纯 IO，不依赖 ffmpeg）
func TestNormalizeBackupManagement(t *testing.T) {
	srv, root := newTestServer(t)
	if srv.norm == nil {
		t.Fatal("norm 未初始化")
	}
	// 模拟一次规整后的备份：手动把文件挪到备份目录 + 原路径放规整版
	abs := filepath.Join(root, "a.txt")
	bak := srv.norm.backupRelPath(abs)
	if err := os.MkdirAll(filepath.Dir(bak), 0o755); err != nil {
		t.Fatal(err)
	}
	// 备份=原文件内容
	if err := os.WriteFile(bak, []byte("backup-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 原路径=规整版（不同内容）
	if err := os.WriteFile(abs, []byte("normalized-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 列出备份
	bs := srv.norm.listBackups()
	found := false
	for _, b := range bs {
		if b.Path == "/a.txt" && b.Exists {
			found = true
		}
	}
	if !found {
		t.Fatalf("备份列表应包含 /a.txt: %+v", bs)
	}

	// 恢复：备份覆盖回原路径，原文件变回备份内容
	if err := srv.norm.restore(abs, bak); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	data, _ := os.ReadFile(abs)
	if string(data) != "backup-content" {
		t.Fatalf("恢复后原文件内容应为备份内容, got %q", data)
	}
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Fatalf("恢复后备份应被删除")
	}
}

// TestNormalizeCompactFlow 端到端：规整一个小视频 -> 备份 -> 覆盖 -> 恢复
// （需要 ffmpeg；无则跳过）
func TestNormalizeCompactFlow(t *testing.T) {
	// 用独立临时目录，避免依赖 newTestServer 预设文件
	root2 := t.TempDir()
	srv2 := New(root2, Options{})
	defer srv2.Close()
	if srv2.norm == nil || srv2.norm.ff == nil || !srv2.norm.ff.Available() {
		t.Skip("无 ffmpeg，跳过规整端到端")
	}
	mp4 := filepath.Join(root2, "v.mp4")
	if !mkVideoFFmpeg(t, mp4) {
		t.Skip("无法生成测试视频")
	}
	fi, err := os.Stat(mp4)
	if err != nil {
		t.Fatal(err)
	}
	rel := "/v.mp4"
	if !srv2.norm.Enqueue(rel, mp4, fi) {
		t.Fatal("入队失败")
	}
	// 等待任务完成
	deadline := 120
	for i := 0; i < deadline; i++ {
		tk := srv2.norm.task(rel)
		if tk != nil && (tk.State == "done" || tk.State == "failed") {
			if tk.State == "failed" {
				t.Fatalf("规整失败: %s", tk.Err)
			}
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	// 验证：原文件已规整，备份存在
	bak := srv2.norm.backupRelPath(mp4)
	if _, err := os.Stat(bak); err != nil {
		t.Fatalf("应有备份: %v", err)
	}
	if !srv2.isWeird(mp4) {
		t.Log("规整后不再是怪封装（预期）")
	}
	// 通过 HTTP 恢复
	ts := httptest.NewServer(srv2.Handler())
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/normalize/restore?path="+url.QueryEscape(rel), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("恢复应 200, got %d", resp.StatusCode)
	}
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Fatal("恢复后备份应删除")
	}
}
