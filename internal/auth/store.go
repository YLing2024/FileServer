package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ============================================================
// 凭据与会话的持久化：.FileServer/auth.json（0600，原子写）。
//
// 只存服务端产物：口令为 Argon2id PHC 哈希；TOTP 密钥为明文（无主密钥可加密，
// 取舍见 doc/security.md：文件 0600 + 目录受控，等价于 SSH 私钥的保护级别）；
// 会话/记住我 token 只存 sha256 哈希，绝不落明文。
// ============================================================

// authFile auth.json 的持久化结构。
type authFile struct {
	Version      int             `json:"version"`
	Mode         string          `json:"mode"`
	Password     string          `json:"password,omitempty"` // Argon2id PHC 哈希
	TOTPSecret   string          `json:"totp_secret,omitempty"`
	LastTOTPStep int64           `json:"last_totp_step,omitempty"` // 已消费的最晚时间步（重放防护）
	Sessions     []sessionRecord `json:"sessions,omitempty"`
}

// sessionRecord 一个会话/记住我 token 的服务端记录（token 只存哈希）。
type sessionRecord struct {
	ID        string `json:"id"`
	TokenHash string `json:"token_hash"` // sha256 hex
	Created   int64  `json:"created"`
	LastSeen  int64  `json:"last_seen"`
	Expires   int64  `json:"expires"`
	Remember  bool   `json:"remember"`
	UA        string `json:"ua,omitempty"`
	IP        string `json:"ip,omitempty"`
}

// loadAuthFile 读取 auth.json；不存在返回空结构（未配置态）。
func loadAuthFile(path string) (authFile, error) {
	var af authFile
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return authFile{Version: 1}, nil
		}
		return af, err
	}
	if err := json.Unmarshal(data, &af); err != nil {
		return authFile{Version: 1}, err
	}
	if af.Version == 0 {
		af.Version = 1
	}
	return af, nil
}

// ResetTOTPFile 清除 auth.json 中的 TOTP 绑定（命令行自救：--reset-totp）。
func ResetTOTPFile(path string) error {
	af, err := loadAuthFile(path)
	if err != nil {
		return err
	}
	af.TOTPSecret = ""
	af.LastTOTPStep = 0
	return saveAuthFile(path, &af)
}

// ResetAllFile 清除 auth.json 中全部凭据与会话（命令行自救：--reset-auth）。
func ResetAllFile(path string) error {
	af, err := loadAuthFile(path)
	if err != nil {
		return err
	}
	af.Password = ""
	af.TOTPSecret = ""
	af.LastTOTPStep = 0
	af.Sessions = nil
	return saveAuthFile(path, &af)
}

// saveAuthFile 以 0600 原子写入 auth.json（同目录临时文件 + rename）。
func saveAuthFile(path string, af *authFile) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(af, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".auth-*.tmp")
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
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
