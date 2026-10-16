package server

import (
	"sync/atomic"

	"github.com/YLing2024/FileServer/internal/auth"
)

// config 服务器运行配置。构造后字段不再变更（transcodeEnabled 例外：可由前端
// 设置面板动态开关），集中一处让 Server 只持一个 cfg。
type config struct {
	root    string // 服务根目录（绝对路径）
	hidden  bool   // 是否显示隐藏文件
	verbose bool

	auth         *auth.Manager // 认证状态机（认证关闭时为 nil）
	authRequired bool          // 是否要求认证（Manager 初始化失败时也保持失败关闭）
	trustProxy   bool          // 是否信任 X-Forwarded-For（取最左）
	cookieSecure bool          // 强制 cookie Secure

	textMaxBytes int64 // 可编辑文本大小上限（字节）

	// transcodeEnabled：冷门格式（MKV/RMVB/HEVC 等）在线转码播放 + 服务端抽帧缩略图。
	// 默认跟随 --ffmpeg 参数，可在前端设置面板动态切换（POST /api/settings/ffmpeg）。
	transcodeEnabled atomic.Bool
}
