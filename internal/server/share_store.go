package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ============================================================
// 分享记录的持久化：.FileServer/shares.json（0600，原子写）。
//
// 只持久化服务端生成的数据：token 是能力凭据（本身不含路径），提取码只存
// sha256(token + code)，不落明文；下载次数在每次下载前于锁内递增并落盘，
// 这样进程重启后额度仍然有效。
// ============================================================

const (
	shareFileName = "shares.json"

	// shareTokenBytes 16 字节随机数：base32 后 26 个字符，不可枚举。
	shareTokenBytes = 16

	shareDefaultNote = "共享文件"

	// 分享状态（由记录字段与当前时间计算，不落盘，避免状态老化）。
	shareStatusActive    = "active"
	shareStatusExpired   = "expired"
	shareStatusRevoked   = "revoked"
	shareStatusExhausted = "exhausted"
)

var (
	errShareNotFound  = errors.New("分享不存在")
	errShareExpired   = errors.New("分享已过期")
	errShareRevoked   = errors.New("分享已撤销")
	errShareExhausted = errors.New("分享下载次数已用完")
)

// shareRecord 一条分享记录。
type shareRecord struct {
	Token        string `json:"token"`
	Path         string `json:"path"` // 相对服务根目录，始终以 / 开头；不对外暴露
	IsDir        bool   `json:"is_dir"`
	Note         string `json:"note,omitempty"`
	Created      int64  `json:"created"` // Unix 秒
	Expires      int64  `json:"expires"` // Unix 秒；0 表示永久
	CodeHash     string `json:"code_hash,omitempty"`
	MaxDownloads int    `json:"max_downloads"` // 0 表示不限
	Downloads    int    `json:"downloads"`
	AllowPreview bool   `json:"allow_preview"`
	Revoked      bool   `json:"revoked"`
	LastDownload int64  `json:"last_download,omitempty"`
}

// status 返回记录在 now 时刻的状态。撤销优先于过期，便于管理页明确显示。
func (r shareRecord) status(now time.Time) string {
	if r.Revoked {
		return shareStatusRevoked
	}
	if r.Expires > 0 && now.Unix() >= r.Expires {
		return shareStatusExpired
	}
	if r.MaxDownloads > 0 && r.Downloads >= r.MaxDownloads {
		return shareStatusExhausted
	}
	return shareStatusActive
}

// sharesFile shares.json 的持久化结构。
type sharesFile struct {
	Version int           `json:"version"`
	Shares  []shareRecord `json:"shares"`
}

// shareStore 分享记录的内存视图 + 原子持久化。所有公开方法自行加锁。
type shareStore struct {
	mu      sync.Mutex
	path    string
	records map[string]shareRecord
}

// newShareStore 读取 shares.json 并建立索引。文件不存在视为空；解析失败时
// 记录日志并保持为空（服务仍可用，但不能把损坏内容当作有效分享继续发放）。
func newShareStore(path string) *shareStore {
	st := &shareStore{path: path, records: make(map[string]shareRecord)}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Error("读取分享记录失败，已按空列表启动", "err", err)
		}
		return st
	}
	var f sharesFile
	if err := json.Unmarshal(data, &f); err != nil {
		slog.Error("解析分享记录失败，已按空列表启动", "err", err)
		return st
	}
	for _, rec := range f.Shares {
		if rec.Token == "" {
			continue
		}
		if rec.Note == "" {
			rec.Note = shareDefaultNote
		}
		st.records[rec.Token] = rec
	}
	return st
}

// get 返回记录副本；不存在返回 false。
func (st *shareStore) get(token string) (shareRecord, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	rec, ok := st.records[token]
	return rec, ok
}

// list 返回按创建时间倒序排列的记录副本。
func (st *shareStore) list() []shareRecord {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]shareRecord, 0, len(st.records))
	for _, rec := range st.records {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created == out[j].Created {
			return out[i].Token < out[j].Token
		}
		return out[i].Created > out[j].Created
	})
	return out
}

