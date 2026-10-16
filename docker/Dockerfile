# OmniBot 本地部署镜像(26 号基础设施调整:服务生命周期交给 Docker)。
# 多阶段:前端 dist → 嵌入后端 → 运行时(node 供 lark-cli;docker.sock 供 sandbox_run)。
# 运行时依赖以挂载注入(configs/、~/.npm-global),镜像内不含任何凭证。
FROM docker.1panel.live/library/node:20-alpine AS frontend
WORKDIR /build
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM docker.1panel.live/library/golang:1.27-alpine AS backend
WORKDIR /build
# 国内网络:go 模块走 goproxy.cn(直连 proxy.golang.org 被墙)
ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# 用本阶段构建的前端产物覆盖 embed 源目录(前端 //go:embed dist/*)
COPY --from=frontend /build/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server/

FROM docker.1panel.live/library/node:20-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata \
	&& rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=backend /out/server ./bin/server
# TZ:容器默认 UTC,服务内 cron/日期换算依赖本地时区,与宿主保持一致(Asia/Shanghai)
ENV TZ=Asia/Shanghai
ENV PATH="/opt/npm-global/bin:${PATH}"
EXPOSE 8090
CMD ["./bin/server", "-config", "configs/config.docker.yaml"]
