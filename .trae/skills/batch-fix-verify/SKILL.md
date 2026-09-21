---
name: batch-fix-verify
description: Batch-fix issues with compile gate, end-to-end verification and cleanup. Use when user says 按清单继续修, 继续处理 P0/P1/P2, or approves an issue list. Not for one trivial edit.
---

# 批量修复 + 端到端验证流程

本仓库 audio-repair-studio 的约定化修复流程。当用户给出/批准一份问题清单（按 P0/P1/P2 分级）并要求继续时，按本流程执行，不要重新勘探仓库。

## 1. 规划与修改

- 用 TodoWrite 按批次列出任务；同一优先级合并为一批，改完一批再做下一批。
- 代码注释用中文；Go 后端 + web/static 原生 JS 三件套，不引入构建链。
- 只改清单内的项，不顺手做额外重构。

## 2. 编译闸门（每批改完必过）

环境为 Windows PowerShell 5：**没有 `&&`**，用 `;` 串联，靠 `$LASTEXITCODE` 判断：

```powershell
go vet ./...; if ($LASTEXITCODE -eq 0) { go test ./...; if ($LASTEXITCODE -eq 0) { go build -o bin/server.exe ./cmd/server; if ($LASTEXITCODE -eq 0) { Write-Output ALL_OK } } }
```

## 3. 端到端验证

### 3.1 启动测试服务器（后台，固定 8090 端口）

```powershell
$env:SERVER_ADDR=":8090"; .\bin\server.exe
```

用 run_in_background 启动，记下 command_id，结束时用 StopCommand 停。

### 3.2 造测试数据（ffmpeg lavfi，不要录真实音频）

```powershell
# MP3
ffmpeg -y -loglevel error -f lavfi -i sine=frequency=440:duration=10 -c:a libmp3lame test.mp3
# WAV（立体声 PCM）
ffmpeg -y -loglevel error -f lavfi -i sine=frequency=440:duration=300 -ac 2 -ar 44100 -c:a pcm_s16le big.wav
# 带封面：加 color 源 + -shortest + attached_pic
```

### 3.3 调 API 用 curl.exe

- 统一用 `curl.exe`（避免 PowerShell 的 `curl` 别名和 Invoke-WebRequest 坑）。
- multipart 里传 JSON 字段时，PowerShell 引号会吞内容，用文件重定向：
  `curl.exe -F "file=@x.mp3" -F "repair_params=<params.json" http://localhost:8090/api/songs`
- URL 里含 `?` 时不要直接放双引号字符串（`$?` 被当变量名导致 URL 损坏），用字符串拼接：
  `"http://localhost:8090/play/$id" + "?mode=repaired"`

### 3.4 验证 WebSocket 事件

- **不要用 PowerShell 5 的 ClientWebSocket**：后台会话里对象创建为 null，必然失败。
- 写临时 Go 客户端放 `testdata/wsclient/main.go`（gorilla/websocket 已是项目依赖）：
  - **Dial 的 URL scheme 必须是 `ws://`**，写成 `http://` 会报 `malformed ws or wss URL`。
  - 用 goroutine 持续 ReadMessage 送 channel；**不要用 SetReadDeadline**，超时会让连接进入 failed 态，之后读取直接 panic。
  - 主循环 select 消息与 time.Timer。
- 验证后删除整个临时客户端目录。
- 事件识别约定：歌曲修复事件 song_id 非空（stage started/processing/completed/failed）；目录导入事件 song_id 为空（stage import_start/import_copy/import_progress/import_done）。
- **认证场景（启用 Basic Auth）不要让浏览器用 URL 内嵌凭证导航**（`http://user:pass@host`）：Chrome 禁止该页面内的 fetch 构造凭证 URL，且凭证 URL 不写 auth cache，会误判为产品缺陷。认证链路的端到端证据以后端 Go WS 客户端为准（/api/ws-ticket 带 Basic Auth 取 ticket → ws ?ticket=xxx）；浏览器验证改用无认证标准场景。

### 3.5 浏览器验证

用 browser_use 子代理，prompt 中给出编号检查清单（按钮存在→点击→预期 UI→console 无红色错误），要求逐步回报 PASS/FAIL。同一任务连续调用不超过 3 次。

## 4. Windows 特有坑

- 删被 ffmpeg 占用的文件会失败，用重试模式（5 次、间隔 200ms），参考 songs.go 的 removeWithRetry。
- 改了代码必须重新 go build 并重启后台服务，否则验证的是旧二进制。

## 5. 清理（验证完立即做，不留垃圾）

1. 测试歌曲：`curl.exe -X DELETE http://localhost:8090/api/songs/<id>`（接口会取消修复任务并清理 original/repaired/covers 文件）。
2. 删除 storage/import 下的测试源文件、testdata 下的临时脚本与 JSON。
3. StopCommand 停后台服务器。
4. 保留 testdata/sample.wav（仓库原有文件）。

## 6. 汇报格式

- 表格列出每项改动：文件（可点击绝对链接，带行号）、改动要点、实测结果。
- 明确剩余未做项；全部完成后给出总览表（P0/P1/P2 状态）。

## 仓库关键事实（避免重复勘探）

- 允许扩展名：.mp3 .flac .wav .m4a .aac .ogg .wma
- storage/ 布局：original/（uuid+ext）、repaired/（uuid.用户选格式）、covers/（uuid.jpg）、import/（扫描导入源目录，复制不移动）
- DB：data/library.db；去重键为 文件名 + original_size；旧库加列靠启动时 ALTER TABLE（报错即列已存在，忽略）
- 认证：AUTH_USER + AUTH_PASSWORD 同时设置才启用 Basic Auth；/health 与 /ws/progress 不走密码中间件；浏览器 WS 靠一次性 ticket 鉴权（GET /api/ws-ticket 取，30 秒有效、用后即废，未启用认证时 required:false）
- 健康检查 /health 返回 db/ffmpeg/ffprobe/storage 四项 checks；WS 路径 /ws/progress
- retryRepair 顺序约定：先 Submit 成功，再删旧修复版/改状态（队列满时不破坏现状）；importOne 复制失败必须清理半成品 original 文件
