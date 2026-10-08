package server

import "os"

// mp4Layout 描述 MP4 顶层结构：moov 位置/大小与 mdat 块数量
type mp4Layout struct {
	moovOffset int64
	moovSize   int64
	mdatCount  int
}

// mp4LayoutOf 解析 MP4 顶层 box。关键性能点：顺序读文件头部一块区域后
// 在内存中解析 box，而不是每个 box 一次 ReadAt 随机寻道——怪封装文件有
// 几百到几千个 mdat 块，逐块 seek 在机械盘上要几秒（实测 4s），而顺序读
// 头部区域只需 ~150ms。
// 头部未找到 moov 时再读尾部区域找 moov（moov 在尾部的正常录制片）——
// 供 thumb-src 正确识别「moov 后置」文件，避免误判为解析失败而整段返回。
func mp4LayoutOf(abs string) mp4Layout {
	var l mp4Layout
	l.moovOffset = -1
	f, err := os.Open(abs)
	if err != nil {
		return l
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || st.Size() < 1024 {
		return l
	}
	fileSize := st.Size()
	buf := make([]byte, mp4HeadProbe)
	n, _ := f.Read(buf) // 顺序读头部（机械盘友好）
	buf = buf[:n]
	var pos int64 = 0
	const maxBoxes = 5000
	for i := 0; i < maxBoxes && pos+8 <= int64(len(buf)); i++ {
		boxSize := int64(buf[pos])<<24 | int64(buf[pos+1])<<16 | int64(buf[pos+2])<<8 | int64(buf[pos+3])
		typ := string(buf[pos+4 : pos+8])
		if typ == "moov" {
			// 记录 moov 位置但不返回：怪封装可能是 moov 在头 + 大量 mdat 碎块在后，
			// 必须继续遍历才能统计 mdat 块数（这是「怪封装」的真正判据）。
			l.moovOffset = pos
			l.moovSize = boxSize
		} else if typ == "mdat" {
			l.mdatCount++
			if boxSize == 0 {
				break // mdat 延伸到 EOF，其后不再有顶层 box
			}
		}
		if boxSize == 1 {
			if pos+16 > int64(len(buf)) {
				break
			}
			boxSize = int64(buf[pos+8])<<56 | int64(buf[pos+9])<<48 | int64(buf[pos+10])<<40 | int64(buf[pos+11])<<32 |
				int64(buf[pos+12])<<24 | int64(buf[pos+13])<<16 | int64(buf[pos+14])<<8 | int64(buf[pos+15])
		}
		if boxSize < 8 {
			break
		}
		pos += boxSize
	}

	// 头部未找到 moov：读尾部区域找 moov（moov 在尾部的正常录制片）
	if l.moovOffset < 0 {
		tailStart := fileSize - mp4HeadProbe
		if tailStart < 0 {
			tailStart = 0
		}
		tbuf := make([]byte, mp4HeadProbe)
		tn, _ := f.ReadAt(tbuf, tailStart)
		tbuf = tbuf[:tn]
		var tpos int64 = 0
		for i := 0; i < maxBoxes && tpos+8 <= int64(len(tbuf)); i++ {
			boxSize := int64(tbuf[tpos])<<24 | int64(tbuf[tpos+1])<<16 | int64(tbuf[tpos+2])<<8 | int64(tbuf[tpos+3])
			typ := string(tbuf[tpos+4 : tpos+8])
			if typ == "moov" {
				l.moovOffset = tailStart + tpos
				l.moovSize = boxSize
				return l
			}
			if boxSize == 1 {
				if tpos+16 > int64(len(tbuf)) {
					break
				}
				boxSize = int64(tbuf[tpos+8])<<56 | int64(tbuf[tpos+9])<<48 | int64(tbuf[tpos+10])<<40 | int64(tbuf[tpos+11])<<32 |
					int64(tbuf[tpos+12])<<24 | int64(tbuf[tpos+13])<<16 | int64(tbuf[tpos+14])<<8 | int64(tbuf[tpos+15])
			}
			if boxSize < 8 {
				break
			}
			tpos += boxSize
		}
	}
	return l
}

// mp4MoovOffset 解析 MP4 顶层 box 结构，返回 moov box 的起始偏移。
// 返回 -1 表示未找到 moov（可能为分片 MP4 或无 moov）。
func mp4MoovOffset(f *os.File, size int64) int64 {
	buf := make([]byte, 8)
	var pos int64 = 0
	const maxBoxes = 64
	for i := 0; i < maxBoxes && pos+8 <= size; i++ {
		if _, err := f.ReadAt(buf, pos); err != nil {
			return -1
		}
		boxSize := int64(buf[0])<<24 | int64(buf[1])<<16 | int64(buf[2])<<8 | int64(buf[3])
		typ := string(buf[4:8])
		if typ == "moov" {
			return pos
		}
		if typ == "mdat" && boxSize == 0 {
			// mdat 延伸到文件末尾，其后不再有顶层 moov（moov 缺失或位于 mdat 前）
			return -1
		}
		// 64 位扩展 box 尺寸（size==1）：必须在 boxSize<8 判定之前处理，
		// 否则 size==1 会被误判为畸形而提前返回 -1
		if boxSize == 1 {
			var ext [8]byte
			if _, err := f.ReadAt(ext[:], pos+8); err != nil {
				return -1
			}
			boxSize = int64(ext[0])<<56 | int64(ext[1])<<48 | int64(ext[2])<<40 | int64(ext[3])<<32 |
				int64(ext[4])<<24 | int64(ext[5])<<16 | int64(ext[6])<<8 | int64(ext[7])
		}
		if boxSize < 8 {
			return -1
		}
		pos += boxSize
	}
	return -1
}

// mp4IsFastStart 判断 MP4 是否已 faststart（moov 在文件头部，浏览器可直接起播）。
func mp4IsFastStart(abs string) bool {
	l := mp4LayoutOf(abs)
	// moov 在开头 256KB 内视为已 faststart（正常情况 moov 紧跟 ftyp，几十 KB 内）
	return l.moovOffset >= 0 && l.moovOffset <= 256*1024
}
