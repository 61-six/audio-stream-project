// Package config 全局配置:从环境变量加载,便于 Docker 通过 -e 注入
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Config 应用全局配置
type Config struct {
	Env               string // 环境标志:development / production
	ServerAddr        string // HTTP 监听地址,如 :8080
	StoragePath      string // 存储根目录(内含 original/repaired/covers 子目录)
	ImportPath       string // 目录扫描导入:用户投放歌曲的目录(递归扫描)
	DBPath           string // SQLite 数据库文件路径
	RepairConcurrency int    // 修复任务并发数
	AuthUser         string // Basic Auth 用户名(空则不启用认证)
	AuthPassword     string // Basic Auth 密码(与用户名同时设置才生效)
}

// Load 从环境变量加载,缺省值适合本地开发
// 相对路径会转成绝对路径,避免子进程 cwd 不一致导致文件写入失败
func Load() *Config {
	cfg := &Config{
		Env:                getenv("ENV", "development"),
		ServerAddr:         getenv("SERVER_ADDR", ":8080"),
		StoragePath:        getenv("STORAGE_PATH", "./storage"),
		ImportPath:         getenv("IMPORT_DIR", "./storage/import"),
		DBPath:             getenv("DB_PATH", "./data/library.db"),
		RepairConcurrency:  getint("REPAIR_CONCURRENCY", 2),
		AuthUser:           getenv("AUTH_USER", ""),
		AuthPassword:       getenv("AUTH_PASSWORD", ""),
	}
	cfg.StoragePath = toAbs(cfg.StoragePath)
	cfg.ImportPath = toAbs(cfg.ImportPath)
	cfg.DBPath = toAbs(cfg.DBPath)
	cfg.validate()
	return cfg
}

// validate 校验配置合法性,非法则直接 panic(启动期失败优于运行期踩坑)
func (c *Config) validate() {
	// 导入目录不能等于原始音频目录,否则扫描导入会把已入库的歌重复入库
	if c.ImportPath == c.OriginalDir() {
		panic(fmt.Sprintf("IMPORT_DIR (%s) 不能等于原始音频目录 (%s),会导致重复扫描", c.ImportPath, c.OriginalDir()))
	}
	if c.RepairConcurrency < 1 {
		panic("REPAIR_CONCURRENCY 必须 >= 1")
	}
	// 认证只配了一半:给出明确提示,避免误以为已启用防护
	if (c.AuthUser == "") != (c.AuthPassword == "") {
		panic("Basic Auth 需要同时设置 AUTH_USER 和 AUTH_PASSWORD(只设一个不会启用认证)")
	}
}

// toAbs 把相对路径转成绝对路径(基于当前工作目录)
func toAbs(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// OriginalDir 原始音频目录(绝对路径)
func (c *Config) OriginalDir() string { return filepath.Join(c.StoragePath, "original") }

// RepairedDir 修复后音频目录
func (c *Config) RepairedDir() string { return filepath.Join(c.StoragePath, "repaired") }

// CoversDir 封面图目录
func (c *Config) CoversDir() string { return filepath.Join(c.StoragePath, "covers") }

// ImportDir 目录扫描导入目录(用户把待导入歌曲放到这里,可含子目录)
func (c *Config) ImportDir() string { return c.ImportPath }

// AuthEnabled 是否启用 Basic Auth(用户名和密码同时设置才生效)
func (c *Config) AuthEnabled() bool {
	return c.AuthUser != "" && c.AuthPassword != ""
}

// DataDir SQLite 所在目录(用于自动创建)
func (c *Config) DataDir() string { return filepath.Dir(c.DBPath) }

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getint(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
