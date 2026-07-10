# memos-importer

Self-hosted Web console for importing Notion pages and database entries into memos 0.29.1+.

The image runs a single Go binary with the Web UI embedded. It stores mappings, attachment records, and import job state in SQLite. User memos and Notion credentials are saved in each browser's `localStorage` by default, not in the backend SQLite database.

## Features

- Import Notion pages and database entries into memos.
- Preview converted Markdown before importing.
- Preserve the selected Notion timestamp as the memo `create_time`.
- Download Notion-hosted `image/file/pdf/video` attachments and upload them to memos.
- Replace Notion temporary file URLs with memos `/file/attachments/{uid}/{filename}` paths.
- Choose memo visibility: `PRIVATE`, `PROTECTED`, or `PUBLIC`.
- Run imports as background jobs with live progress.
- Resume interrupted jobs and retry failed items.
- Skip unchanged documents or overwrite changed documents without creating duplicate memos.

## Quick Start

Replace the image name with the published Docker Hub image name once it is released.

```bash
docker run -d \
  --name memos-importer \
  -p 8080:8080 \
  -v memos-importer-data:/data \
  -e MEMOS_IMPORTER_ACCESS_PASSWORD='change-me' \
  -e MEMOS_IMPORTER_MEMOS_ENDPOINT='https://memos.example.com' \
  your-dockerhub-namespace/memos-importer:latest
```

Open:

```text
http://localhost:8080
```

The Web console will ask for `MEMOS_IMPORTER_ACCESS_PASSWORD` before API requests are allowed.

## Docker Compose

```yaml
services:
  memos-importer:
    image: your-dockerhub-namespace/memos-importer:latest
    container_name: memos-importer
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - memos-importer-data:/data
    environment:
      MEMOS_IMPORTER_ACCESS_PASSWORD: "change-me"
      MEMOS_IMPORTER_MEMOS_ENDPOINT: "https://memos.example.com"

volumes:
  memos-importer-data:
```

## Environment Variables

| Variable | Default in image | Description |
| --- | --- | --- |
| `MEMOS_IMPORTER_DB` | `/data/memos-importer.db` | SQLite database path used for mappings, attachment records, and import jobs. |
| `MEMOS_IMPORTER_LISTEN_ADDR` | `0.0.0.0:8080` | HTTP listen address inside the container. |
| `MEMOS_IMPORTER_ACCESS_PASSWORD` | empty | Required when listening on `0.0.0.0`. Protects the Web console and API. |
| `MEMOS_IMPORTER_MEMOS_ENDPOINT` | empty | Root URL of your memos instance, for example `https://memos.example.com`. |
| `MEMOS_IMPORTER_MEMOS_TOKEN` | empty | Optional server-side default memos access token. Do not set it for public or multi-user deployments. |
| `MEMOS_IMPORTER_NOTION_TOKEN` | empty | Optional server-side default Notion integration token. Do not set it for public or multi-user deployments. |
| `MEMOS_IMPORTER_NOTION_TIME_SOURCE` | `created_time` | Default timestamp source: `created_time`, `last_edited_time`, or `property:<Notion date property name>`. |
| `MEMOS_IMPORTER_WORKERS` | `4` | Import worker concurrency. |
| `MEMOS_IMPORTER_REQUEST_TIMEOUT` | `30s` | Timeout for memos API calls, Notion API calls, and Notion-hosted attachment downloads. |

For the default browser-local workflow, leave the memos and Notion token variables empty. Each browser stores its own credentials locally and sends them only with API requests that need them. `GET /api/config` does not return browser-saved credentials; server-side default tokens, if configured, are returned masked only.

## Persistent Data

Mount `/data` to keep the SQLite database across container restarts:

```bash
-v memos-importer-data:/data
```

The database stores:

- Notion document to memos memo mappings,
- attachment mappings,
- import job history and item status.

Do not delete the volume if you want duplicate detection, resume, and retry behavior to keep working across restarts.

## Security Notes

- Always set a strong `MEMOS_IMPORTER_ACCESS_PASSWORD` when publishing the container port.
- Use dedicated memos and Notion tokens with the minimum permissions required.
- Browser-saved credentials live in `localStorage` for this origin. Use HTTPS and trusted devices when exposing the console outside localhost.
- Do not put tokens in public compose files, screenshots, logs, or issue reports.
- Run behind HTTPS when exposing the Web console outside localhost.
- The application redacts Authorization headers, token-like values, signed URLs, and credentials from API error responses.

## Compatibility

- memos: `0.29.1+`
- Source: Notion only in v1
- Import target: memos API `/api/v1/*`
- Database access: no direct memos database access

## Build Locally

```bash
docker build -t memos-importer:local .
```

Run the local image:

```bash
docker run --rm \
  -p 8080:8080 \
  -v memos-importer-data:/data \
  -e MEMOS_IMPORTER_ACCESS_PASSWORD='change-me' \
  memos-importer:local
```

## Known Limits

- Notion is the only source supported in v1.
- Not all Notion block types are fully converted. Unsupported blocks are kept visible as warnings and placeholders.
- The importer is not a bidirectional sync engine.
- There is no multi-user or multi-tenant permission model.
