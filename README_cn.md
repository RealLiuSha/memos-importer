# memos-importer

这是供部分平台或文档站点按语言文件名识别的中文快速说明；完整行为、安全与验收说明以 [README.md](README.md) 为准。

memos-importer 是一个自托管的 Notion 到 memos 导入控制台。它以 Go 单二进制运行，内嵌 Web 界面，使用本地 SQLite 保存映射关系、附件记录和导入任务状态。memos / Notion 凭据默认保存在当前浏览器的 `localStorage`，不会写入后端 SQLite。

当前目标是把 Notion 页面或数据库条目导入到 memos 0.29.1+，并尽量保留原文档时间、正文结构和 Notion 托管附件。

## 功能

- 在浏览器本地保存并校验 memos endpoint、memos access token 和 Notion integration token。
- 拉取 Notion integration 可访问的页面和数据库，并在 Web 界面中选择导入范围。
- 预览单篇 Notion 文档转换后的 Markdown、附件数量和 unsupported block warning。
- 将 Notion 托管的 `image/file/pdf/video` 下载后上传到 memos。
- 导入后的正文使用 memos `/file/attachments/{uid}/{filename}` 路径，不依赖 Notion 临时文件 URL。
- 导入 memo 时默认使用 Notion `created_time`，也支持 `last_edited_time` 或 `property:<Notion date property name>`。
- 支持导入可见性选择：`PRIVATE`、`PROTECTED`、`PUBLIC`。
- 支持重复导入策略：未变化内容自动跳过，变化内容可选择 `skip` 或 `overwrite`。
- 导入任务异步执行，Web 界面可实时查看进度、历史、明细、失败原因和 warning。
- 支持取消、断点续跑和失败项重试。
- 不直连 memos 数据库，只通过 memos `/api/v1/*` API 写入数据。

## 快速开始

```bash
go run ./cmd/server
```

默认打开：

```text
http://127.0.0.1:8080
```

常用环境变量：

```bash
MEMOS_IMPORTER_DB=memos-importer.db
MEMOS_IMPORTER_LISTEN_ADDR=127.0.0.1:8080
MEMOS_IMPORTER_ACCESS_PASSWORD=
MEMOS_IMPORTER_MEMOS_ENDPOINT=http://127.0.0.1:5230
MEMOS_IMPORTER_MEMOS_TOKEN=
MEMOS_IMPORTER_NOTION_TOKEN=
MEMOS_IMPORTER_NOTION_TIME_SOURCE=created_time
MEMOS_IMPORTER_WORKERS=4
MEMOS_IMPORTER_REQUEST_TIMEOUT=30s
```

监听非 loopback 地址时必须设置 `MEMOS_IMPORTER_ACCESS_PASSWORD`。Web 控制台解锁后通过 HttpOnly same-origin session cookie 访问 API 和 SSE 进度流。

## Docker

```bash
docker build -t memos-importer:local .
docker run --rm \
  -p 8080:8080 \
  -v memos-importer-data:/data \
  -e MEMOS_IMPORTER_ACCESS_PASSWORD='change-me' \
  -e MEMOS_IMPORTER_MEMOS_ENDPOINT='https://memos.example.com' \
  memos-importer:local
```

容器镜像默认将 SQLite 数据库放在 `/data/memos-importer.db`，建议挂载持久化 volume。打开 Web 控制台后，每个浏览器会把自己的 memos / Notion 配置保存到本地。Docker Hub 英文说明见 [README_docker.md](README_docker.md)。

## 构建与验证

```bash
make build
make test
make ui-smoke
```

`make test` 会运行 Go 测试并检查 importer 核心不依赖 Notion adapter。`make ui-smoke` 会构建前端并用浏览器自动化验证控制台主流程。

## 真实端点验收

1. 使用测试 memos 实例和测试 Notion 页面。
2. 启动 `cmd/server`。
3. 在 Web 控制台保存并校验 memos 与 Notion 配置；配置会保存在当前浏览器本地。
4. 导入一篇包含图片或文件的 Notion 页面。
5. 确认 memos 正文包含 `/file/attachments/{uid}/{filename}`，且不包含 Notion 临时文件 URL。
6. 用 `skip` 和 `overwrite` 分别重复导入一次，确认不会创建重复 memo。

不要把 token、原始私密正文、附件内容或 Notion 临时文件 URL 写入 issue、日志或文档。
