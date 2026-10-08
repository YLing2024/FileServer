package server

import "sync/atomic"

// config 服务器运行配置。构造后字段不再变更（transcodeEnabled 例外：可由前端
// 设置面板动态开关），集中一处让 Server 只持一个 cfg。
type config struct {
	root    string // 服务根目录（绝对路径）
	hidden  bool   // 是否显示隐藏文件
	auth    string // 可选口令 "user:pass"
	verbose bool

	// transcodeEnabled：冷门格式（MKV/RMVB/HEVC 等）在线转码播放 + 服务端抽帧缩略图。
	// 默认跟随 --ffmpeg 参数，可在前端设置面板动态切换（POST /api/settings/ffmpeg）。
	transcodeEnabled atomic.Bool
}
