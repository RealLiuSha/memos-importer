[English](README.md) | **简体中文**

# memos-importer

memos-importer 是一个自托管的 Notion 到 [memos](https://github.com/usememos/memos) 导入控制台。它以 Go 单二进制运行，内嵌 Web 界面，使用本地 SQLite 保存映射关系、附件记录和导入任务状态。memos / Notion 凭据默认保存在当前浏览器的 `localStorage`，不会写入后端 SQLite。

当前目标是把 Notion 页面或数据库条目导入到 memos 0.29.1+，并尽量保留原文档时间、正文结构和 Notion 托管附件。

在线实例：<https://memos-importer.liusha.net>

## 功能

- 支持在浏览器本地保存并校验 memos endpoint、memos access token 和 Notion integration token。
- 支持拉取 Notion integration 可访问的页面和数据库，按最近编辑时间倒序展示；Web 控制台
  默认加载 100 篇文档，并允许用户在选择导入范围前调整有上限的加载数量。
- 支持预览单篇 Notion 文档转换后的 Markdown、附件数量和 unsupported block warning。
- Notion 托管的 `image/file/pdf/video` 会先下载，再上传到 memos。
- 导入后的正文使用 memos `/file/attachments/{uid}/{filename}` 路径，不依赖 Notion 临时文件 URL。
- 导入 memo 时默认使用 Notion `created_time`，也支持 `last_edited_time` 或 `property:<Notion date property name>`。
- 支持导入可见性选择：`PRIVATE`、`PROTECTED`、`PUBLIC`。
- 支持重复导入策略：未变化内容自动跳过，变化内容可选择 `skip` 或 `overwrite`。
- 导入任务异步执行，Web 界面可实时查看进度、历史、明细、失败原因和 warning。
- 支持取消、断点续跑和失败项重试。
- 不直连 memos 数据库，只通过 memos `/api/v1/*` API 写入数据。

## 非目标

- 不兼容 memos 0.29.1 以前版本。
- 不做双向同步、定时同步、多用户/多租户。
- v1 只支持 Notion，不支持 Markdown 目录、Flomo 或其他数据源。
- 不承诺完整还原所有 Notion block；不支持的 block 会以 warning 和可见占位保留。
- 不实现全局限流器、令牌桶或优先级队列；只保留 worker 并发、请求超时、context cancellation 和 429/5xx bounded retry。

## 本地运行

```bash
go run ./cmd/server
```

默认监听地址：

```text
127.0.0.1:8080
```

浏览器打开：

```text
http://127.0.0.1:8080
```

## 环境变量

```bash
MEMOS_IMPORTER_DB=memos-importer.db
MEMOS_IMPORTER_LISTEN_ADDR=127.0.0.1:8080
MEMOS_IMPORTER_ACCESS_PASSWORD=
MEMOS_IMPORTER_ALLOW_NO_PASSWORD=false
MEMOS_IMPORTER_MEMOS_ENDPOINT=http://127.0.0.1:5230
MEMOS_IMPORTER_MEMOS_TOKEN=
MEMOS_IMPORTER_NOTION_TOKEN=
MEMOS_IMPORTER_NOTION_TIME_SOURCE=created_time
MEMOS_IMPORTER_WORKERS=4
MEMOS_IMPORTER_REQUEST_TIMEOUT=30s
```

说明：

- `MEMOS_IMPORTER_DB`：本地 SQLite 数据库路径，用于保存映射关系、附件记录和导入任务状态。
- `MEMOS_IMPORTER_LISTEN_ADDR`：HTTP 监听地址。
- `MEMOS_IMPORTER_ACCESS_PASSWORD`：Web 控制台访问密码。监听非 loopback 地址时必须设置，除非显式设置了 `MEMOS_IMPORTER_ALLOW_NO_PASSWORD`。
- `MEMOS_IMPORTER_ALLOW_NO_PASSWORD`：设为 `1`/`true` 时，允许在非 loopback 地址上无密码启动。**不建议**——整个 API（导入任务历史，以及用调用方自带 endpoint/token 发起的外部请求）将无需鉴权即可访问。默认留空即为安全模式。
- `MEMOS_IMPORTER_MEMOS_ENDPOINT`：memos 实例根地址，例如 `https://memos.example.com`。以 `/api/v1` 结尾的地址也会被兼容处理。
- `MEMOS_IMPORTER_MEMOS_TOKEN`：可选的服务端默认 memos access token。公网或多人使用场景不建议设置。
- `MEMOS_IMPORTER_NOTION_TOKEN`：可选的服务端默认 Notion integration token。公网或多人使用场景不建议设置。
- `MEMOS_IMPORTER_NOTION_TIME_SOURCE`：默认时间来源，支持 `created_time`、`last_edited_time` 或 `property:<Notion date property name>`。
- `MEMOS_IMPORTER_WORKERS`：导入 worker 并发数。
- `MEMOS_IMPORTER_REQUEST_TIMEOUT`：memos API、Notion API 和附件下载请求超时时间。

当服务监听 `0.0.0.0` 或其他非本机回环地址时，必须设置 `MEMOS_IMPORTER_ACCESS_PASSWORD`（或用 `MEMOS_IMPORTER_ALLOW_NO_PASSWORD=1` 显式选择无密码开放部署）。Web 控制台会先加载访问密码面板，解锁后通过 HttpOnly same-origin session cookie 访问 API 和 SSE 进度流。脚本客户端也可以发送 `X-Memos-Importer-Password` 或 `Authorization: Bearer ...`。

## Docker

公开镜像为 `realliusha/memos-importer:latest`：

```bash
docker run --rm \
  -p 8080:8080 \
  -v memos-importer-data:/data \
  -e MEMOS_IMPORTER_ACCESS_PASSWORD='change-me' \
  -e MEMOS_IMPORTER_LISTEN_ADDR='0.0.0.0:8080' \
  -e MEMOS_IMPORTER_MEMOS_ENDPOINT='https://memos.example.com' \
  realliusha/memos-importer:latest
```

也可以在本地构建镜像：

```bash
docker build -t memos-importer:local .
```

容器镜像默认将 SQLite 数据库放在 `/data/memos-importer.db`，建议挂载持久化 volume。打开 Web 控制台后，每个浏览器会把自己的 memos / Notion 配置保存到本地。更多 Docker Hub 使用说明见 [README_docker.md](README_docker.md)。

## 构建

```bash
make build
```

该命令会先构建 Web 前端产物，再编译 `cmd/server`，输出到 `bin/memos-importer`。

## 验证

```bash
make test
make ui-smoke
```

- `make test`：运行 Go 测试，并检查 importer 核心不依赖 Notion adapter。
- `make ui-smoke`：构建 Web 前端，并用浏览器自动化走一遍控制台主流程。截图会写入 `tmp/ui-smoke.png` 和 `tmp/ui-smoke-mobile.png`。

## 真实端点验收

建议使用测试 memos 实例和测试 Notion 页面：

1. 启动 `cmd/server`。
2. 在 Web 控制台保存 memos 和 Notion 配置；配置会保存在当前浏览器本地。
3. 执行配置校验，确认 memos 版本至少为 `0.29.1`。
4. 导入一篇包含图片或文件的 Notion 页面。
5. 确认导入任务状态为 `done`。
6. 确认 memos 中创建的正文包含 `/file/attachments/{uid}/{filename}`。
7. 确认正文不包含 Notion 临时文件 URL。
8. 用 `skip` 和 `overwrite` 分别重复导入一次，确认不会创建重复 memo。

不要把 token、原始私密正文、附件内容或 Notion 临时文件 URL 写入 issue、日志或文档。

## 安全注意事项

- Web 控制台保存按钮只写入当前浏览器 `localStorage`，不会把 token 持久化到后端 SQLite。
- `GET /api/config` 不返回浏览器保存的 token；若部署者通过环境变量提供了服务端默认 token，也只返回脱敏值。
- 错误响应会对 Authorization header、token、签名 URL 和账号密码形式的 URL 做脱敏。
- 监听公网地址时必须设置访问密码。
- 建议只在可信网络和可信浏览器中运行，并使用专用的 memos access token 和 Notion integration。
