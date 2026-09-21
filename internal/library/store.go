package library

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
) // 纯 Go SQLite 驱动,无需 CGO

// Store 音乐库持久化层(SQLite + WAL)
type Store struct {
	db *sql.DB
}

// Open 打开/初始化数据库(自动建表)
func Open(dbPath string) (*Store, error) {
	// WAL 提升并发读,foreign_keys 启用外键,busy_timeout 避免写冲突
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite 写串行,设上限避免锁竞争
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// init 建表(幂等)
func (s *Store) init() error {
	const ddl = `
CREATE TABLE IF NOT EXISTS songs (
    id                TEXT PRIMARY KEY,
    title             TEXT NOT NULL DEFAULT '',
    artist            TEXT NOT NULL DEFAULT '',
    album             TEXT NOT NULL DEFAULT '',
    year              INTEGER NOT NULL DEFAULT 0,
    original_filename TEXT NOT NULL,
    original_path     TEXT NOT NULL,
    original_size     INTEGER NOT NULL DEFAULT 0,
    repaired_path     TEXT NOT NULL DEFAULT '',
    cover_path        TEXT NOT NULL DEFAULT '',
    duration_ms       INTEGER NOT NULL DEFAULT 0,
    sample_rate       INTEGER NOT NULL DEFAULT 0,
    channels          INTEGER NOT NULL DEFAULT 0,
    bitrate           INTEGER NOT NULL DEFAULT 0,
    codec             TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'pending',
    error_msg         TEXT NOT NULL DEFAULT '',
    repair_params     TEXT NOT NULL DEFAULT '',
    favorite          INTEGER NOT NULL DEFAULT 0,
    created_at        DATETIME NOT NULL,
    updated_at        DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_songs_status     ON songs(status);
CREATE INDEX IF NOT EXISTS idx_songs_created_at ON songs(created_at DESC);
`
	_, err := s.db.Exec(ddl)
	if err != nil {
		return err
	}
	// 迁移:旧库没有 original_size / favorite 列时补上
	for _, col := range []string{
		`ALTER TABLE songs ADD COLUMN original_size INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE songs ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := s.db.Exec(col); err != nil {
			_ = err // 列已存在则忽略
		}
	}
	return nil
}

// Close 关闭数据库
func (s *Store) Close() error { return s.db.Close() }

// Ping 探活数据库连接
func (s *Store) Ping() error { return s.db.Ping() }

// AddSong 入库新歌曲
func (s *Store) AddSong(song *Song) error {
	ctx := context.Background()
	if song.CreatedAt.IsZero() {
		song.CreatedAt = time.Now()
	}
	song.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO songs
		(id,title,artist,album,year,original_filename,original_path,original_size,repaired_path,cover_path,
		duration_ms,sample_rate,channels,bitrate,codec,status,error_msg,repair_params,
		created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		song.ID, song.Title, song.Artist, song.Album, song.Year,
		song.OriginalFilename, song.OriginalPath, song.OriginalSize, song.RepairedPath, song.CoverPath,
		song.DurationMs, song.SampleRate, song.Channels, song.Bitrate, song.Codec,
		string(song.Status), song.ErrorMsg, song.RepairParams,
		song.CreatedAt, song.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert song: %w", err)
	}
	return nil
}

// GetSong 按 ID 查询单曲
func (s *Store) GetSong(id string) (*Song, error) {
	row := s.db.QueryRowContext(context.Background(),
		`SELECT id,title,artist,album,year,original_filename,original_path,original_size,repaired_path,cover_path,
		duration_ms,sample_rate,channels,bitrate,codec,status,error_msg,repair_params,favorite,created_at,updated_at
		FROM songs WHERE id = ?`, id)
	song, err := scanSong(row)
	if err != nil {
		return nil, fmt.Errorf("get song %s: %w", id, err)
	}
	return song, nil
}

// ListSongs 按创建时间倒序返回所有歌曲
func (s *Store) ListSongs() ([]*Song, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT id,title,artist,album,year,original_filename,original_path,original_size,repaired_path,cover_path,
		duration_ms,sample_rate,channels,bitrate,codec,status,error_msg,repair_params,favorite,created_at,updated_at
		FROM songs ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list songs: %w", err)
	}
	defer rows.Close()
	var songs []*Song
	for rows.Next() {
		song, err := scanSong(rows)
		if err != nil {
			return nil, fmt.Errorf("scan song: %w", err)
		}
		songs = append(songs, song)
	}
	return songs, rows.Err()
}

// UpdateStatus 更新修复状态与产物路径
func (s *Store) UpdateStatus(id string, status Status, repairedPath, errMsg string) error {
	_, err := s.db.ExecContext(context.Background(),
		`UPDATE songs SET status = ?, repaired_path = ?, error_msg = ?, updated_at = ?
		WHERE id = ?`,
		string(status), repairedPath, errMsg, time.Now(), id)
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	return nil
}

// UpdateRepairParams 更新修复参数(重试时可换参数)
func (s *Store) UpdateRepairParams(id, params string) error {
	_, err := s.db.ExecContext(context.Background(),
		`UPDATE songs SET repair_params = ?, updated_at = ? WHERE id = ?`,
		params, time.Now(), id)
	return err
}

// UpdateMeta 更新歌曲元信息(标题/歌手/专辑/年份),供前端手动修正 ffprobe 解析错误
func (s *Store) UpdateMeta(id, title, artist, album string, year int) error {
	_, err := s.db.ExecContext(context.Background(),
		`UPDATE songs SET title = ?, artist = ?, album = ?, year = ?, updated_at = ? WHERE id = ?`,
		title, artist, album, year, time.Now(), id)
	return err
}

// ToggleFavorite 切换收藏状态,返回切换后的状态
func (s *Store) ToggleFavorite(id string) (bool, error) {
	_, err := s.db.ExecContext(context.Background(),
		`UPDATE songs SET favorite = 1 - favorite, updated_at = ? WHERE id = ?`,
		time.Now(), id)
	if err != nil {
		return false, err
	}
	var fav int
	err = s.db.QueryRowContext(context.Background(),
		`SELECT favorite FROM songs WHERE id = ?`, id).Scan(&fav)
	return fav != 0, err
}

// ExistsByOriginalFilename 按原始文件名判断是否已入库(目录扫描导入去重用)
// 注意:仅按文件名可能误伤同名不同歌,建议用 ExistsByNameAndSize
func (s *Store) ExistsByOriginalFilename(name string) (bool, error) {
	var ok int
	err := s.db.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM songs WHERE original_filename = ?)`, name).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check filename %q: %w", name, err)
	}
	return ok == 1, nil
}

