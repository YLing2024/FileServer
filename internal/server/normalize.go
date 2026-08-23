package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fileserver/internal/platform"
)

// ============================================================
// 怪封装规整化（normalize）：
// 用户手动触发，把怪封装 MP4（mdat 碎片化 / moov 过大）用 `-c copy +faststart`
// 重封装为规整 MP4（零画质损失）。覆盖原文件前先把原文件备份到
// .FileServer\backup\<相对路径>；备份可恢复或彻底删除。
//
// 进程/资源保障：
//   - 任务队列单飞、全局并发上限（机械盘保护，绝不拉满）；
//   - ffmpeg 低于正常优先级运行，Job Object 随主进程终止；
//   - 覆盖原文件前先用 os.Rename 把原文件原子移动到备份目录（同卷瞬时、零拷贝），
//     再用 Rename 把规整文件挪回原路径——任何一步失败都能回滚，绝不留半成品。
// ============================================================

// normalizeConc 全局并发规整任务上限：机械盘上多路同时读+写会互相拖慢，==1 单飞
const normalizeConc = 1

// normalizeRetainFinished 内存里保留的最近已完成任务数（前端面板展示）
const normalizeRetainFinished = 50

// normalizeBackupDir 备份相对目录名（位于 .FileServer 下）
const normalizeBackupDir = "backup"

// normalizeTaskState 任务状态
type normalizeTaskState int

const (
	normQueued normalizeTaskState = iota
	normNormalizing
	normVerifying
	normBackingUp
	normDone
	normFailed
)

func (s normalizeTaskState) String() string {
	switch s {
	case normQueued:
		return "queued"
	case normNormalizing:
		return "normalizing"
	case normVerifying:
		return "verifying"
	case normBackingUp:
		return "backing_up"
	case normDone:
		return "done"
	case normFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// NormalizeTask 一个规整化任务（含对前端可见的状态）
type NormalizeTask struct {
	Path    string  `json:"path"`    // 相对根目录路径（/ 开头）
	Name    string  `json:"name"`    // 文件名（去目录）
	Size    int64   `json:"size"`    // 源文件大小
	State   string  `json:"state"`   // 见 NormalizeTaskState.String()
	Err     string  `json:"err,omitempty"`
	Percent float64 `json:"percent"` // 0~100 进度（按输出文件大小 / 源大小估算）
}

// BackupEntry 一个备份条目（前端「备份管理」展示）
type BackupEntry struct {
	Path    string `json:"path"`    // 备份对应的原视频相对路径
	Name    string `json:"name"`    // 文件名
	Size    int64  `json:"size"`    // 备份大小
	BackupT int64  `json:"mtime"`   // 备份时间（Unix 秒）
	Exists  bool   `json:"exists"`  // 原视频当前是否存在（存在=可恢复覆盖）
}

// Normalizer 规整化管理器
type Normalizer struct {
	root      string
	backupDir string
	tmpDir    string
	ff        *Ffmpeg

	mu     sync.Mutex
	queue  []string            // FIFO：待处理的任务 path（去重：同 path 只入队一次）
	tasks  map[string]*NormalizeTask // path -> 任务
	order  []string            // 全部任务顺序（含已完成，保留最近若干）
	cancel map[string]context.CancelFunc // path -> 取消函数（进行中任务）
	sem    chan struct{}       // 并发槽（normalizeConc）
}

// NewNormalizer 创建规整化管理器。备份/临时目录都在 .FileServer 隐藏目录下。
func NewNormalizer(root, base string, ff *Ffmpeg) *Normalizer {
	backupDir := filepath.Join(base, normalizeBackupDir)
	tmpDir := filepath.Join(base, "tmp-normalize")
	os.MkdirAll(backupDir, 0o755)
	os.MkdirAll(tmpDir, 0o755)
	return &Normalizer{
		root:      root,
		backupDir: backupDir,
		tmpDir:    tmpDir,
		ff:        ff,
		tasks:     make(map[string]*NormalizeTask),
		order:     make([]string, 0),
		cancel:    make(map[string]context.CancelFunc),
		sem:       make(chan struct{}, normalizeConc),
	}
}

// BackupPath 返回 path（相对）对应的备份目标绝对路径（镜像相对目录结构）
func (n *Normalizer) BackupPath(rel string) string {
	clean := strings.TrimPrefix(filepath.ToSlash(rel), "/")
	return filepath.Join(n.backupDir, filepath.FromSlash(clean))
}

// backupRelPath 返回备份文件相对 backupDir 的路径（去掉共享根与文件名重名防冲突）
func (n *Normalizer) backupRelPath(abs string) string {
	return n.BackupPath(relOf(n.root, abs))
}

// backupExists 判断该文件是否已有备份
func (n *Normalizer) backupExists(abs string) bool {
	_, err := os.Stat(n.backupRelPath(abs))
	return err == nil
}

// ---- HTTP 处理 ----

// handleWeirdList GET /api/weird?path= 返回目录下所有怪封装视频的相对路径。
// 并发 + size 预过滤：isWeird 内部已对 <256MB 直接跳过（不读头），这里再限制
// 并发读头（机械盘上串行读头慢），缩短首次扫描耗时。布局结果带 1 小时缓存。
func (s *Server) handleWeirdList(w http.ResponseWriter, r *http.Request) {
	abs, err := s.safePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, errToStatus(err), err.Error())
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "不是目录")
		return
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取目录失败")
		return
	}
	// 收集候选：mp4/m4v/mov 且 ≥256MB（isWeird 同样以 size 预过滤）
	var cands []string
	for _, de := range des {
		if de.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(de.Name()))
		if ext != ".mp4" && ext != ".m4v" && ext != ".mov" {
			continue
		}
		if info, ierr := de.Info(); ierr == nil && info.Size() >= 256<<20 {
			cands = append(cands, filepath.Join(abs, de.Name()))
		}
	}
	// 串行判定：机械盘上并发读会互相抢磁头（实测 4 路反而更慢），
	// 单路顺序读 + 流式提前停（数到 >4 块即返回）是最快路径。
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		if s.isWeirdStream(c) {
			out = append(out, relOf(s.root, c))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"weird": out})
}

