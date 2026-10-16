package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// scriptableExt 可执行/可脚本化内容：直链访问强制附件下载（防源内存储型 XSS）。
// 前端预览走 fetch + textContent 渲染，不受 Content-Disposition 影响。
var scriptableExt = map[string]bool{
	".html": true, ".htm": true, ".xhtml": true,
	".svg": true, ".js": true, ".mjs": true, ".cjs": true,
}

// sanitizeFilename 剔除文件名中的控制字符，作为 Content-Disposition 纵深防御，
// 避免任何残留的响应头注入面（POSIX 文件名可含 \r \n 等）。
func sanitizeFilename(name string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
}

// requestIDKey 请求 id 的 context 键类型（未导出的空结构体避免键冲突）。
type requestIDKey struct{}

// withRequestID 为每个请求确定 request id：优先沿用调用方传入的
// X-Request-Id（便于跨端关联），否则生成随机 id；写回响应头并注入 context。
func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// requestID 读取 context 中的请求 id（无则空串）。
func requestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// newRequestID 生成 16 位十六进制随机 id；随机源失败时回退固定占位（不 panic）。
func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// accessLog 访问日志（-v 开启后级别为 Debug，故仅详细模式下打印）。
// 使用 slog 的结构化字段，控制台仍是人类可读文本（非 JSON）。
// /api/auth/* 只记录路径本身、不记录查询串，避免任何 token/凭据进入日志。
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logPath := r.URL.RequestURI()
		if strings.HasPrefix(r.URL.Path, "/api/auth/") {
			logPath = r.URL.Path
		}
		slog.Debug("request",
			"request_id", requestID(r.Context()),
			"method", r.Method,
			"path", logPath,
			"remote", r.RemoteAddr,
			"duration", time.Since(start).String(),
		)
	})
}
