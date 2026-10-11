FROM golang:1.27.2-alpine

WORKDIR /app

# 安装必要的工具
RUN apk add --no-cache git nodejs npm chromium font-noto ttf-dejavu su-exec

# 复制go.mod和go.sum
COPY go.mod go.sum ./

# 下载依赖
RUN go mod download

# 复制源代码
COPY . .

RUN npm ci --prefix /app/scripts/quality-renderer --omit=dev --ignore-scripts && \
    npm cache clean --force
ENV QUALITY_CHROMIUM_EXECUTABLE_PATH=/usr/bin/chromium-browser \
    QUALITY_CHROMIUM_SANDBOX=false

# 构建应用
RUN VERSION_VALUE="$(./scripts/resolve-version.sh)" && \
    go build -ldflags="-s -w -X main.Version=${VERSION_VALUE}" -o main ./cmd/server/

RUN addgroup -g 1000 sub2api && \
    adduser -u 1000 -G sub2api -s /bin/sh -D sub2api && \
    mkdir -p /app/data && chown sub2api:sub2api /app/data
RUN su-exec sub2api node /app/scripts/quality-renderer/smoke.mjs
USER sub2api

# 暴露端口
EXPOSE 8080

# 运行应用
CMD ["./main"]
