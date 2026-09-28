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
	MediaType        string    `json:"media_type"`         // 介质类型:vinyl/cassette/reel,空=默认(影响修复预设)
	SplitPoints      string    `json:"split_points"`        // 切割元数据(JSON,仅整盘源):silence spans + points;切片此列为空
	CurrentVersion   int       `json:"current_version"`   // 当前选中的修复版本号(时光机功能),0=无版本
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// RepairVersion 修复版本记录(对应 repair_versions 表一行)
// 每次修复(含重试)创建一个新版本,保留历史修复结果供 A/B 对比与回滚
type RepairVersion struct {
	ID          int64     `json:"id"`           // 自增主键
	SongID      string    `json:"song_id"`      // 关联歌曲 ID
	Version     int       `json:"version"`     // 版本号:1,2,3...(同一歌曲内自增)
	Params      string    `json:"params"`       // 修复参数快照(JSON)
	Path        string    `json:"path"`         // 修复文件相对路径,如 repaired/<id>/v1.flac
	Status      string    `json:"status"`       // pending/done/failed
	Error       string    `json:"error"`        // 失败原因
	DurationMs  int64     `json:"duration_ms"`  // 修复耗时(毫秒)
	VBitrate    int       `json:"v_bitrate"`    // 修复文件比特率(kbps,probe 后写入)
	VSampleRate int       `json:"v_sample_rate"`// 修复文件采样率(probe 后写入)
	CreatedAt   time.Time `json:"created_at"`
}