// create 生成唯一 token 并落盘；code 非空时用 token 作盐计算哈希后一次写入。
// token 冲突概率极低，仍循环重试以保证唯一。
func (st *shareStore) create(rec shareRecord, code string) (shareRecord, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for {
		token, err := newShareToken()
		if err != nil {
			return shareRecord{}, err
		}
		if _, exists := st.records[token]; exists {
			continue
		}
		rec.Token = token
		if code != "" {
			rec.CodeHash = hashShareCode(token, code)
		}
		st.records[token] = rec
		if err := st.saveLocked(); err != nil {
			delete(st.records, token)
			return shareRecord{}, err
		}
		return rec, nil
	}
}

// revoke 标记撤销（记录保留，供管理页展示与用户确认）。
func (st *shareStore) revoke(token string) (shareRecord, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	rec, ok := st.records[token]
	if !ok {
		return shareRecord{}, errShareNotFound
	}
	if rec.Revoked {
		return rec, nil
	}
	rec.Revoked = true
	st.records[token] = rec
	if err := st.saveLocked(); err != nil {
		rec.Revoked = false
		st.records[token] = rec
		return shareRecord{}, err
	}
	return rec, nil
}

// delete 删除记录本身（不动文件）。返回是否存在。
func (st *shareStore) delete(token string) (bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	rec, ok := st.records[token]
	if !ok {
		return false, nil
	}
	delete(st.records, token)
	if err := st.saveLocked(); err != nil {
		st.records[token] = rec
		return false, err
	}
	return true, nil
}

// consumeDownload 在锁内校验状态并递增下载次数。校验与递增必须原子，否则
// 并发请求可以越过 max_downloads 上限。落盘失败时回滚内存计数，宁可失败
// 也不在没有记录额度的情况下继续发放文件。
func (st *shareStore) consumeDownload(token string, now time.Time) (shareRecord, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	rec, ok := st.records[token]
	if !ok {
		return shareRecord{}, errShareNotFound
	}
	switch rec.status(now) {
	case shareStatusRevoked:
		return rec, errShareRevoked
	case shareStatusExpired:
		return rec, errShareExpired
	case shareStatusExhausted:
		return rec, errShareExhausted
	}
	rec.Downloads++
	rec.LastDownload = now.Unix()
	st.records[token] = rec
	if err := st.saveLocked(); err != nil {
		rec.Downloads--
		rec.LastDownload = 0
		st.records[token] = rec
		return rec, err
	}
	return rec, nil
}

// saveLocked 以 0600 原子写入 shares.json（同目录临时文件 + rename）。
// 调用方必须已持有 st.mu。
func (st *shareStore) saveLocked() error {
	dir := filepath.Dir(st.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f := sharesFile{Version: 1, Shares: make([]shareRecord, 0, len(st.records))}
	for _, rec := range st.records {
		f.Shares = append(f.Shares, rec)
	}
	sort.Slice(f.Shares, func(i, j int) bool {
		if f.Shares[i].Created == f.Shares[j].Created {
			return f.Shares[i].Token < f.Shares[j].Token
		}
		return f.Shares[i].Created > f.Shares[j].Created
	})
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".shares-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, st.path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// newShareToken 生成 16 字节随机 token（base32 小写，无填充）。
func newShareToken() (string, error) {
	b := make([]byte, shareTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	return toLowerASCII(enc.EncodeToString(b)), nil
}

// shareTokenPrefix 日志用短标识：能定位是哪条分享，又不把完整能力凭据写进日志。
func shareTokenPrefix(token string) string {
	if len(token) > 8 {
		return token[:8]
	}
	return token
}

// hashShareCode 提取码只保存加盐哈希（盐即 token），比较用恒定时间。
func hashShareCode(token, code string) string {
	sum := sha256.Sum256([]byte(token + "\x00" + code))
	return hex.EncodeToString(sum[:])
}

// verifyShareCode 恒定时间比较提取码哈希。
func verifyShareCode(codeHash, token, code string) bool {
	want, err := hex.DecodeString(codeHash)
	if err != nil || len(want) == 0 {
		return false
	}
	got := sha256.Sum256([]byte(token + "\x00" + code))
	return subtle.ConstantTimeCompare(want, got[:]) == 1
}

// toLowerASCII 只折叠 ASCII 大写，避免 strings.ToLower 对非 ASCII 做额外转换。
func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
