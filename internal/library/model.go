// Package library 音乐库数据层:模型定义 + SQLite 持久化 + 文件路径管理
package library

import "time"

// Status 歌曲修复状态枚举(字符串直接入库,便于查询与可读)
type Status string

const (
	StatusPending   Status = "pending"   // 已入库,等待修复
	StatusRepairing Status = "repairing"  // 正在修复
	StatusRepaired  Status = "repaired"   // 修复完成
	StatusFailed    Status = "failed"     // 修复失败
)

// Mode 播放模式,用于 /play/:id?mode=original|repaired
type Mode string

const (
	ModeOriginal Mode = "original"
	ModeRepaired Mode = "repaired"
)

// Song 歌曲实体(对应 SQLite 表 songs 一行)
type Song struct {
	ID               string    `json:"id"`                // UUID 主键
	Title            string    `json:"title"`             // 歌曲名
	Artist           string    `json:"artist"`            // 艺术家
	Album            string    `json:"album"`             // 专辑
	Year             int       `json:"year"`              // 发行年份
	OriginalFilename string    `json:"original_filename"` // 用户上传时的原文件名(展示用)
	OriginalPath    string    `json:"original_path"`     // 原文件相对 storage 的路径,如 original/<id>.mp3
	OriginalSize    int64     `json:"original_size"`      // 原文件大小(字节,用于导入去重)
	RepairedPath     string    `json:"repaired_path"`     // 修复文件相对路径,空表示未修复
	CoverPath       string    `json:"cover_path"`        // 封面图相对路径,可选
	DurationMs       int64     `json:"duration_ms"`       // 时长(毫秒)
	SampleRate       int       `json:"sample_rate"`       // 采样率
	Channels         int       `json:"channels"`          // 声道数
	Bitrate          int       `json:"bitrate"`            // 比特率(kbps)
	Codec            string    `json:"codec"`              // 编码格式
	Status           Status    `json:"status"`            // 修复状态
	ErrorMsg         string    `json:"error_msg"`          // 失败原因
	RepairParams     string    `json:"repair_params"`      // 修复参数(JSON)
	Favorite         bool      `json:"favorite"`           // 是否收藏
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