// handleNormalizeStart POST /api/normalize?path= 把怪封装文件加入规整队列
func (s *Server) handleNormalizeStart(w http.ResponseWriter, r *http.Request) {
	if s.norm == nil || s.norm.ff == nil || !s.norm.ff.Available() {
		writeErr(w, http.StatusBadGateway, "服务端无 ffmpeg，无法规整")
		return
	}
	abs, err := s.safePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, errToStatus(err), err.Error())
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		writeErr(w, http.StatusNotFound, "路径不存在")
		return
	}
	if !s.isWeird(abs) {
		// 已规整/非怪封装：不重复规整
		writeErr(w, http.StatusConflict, "该文件不是怪封装，无需规整")
		return
	}
	rel := relOf(s.root, abs)
	if s.norm.Enqueue(rel, abs, fi) {
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "path": rel})
	} else {
		writeErr(w, http.StatusConflict, "该文件已在队列中或正在规整")
	}
}

// Enqueue 把文件加入规整队列。重复入队返回 false。
func (n *Normalizer) Enqueue(rel, abs string, fi os.FileInfo) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.tasks[rel]; ok {
		return false // 已在队列/已存在（含完成——完成后再次访问需重新入队？此处禁止重复）
	}
	t := &NormalizeTask{Path: rel, Name: filepath.Base(rel), Size: fi.Size(), State: normQueued.String()}
	n.tasks[rel] = t
	n.queue = append(n.queue, rel)
	n.order = append(n.order, rel)
	n.trimFinishedLocked()
	// 启动消费（有槽位则立即跑，否则阻塞在 sem——由 worker 循环接手）
	go n.pump()
	return true
}

// pump 消费队列：在 sem 槽位内按 FIFO 处理下一个任务
func (n *Normalizer) pump() {
	n.mu.Lock()
	for len(n.queue) > 0 {
		// 找第一个还在队列（未开始）的任务
		next := n.queue[0]
		n.queue = n.queue[1:]
		// 已在执行/已完成则跳过
		if t, ok := n.tasks[next]; ok && t.State == normQueued.String() {
			n.mu.Unlock()
			n.runOne(next, t)
			n.mu.Lock()
		}
	}
	n.mu.Unlock()
}

