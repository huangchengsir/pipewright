# 双运行模式之容器形态。最终镜像 = distroless static + 单静态二进制(前端已 go:embed)。
# 注:Story 1.1 阶段本机未装 Docker,此文件作为脚手架交付,容器构建/运行的验证延后。

# ---- 前端构建 ----
FROM node:22-alpine AS web
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm config set registry https://registry.npmmirror.com && npm ci
COPY web/ ./
RUN npm run build

# ---- Go 静态构建 ----
FROM golang:1.26-alpine AS build
ENV CGO_ENABLED=0 \
    GOPROXY=https://goproxy.cn,direct \
    GOSUMDB=off \
    GOTOOLCHAIN=local
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /app/web/dist ./web/dist
RUN go build -o /devopstool ./cmd/devopstool

# ---- 运行镜像 ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /devopstool /devopstool
EXPOSE 8080
ENTRYPOINT ["/devopstool"]
