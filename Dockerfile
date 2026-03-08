# ── Stage 1: Build ──────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

# 安装构建依赖
RUN apk --no-cache add git

WORKDIR /app

# 设置国内 Go 代理（解决 proxy.golang.org 访问超时）
ENV GOPROXY=https://goproxy.cn,https://goproxy.io,direct
ENV GONOSUMCHECK=*

# 先拷贝依赖文件，充分利用 Docker 层缓存
COPY go.mod go.sum ./
RUN go mod download

# 拷贝源码并编译（静态链接，不依赖 glibc）
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s" -o optitree .

# ── Stage 2: Runtime ─────────────────────────────────────────────────────────
FROM alpine:3.21

# ca-certificates: TLS 支持；tzdata: 时区支持
RUN apk --no-cache add ca-certificates tzdata && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone && \
    apk del tzdata

WORKDIR /app

# 从构建阶段拷贝二进制和配置
COPY --from=builder /app/optitree .
COPY --from=builder /app/configs ./configs

# 存储目录（上传文件）
RUN mkdir -p ./storage

EXPOSE 8080

CMD ["./optitree"]