// runOne 在 sem 槽位内执行单个任务（阻塞直到完成）
func (n *Normalizer) runOne(rel string, t *NormalizeTask) {
	n.sem <- struct{}{}
	defer func() { <-n.sem }()

	abs := filepath.Join(n.root, filepath.FromSlash(strings.TrimPrefix(rel, "/")))
	ctx, cancel := context.WithCancel(context.Background())
	n.mu.Lock()
	n.cancel[rel] = cancel
	n.mu.Unlock()
	defer func() { n.mu.Lock(); delete(n.cancel, rel); cancel(); n.mu.Unlock() }()

	n.setState(rel, normNormalizing)
	if err := n.normalize(ctx, abs, rel, t); err != nil {
		n.setState(rel, normFailed)
		n.setErr(rel, err.Error())
		return
	}
	// 全部完成（含备份落盘）才置 100% 与 done：
	// 此前进度最高 90%（剩余 10% 属于 ffmpeg faststart 重排 + 校验 + 备份 rename）。
	n.setPercent(rel, 100)
	n.setState(rel, normDone)
}

func (n *Normalizer) setState(rel string, st normalizeTaskState) {
	n.mu.Lock()
	if t, ok := n.tasks[rel]; ok {
		t.State = st.String()
	}
	n.mu.Unlock()
}

func (n *Normalizer) setErr(rel, msg string) {
	n.mu.Lock()
	if t, ok := n.tasks[rel]; ok {
		t.Err = msg
	}
	n.mu.Unlock()
}

func (n *Normalizer) setPercent(rel string, p float64) {
	n.mu.Lock()
	if t, ok := n.tasks[rel]; ok {
		t.Percent = p
	}
	n.mu.Unlock()
}

// normalize 执行规整：ffmpeg -c copy +faststart 到临时文件 -> 校验 -> 备份原文件 -> 覆盖
func (n *Normalizer) normalize(ctx context.Context, abs, rel string, t *NormalizeTask) error {
	// 1) 规整到临时文件（零转码，只重组 container）
	tmp := filepath.Join(n.tmpDir, fmt.Sprintf("norm-%d.tmp", time.Now().UnixNano()))
	defer os.Remove(tmp)
	if err := n.runFFmpegNormalize(ctx, abs, tmp, t); err != nil {
		return fmt.Errorf("规整失败: %v", err)
	}
	// 2) 校验规整结果可读（ffprobe 能取到时长）
	n.setPercent(rel, 93)
	n.setState(rel, normVerifying)
	if err := n.verify(tmp); err != nil {
		return fmt.Errorf("规整结果校验失败: %v", err)
	}
	// 3) 备份原文件（原子 rename 到备份目录，同卷瞬时）
	n.setPercent(rel, 96)
	n.setState(rel, normBackingUp)
	bak := n.backupRelPath(abs)
	if err := os.MkdirAll(filepath.Dir(bak), 0o755); err != nil {
		return fmt.Errorf("创建备份目录失败: %v", err)
	}
	// 若已有备份（异常残留），先删除旧备份再移动，确保备份即当前原文件
	os.Remove(bak)
	if err := os.Rename(abs, bak); err != nil {
		return fmt.Errorf("备份原文件失败: %v", err)
	}
	// 4) 覆盖原路径（瞬时 rename；若失败回滚备份）
	n.setPercent(rel, 99)
	if err := os.Rename(tmp, abs); err != nil {
		// 回滚：把备份放回原位，避免文件丢失
		os.Rename(bak, abs)
		return fmt.Errorf("覆盖原文件失败: %v", err)
	}
	return nil
}

