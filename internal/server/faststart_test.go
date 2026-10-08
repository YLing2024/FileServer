package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestWarmFaststartTriggered 断言 /api/video-info 会在「直链 + MP4 + moov 在尾部
// （尚未 faststart）」时触发后台 faststart 预热；已 faststart（moov 在头部）的文件
// 不触发。用注入钩子断言触发点，不依赖真实 ffmpeg。
//
// 预热本身的单飞防重与「播放中让路」在 warmFaststart 内实现，不在此覆盖。
func TestWarmFaststartTriggered(t *testing.T) {
	root := t.TempDir()

	// moov 在尾部：ftyp + free(300KB) + moov，moov 偏移 >256KB → 判定未 faststart。
	tailPath := filepath.Join(root, "tail.mp4")
	tail := append(mp4Box("ftyp", make([]byte, 8)), mp4Box("free", make([]byte, 300*1024))...)
	tail = append(tail, mp4Box("moov", make([]byte, 16))...)
	if err := os.WriteFile(tailPath, tail, 0o644); err != nil {
		t.Fatal(err)
	}

	// moov 在头部（>1KB 才走布局解析）→ 已 faststart，不应触发。
	headPath := filepath.Join(root, "head.mp4")
	head := append(mp4Box("ftyp", make([]byte, 8)), mp4Box("moov", make([]byte, 2000))...)
	if err := os.WriteFile(headPath, head, 0o644); err != nil {
		t.Fatal(err)
	}

	srv := New(root, Options{})
	defer srv.Close()
	// 假 ffmpeg（保证触发判定通过）+ 钩子（拦截真实重封装）。infos 非 nil 避免后台探测。
	srv.ff = &Ffmpeg{infos: map[string]*MediaInfo{}}
	var got []string
	srv.faststart.hook = func(abs string, fi os.FileInfo) { got = append(got, abs) }

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := get(t, ts.URL+"/api/video-info?path=/head.mp4")
	if resp.StatusCode != 200 {
		t.Fatalf("head.mp4 状态码 = %d", resp.StatusCode)
	}
	resp.Body.Close()
	// 钩子同步调用：响应返回即已执行完。
	if len(got) != 0 {
		t.Fatalf("已 faststart 的文件不应触发预热，got=%v", got)
	}

	resp2 := get(t, ts.URL+"/api/video-info?path=/tail.mp4")
	if resp2.StatusCode != 200 {
		t.Fatalf("tail.mp4 状态码 = %d", resp2.StatusCode)
	}
	resp2.Body.Close()
	if len(got) != 1 {
		t.Fatalf("moov 在尾部应触发一次预热，got=%v", got)
	}
	if got[0] != tailPath {
		t.Errorf("触发预热的文件 = %q, want %q", got[0], tailPath)
	}
}
