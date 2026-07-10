**English** | [简体中文](README_cn.md)

# memos-importer

memos-importer is a self-hosted console for importing Notion into
[memos](https://github.com/usememos/memos). It runs as a single Go binary with the web UI
embedded, and uses a local SQLite database to store mappings, attachment records, and
import job state. memos / Notion credentials are kept in each browser's `localStorage` by
default and are never written to the backend SQLite database.

The current goal is to import Notion pages or database entries into memos 0.29.1+ while
preserving the original document time, body structure, and Notion-hosted attachments as
faithfully as possible.

Hosted instance: <https://memos-importer.liusha.net>

## Features

- Save and validate the memos endpoint, memos access token, and Notion integration token
  locally in the browser.
- Fetch the pages and databases the Notion integration can access, and pick the import
  scope in the web UI.
- Preview a single Notion document's converted Markdown, attachment count, and unsupported
  block warnings.
- Notion-hosted `image/file/pdf/video` attachments are downloaded first, then uploaded to
  memos.
- Imported bodies reference memos `/file/attachments/{uid}/{filename}` paths and never
  depend on Notion's temporary file URLs.
- Use Notion `created_time` for the memo time by default; `last_edited_time` and
  `property:<Notion date property name>` are also supported.
- Choose the import visibility: `PRIVATE`, `PROTECTED`, or `PUBLIC`.
- Re-import policy: unchanged content is skipped automatically; changed content can be
  `skip`ped or `overwrite`n.
- Import jobs run asynchronously; the web UI shows live progress, history, per-item detail,
  failure reasons, and warnings.
- Cancel, resume, and retry failed items.
- Never talks to the memos database directly — data is written only through the memos
  `/api/v1/*` API.

## Non-goals

- No compatibility with memos versions before 0.29.1.
- No two-way sync, scheduled sync, or multi-user / multi-tenant support.
- v1 supports Notion only — no Markdown directories, Flomo, or other data sources.
- No promise to reproduce every Notion block; unsupported blocks are preserved as warnings
  and visible placeholders.
- No global rate limiter, token bucket, or priority queue — only worker concurrency,
  request timeouts, context cancellation, and bounded 429/5xx retries.

## Run locally

```bash
go run ./cmd/server
```

Default listen address:

```text
127.0.0.1:8080
```

Open in a browser:

```text
http://127.0.0.1:8080
```

## Environment variables

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

- `MEMOS_IMPORTER_DB`: local SQLite path for mappings, attachment records, and import job
  state.
- `MEMOS_IMPORTER_LISTEN_ADDR`: HTTP listen address.
- `MEMOS_IMPORTER_ACCESS_PASSWORD`: web console access password. **Required** when listening
  on a non-loopback address.
- `MEMOS_IMPORTER_MEMOS_ENDPOINT`: memos instance root, e.g. `https://memos.example.com`.
  Addresses ending in `/api/v1` are also accepted.
- `MEMOS_IMPORTER_MEMOS_TOKEN`: optional server-side default memos access token. Not
  recommended for public or multi-user deployments.
- `MEMOS_IMPORTER_NOTION_TOKEN`: optional server-side default Notion integration token. Not
  recommended for public or multi-user deployments.
- `MEMOS_IMPORTER_NOTION_TIME_SOURCE`: default time source — `created_time`,
  `last_edited_time`, or `property:<Notion date property name>`.
- `MEMOS_IMPORTER_WORKERS`: import worker concurrency.
- `MEMOS_IMPORTER_REQUEST_TIMEOUT`: timeout for memos API, Notion API, and attachment
  download requests.

When the service listens on `0.0.0.0` or any non-loopback address, you **must** set
`MEMOS_IMPORTER_ACCESS_PASSWORD`. The web console first loads an access-password panel; once
unlocked, it reaches the API and SSE progress stream through an HttpOnly same-origin session
cookie. Script clients may instead send `X-Memos-Importer-Password` or
`Authorization: Bearer ...`.

## Docker

The public image is `realliusha/memos-importer:latest`:

```bash
docker run --rm \
  -p 8080:8080 \
  -v memos-importer-data:/data \
  -e MEMOS_IMPORTER_ACCESS_PASSWORD='change-me' \
  -e MEMOS_IMPORTER_LISTEN_ADDR='0.0.0.0:8080' \
  -e MEMOS_IMPORTER_MEMOS_ENDPOINT='https://memos.example.com' \
  realliusha/memos-importer:latest
```

You can also build the image locally:

```bash
docker build -t memos-importer:local .
```

The container stores the SQLite database at `/data/memos-importer.db` by default — mount a
persistent volume for it. After opening the web console, each browser saves its own memos /
Notion configuration locally. See [README_docker.md](README_docker.md) for more Docker Hub
usage notes.

## Build

```bash
make build
```

This builds the web frontend first, then compiles `cmd/server` into `bin/memos-importer`.

## Verify

```bash
make test
make ui-smoke
```

- `make test`: run the Go tests and check that the importer core does not depend on the
  Notion adapter.
- `make ui-smoke`: build the web frontend and drive the main console flow with browser
  automation. Screenshots are written to `tmp/ui-smoke.png` and `tmp/ui-smoke-mobile.png`.

## Acceptance against a real endpoint

Use a test memos instance and a test Notion page:

1. Start `cmd/server`.
2. Save the memos and Notion configuration in the web console; it is stored locally in the
   current browser.
3. Run configuration validation and confirm the memos version is at least `0.29.1`.
4. Import a Notion page that contains an image or a file.
5. Confirm the import job reaches state `done`.
6. Confirm the created memo body contains `/file/attachments/{uid}/{filename}`.
7. Confirm the body contains no Notion temporary file URLs.
8. Re-import once with `skip` and once with `overwrite`, and confirm no duplicate memo is
   created.

Do not write tokens, raw private bodies, attachment contents, or Notion temporary file URLs
into issues, logs, or docs.

## Security notes

- The web console save button only writes to the current browser's `localStorage`; it never
  persists tokens to the backend SQLite database.
- `GET /api/config` does not return browser-saved tokens; if the operator provides
  server-side default tokens via environment variables, only redacted values are returned.
- Error responses redact Authorization headers, tokens, signed URLs, and credential-bearing
  URLs.
- An access password is required when listening on a public address.
- Run it only on trusted networks and trusted browsers, and use a dedicated memos access
  token and Notion integration.