// runFFmpegNormalize 低优先级运行 ffmpeg -c copy +faststart，并周期性更新进度
func (n *Normalizer) runFFmpegNormalize(ctx context.Context, src, dst string, t *NormalizeTask) error {
	size := t.Size
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-analyzeduration", "0", "-probesize", "32",
		"-i", src,
		"-map", "0",
		"-c", "copy",
		"-movflags", "+faststart",
		"-f", "mp4", dst,
	}
	cmd := exec.CommandContext(ctx, n.ff.ffmpegPath, args...)
	cmd.Dir = filepath.Dir(dst)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	platform.KillOnParentExit(cmd)
	// 进度看门狗：每 500ms 更新进度。
	// 关键：怪封装文件（大 moov + 碎片 mdat）的 ffmpeg 扫描阶段（读 moov 建索引）
	// 可能占十几秒，期间输出文件大小为 0——若只按输出大小算，进度会长时间卡 0%。
	// 因此取「时间进度」与「输出大小进度」的较大值：
	//   - 时间进度 = 已运行时间 / 预估总时长（源大小按机械盘 ~45MB/s 估算），
	//     扫描阶段平滑爬升，用户能看到任务在推进；
	//   - 输出大小进度 = 输出文件大小 / 源大小，写入阶段接管（更贴近真实）。
	// 上限 90%：剩余 10% 留给 ffmpeg 收尾（faststart 重排）与后续校验/备份/覆盖。
	start := time.Now()
	// 估算总时长：-c copy 写入一遍 + +faststart 重排（moov 前置要整体调整
	// stco 偏移、移动数据块 ≈ 再读写一遍全文件）≈ 2.5 遍全文件 IO。
	// 若不包含重排时间，写入完成后时间进度提前到顶，重排阶段会卡在 90% 很久
	// （实测 794MB 文件写入 18s 达 90%，重排又跑了 36s）。
	estTotal := time.Duration(float64(size) / 45e6 * 2.5 * float64(time.Second))
	if estTotal < 5*time.Second {
		estTotal = 5 * time.Second
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				var sizePct float64
				if fi, err := os.Stat(dst); err == nil && size > 0 {
					sizePct = float64(fi.Size()) / float64(size) * 100
				}
				timePct := float64(time.Since(start)) / float64(estTotal) * 100
				p := timePct
				if sizePct > p {
					p = sizePct
				}
				if p > 90 {
					p = 90
				}
				n.setPercent(t.Path, p)
			}
		}
	}()
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-ctx.Done():
		select {
		case waitErr = <-waitCh:
		case <-time.After(5 * time.Second):
			if cmd.Process != nil {
				exec.Command("taskkill", "/F", "/T", "/PID", strconvItoa(cmd.Process.Pid)).Run()
			}
			waitErr = <-waitCh
		}
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("已取消")
		}
		return fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

// verify 校验规整产物可读（ffprobe duration>0）
func (n *Normalizer) verify(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := n.ff.probeDurationUncached(ctx, path)
	return err
}

// ---- 状态/备份查询 ----

// Snapshot 返回任务列表（最新在前）。只返回进行中/排队的任务——
// 完成/失败的任务视为结束，从队列中消失（其成果体现在备份列表/原文件）。
func (n *Normalizer) Snapshot() map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	tasks := make([]NormalizeTask, 0)
	keep := make([]string, 0, len(n.order))
	for i := len(n.order) - 1; i >= 0; i-- {
		t, ok := n.tasks[n.order[i]]
		if !ok {
			continue
		}
		switch t.State {
		case "queued", "normalizing", "verifying", "backing_up":
			tasks = append(tasks, *t)
			keep = append(keep, n.order[i])
		default:
			// 完成/失败：从队列中移除（备份列表/文件系统才是它们的归宿）
			delete(n.tasks, n.order[i])
		}
	}
	n.order = keep
	return map[string]any{"tasks": tasks}
}

// task 取某任务（供前端轮询单个）
func (n *Normalizer) task(rel string) *NormalizeTask {
	n.mu.Lock()
	defer n.mu.Unlock()
	if t, ok := n.tasks[rel]; ok {
		cp := *t
		return &cp
	}
	return nil
}

// trimFinishedLocked 精简 order（保留最近 normalizeRetainFinished 个已完成/失败任务）
func (n *Normalizer) trimFinishedLocked() {
	// 保留全部进行中的；已完成/失败仅保留最近 N 个
	active := 0
	for _, rel := range n.order {
		if t, ok := n.tasks[rel]; ok && (t.State == normQueued.String() || t.State == normNormalizing.String() || t.State == normVerifying.String() || t.State == normBackingUp.String()) {
			active++
		}
	}
	if active >= normalizeRetainFinished {
		return
	}
	// 从旧到新删除已完成/失败，直到总长合适
	count := active
	keep := make([]string, 0, len(n.order))
	for _, rel := range n.order {
		t, ok := n.tasks[rel]
		if !ok {
			continue
		}
		if t.State == normDone.String() || t.State == normFailed.String() {
			if count >= normalizeRetainFinished {
				delete(n.tasks, rel)
				continue
			}
			count++
		}
		keep = append(keep, rel)
	}
	n.order = keep
}

