# Woodpecker 后端镜像（api 与 worker 共用，--mode 区分）。
# 构建上下文 = 仓库根目录：docker build -t woodpecker-server .
#
# go-git（纯 Go Git 实现）+ CGO_ENABLED=0：无 cgo 依赖，产出静态二进制；
# go-enry 无 oniguruma 时自动降级纯 Go 语言检测。
FROM golang:1.27-bookworm AS builder

WORKDIR /src
# 依赖层缓存：先拷贝 go.mod/go.sum 下载依赖，再拷贝源码
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/woodpecker ./cmd/server

# 运行镜像：alpine + CA 证书（模型 API / Git HTTPS 需要）+ tzdata（中文时间显示）
FROM alpine:3.21

RUN addgroup -g 10001 woodpecker \
 && adduser -u 10001 -G woodpecker -D -H woodpecker \
 && apk add --no-cache ca-certificates tzdata wget

COPY --from=builder /out/woodpecker /usr/local/bin/woodpecker

# 上传/staging 目录（api 与 worker 必须挂同一卷）
RUN mkdir -p /data/uploads && chown -R woodpecker:woodpecker /data
USER woodpecker

WORKDIR /data
ENV UPLOAD_DIR=/data/uploads \
    APP_ENV=production

EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:${APP_PORT:-8080}/healthz >/dev/null 2>&1 || exit 1

ENTRYPOINT ["woodpecker"]
CMD ["--mode=all"]