// ExistsByNameAndSize 按 原始文件名 + 文件大小 联合去重(目录扫描导入用)
// 比仅按文件名更稳妥:同名但大小不同(通常是不同歌曲)不会被误跳过
func (s *Store) ExistsByNameAndSize(name string, size int64) (bool, error) {
	var ok int
	// 文件大小用 duration_ms 字段存不下,这里用 original_path 关联的文件大小
	// 简化:用 original_filename + 修复前文件大小(byte),大小信息由调用方传入
	err := s.db.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM songs WHERE original_filename = ? AND original_size = ?)`,
		name, size).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check name+size %q %d: %w", name, size, err)
	}
	return ok == 1, nil
}

// DeleteSong 删除歌曲记录(文件由调用方负责清理)
func (s *Store) DeleteSong(id string) error {
	_, err := s.db.ExecContext(context.Background(),
		`DELETE FROM songs WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete song: %w", err)
	}
	return nil
}

// DeleteSongs 批量删除歌曲记录(文件由调用方负责清理)
func (s *Store) DeleteSongs(ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	// 构造 IN (?, ?, ...) 占位符
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`DELETE FROM songs WHERE id IN (%s)`, strings.Join(placeholders, ","))
	res, err := s.db.ExecContext(context.Background(), query, args...)
	if err != nil {
		return 0, fmt.Errorf("delete songs: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// scanner 抽象 *sql.Row 和 *sql.Rows 的 Scan 方法
type scanner interface {
	Scan(dest ...any) error
}

func scanSong(s scanner) (*Song, error) {
	song := &Song{}
	var status string
	var fav int
	err := s.Scan(
		&song.ID, &song.Title, &song.Artist, &song.Album, &song.Year,
		&song.OriginalFilename, &song.OriginalPath, &song.OriginalSize, &song.RepairedPath, &song.CoverPath,
		&song.DurationMs, &song.SampleRate, &song.Channels, &song.Bitrate, &song.Codec,
		&status, &song.ErrorMsg, &song.RepairParams, &fav,
		&song.CreatedAt, &song.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	song.Status = Status(status)
	song.Favorite = fav != 0
	return song, nil
}