// ---- HTTP：备份管理 ----

// handleNormalizeStatus GET /api/normalize/status 任务队列快照
func (s *Server) handleNormalizeStatus(w http.ResponseWriter, r *http.Request) {
	if s.norm == nil {
		writeJSON(w, http.StatusOK, map[string]any{"tasks": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, s.norm.Snapshot())
}

// handleBackupList GET /api/normalize/backups 列出全部备份
func (s *Server) handleBackupList(w http.ResponseWriter, r *http.Request) {
	if s.norm == nil {
		writeJSON(w, http.StatusOK, map[string]any{"backups": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": s.norm.listBackups()})
}

// listBackups 扫描备份目录，映射回原路径
func (n *Normalizer) listBackups() []BackupEntry {
	var out []BackupEntry
	filepath.WalkDir(n.backupDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(n.backupDir, p)
		if rel == "." {
			return nil
		}
		// 备份内路径 -> 原视频相对根路径
		origRel := "/" + filepath.ToSlash(rel)
		fi, _ := d.Info()
		// 原视频是否仍存在（存在=可恢复覆盖；不存在=原文件已丢失/或本就在备份状态）
		origAbs := filepath.Join(n.root, filepath.FromSlash(strings.TrimPrefix(origRel, "/")))
		_, statErr := os.Stat(origAbs)
		out = append(out, BackupEntry{
			Path:    origRel,
			Name:    filepath.Base(origRel),
			Size:    fi.Size(),
			BackupT: fi.ModTime().Unix(),
			Exists:  statErr == nil,
		})
		return nil
	})
	// 按备份时间倒序
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].BackupT > out[i].BackupT {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// handleRestore POST /api/normalize/restore?path= 用备份恢复原文件（覆盖回原路径）
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	abs, err := s.safePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, errToStatus(err), err.Error())
		return
	}
	if s.norm == nil {
		writeErr(w, http.StatusBadGateway, "规整化不可用")
		return
	}
	bak := s.norm.backupRelPath(abs)
	if _, err := os.Stat(bak); err != nil {
		writeErr(w, http.StatusNotFound, "没有该文件的备份")
		return
	}
	// 恢复 = 用备份覆盖回原路径（当前可能是规整版）。原文件若存在先挪到临时再清理。
	if err := s.norm.restore(abs, bak); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// restore 用备份覆盖回原路径；成功后删除原规整版、清布局缓存。
func (n *Normalizer) restore(abs, bak string) error {
	// 若当前原文件存在，先挪到临时文件，再覆盖，最后删掉临时（避免直接覆盖失败丢数据）
	tmpDel := filepath.Join(n.tmpDir, fmt.Sprintf("restore-del-%d.tmp", time.Now().UnixNano()))
	origExists := false
	if _, err := os.Stat(abs); err == nil {
		origExists = true
		if err := os.Rename(abs, tmpDel); err != nil {
			return fmt.Errorf("暂存原文件失败: %v", err)
		}
	}
	if err := os.Rename(bak, abs); err != nil {
		// 回滚：把暂存放回
		if origExists {
			os.Rename(tmpDel, abs)
		}
		return fmt.Errorf("恢复备份失败: %v", err)
	}
	if origExists {
		os.Remove(tmpDel)
	}
	return nil
}

// handleBackupDelete POST /api/normalize/delete-backup?path= 彻底删除备份
func (s *Server) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	abs, err := s.safePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, errToStatus(err), err.Error())
		return
	}
	if s.norm == nil {
		writeErr(w, http.StatusBadGateway, "规整化不可用")
		return
	}
	bak := s.norm.backupRelPath(abs)
	if err := os.Remove(bak); err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, "没有该文件的备份")
			return
		}
		writeErr(w, http.StatusInternalServerError, "删除备份失败: "+err.Error())
		return
	}
	// 清理空父目录
	s.norm.pruneEmptyDirs(filepath.Dir(bak))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// pruneEmptyDirs 向上删除空的备份父目录（直到 backupDir 根）
func (n *Normalizer) pruneEmptyDirs(dir string) {
	for strings.HasPrefix(dir, n.backupDir) && dir != n.backupDir {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		os.Remove(dir)
		dir = filepath.Dir(dir)
	}
}
