FROM node:20-alpine AS web
WORKDIR /src
COPY webapp/package.json webapp/package-lock.json ./webapp/
RUN cd webapp && npm ci
COPY webapp ./webapp
RUN cd webapp && npm run build

FROM golang:1.25-alpine AS build
WORKDIR /src
# GOPROXY is overridable at build time; the server build passes goproxy.cn because
# proxy.golang.org is unreliable from there. Plain `docker build` keeps the default.
ARG GOPROXY
ENV GOPROXY=${GOPROXY:-https://proxy.golang.org,direct}
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN go build -o /out/memos-importer ./cmd/server

FROM alpine:3.20
RUN adduser -D -H memos && mkdir -p /app /data && chown -R memos:memos /app /data
USER memos
WORKDIR /app
ENV MEMOS_IMPORTER_DB=/data/memos-importer.db \
    MEMOS_IMPORTER_LISTEN_ADDR=0.0.0.0:8080
COPY --from=build --chown=memos:memos /out/memos-importer /app/memos-importer
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/memos-importer"]
