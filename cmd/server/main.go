// Package main audio-repair-studio 服务入口
//
// 启动流程:加载配置 → 初始化存储目录 → 初始化 SQLite →
// 启动修复队列 → 注册 HTTP 路由 → 监听并优雅关闭
package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"audio-repair-studio/internal/audio"
	"audio-repair-studio/internal/config"
	"audio-repair-studio/internal/library"
	"audio-repair-studio/internal/server"
)

// slogLogWriter 把标准库 log 的输出转发给 slog,实现全项目结构化日志
// (无需逐个替换 log.Printf)
type slogLogWriter struct{ logger *slog.Logger }

func (w *slogLogWriter) Write(p []byte) (int, error) {
	w.logger.Info(strings.TrimSpace(string(p)))
	return len(p), nil
}

func main() {
	cfg := config.Load()

	// 配置 slog:production 用 JSON(便于采集),development 用文本(易读)
	var slogHandler slog.Handler
	if cfg.Env == "production" {
		slogHandler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	} else {
		slogHandler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	}
	slog.SetDefault(slog.New(slogHandler))
	// 标准库 log 重定向到 slog,统一格式
	log.SetFlags(0)
	log.SetOutput(&slogLogWriter{logger: slog.Default()})

	cwd, _ := os.Getwd()
	slog.Info("[startup] cwd", "cwd", cwd)
	slog.Info("[startup] storage", "path", cfg.StoragePath)
	slog.Info("[startup] db", "path", cfg.DBPath)

	// 1) 确保存储目录存在
	for _, dir := range []string{cfg.OriginalDir(), cfg.RepairedDir(), cfg.CoversDir(), cfg.ImportDir(), cfg.DataDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			slog.Error("mkdir failed", "dir", dir, "err", err)
			os.Exit(1)
		}
	}
	slog.Info("[startup] import dir", "path", cfg.ImportDir(),
		"hint", "把歌曲放入此目录,网页点「扫描导入」即可批量入库")

	if cfg.AuthEnabled() {
		slog.Info("[startup] Basic Auth 已启用", "user", cfg.AuthUser)
	} else {
		slog.Info("[startup] Basic Auth 未启用(设置 AUTH_USER 和 AUTH_PASSWORD 可开启)")
	}

	// 启动诊断:在 storage/original 下尝试创建一个探针文件,失败时打印具体错误
	probePath := filepath.Join(cfg.OriginalDir(), "_probe.txt")
	if f, err := os.Create(probePath); err != nil {
		slog.Error("[DIAG] write probe FAILED", "err", err)
	} else {
		slog.Info("[DIAG] write probe OK")
		_ = f.Close()
		_ = os.Remove(probePath)
	}

	// 2) 初始化 SQLite
	store, err := library.Open(cfg.DBPath)
	if err != nil {
		slog.Error("open db failed", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	// 3) 进度总线 + 修复队列
	bus := audio.NewProgressBus()
	queue := audio.NewQueue(store, cfg.StoragePath, cfg.RepairConcurrency, bus)
	defer queue.Stop()

	// 4) HTTP 路由
	httpHandler := server.New(cfg, store, queue, bus)
	srv := &http.Server{
		Addr:    cfg.ServerAddr,
		Handler: httpHandler,
		// ReadTimeout 覆盖整个请求体读取,完整歌曲(几十 MB)在慢网络下
		// 上传可能超过 30s 被掐断,因此不设整体读超时;
		// 改用 ReadHeaderTimeout 只限制请求头(防 slowloris)
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       0,
		WriteTimeout:      0, // 流式播放需要长连接,不设写超时
		IdleTimeout:       120 * time.Second,
	}

	// 5) 启动监听
	go func() {
		slog.Info("audio-repair-studio listening", "addr", cfg.ServerAddr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen failed", "err", err)
			os.Exit(1)
		}
	}()

	// 6) 优雅关闭
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	slog.Info("server stopped")
}
