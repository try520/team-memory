# 多阶段构建：静态编译，产物为单个二进制，不依赖 CGO。
# 基础镜像可用 --build-arg 覆盖（默认官方名，拉取走 daemon 的 registry-mirrors）：
#   docker build --build-arg GO_IMAGE=... --build-arg RUNTIME_IMAGE=... .
ARG GO_IMAGE=golang:1.23-alpine
ARG RUNTIME_IMAGE=alpine:3.20

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/team-memory ./cmd/memorysvc

FROM ${RUNTIME_IMAGE}
# su-exec 供入口脚本从 root 降权到 memory；RUNTIME_IMAGE 必须是带 apk 的 Alpine 系镜像。
RUN apk add --no-cache su-exec tzdata \
    && adduser -D -u 10001 memory \
    && mkdir -p /data \
    && chown memory:memory /data
COPY --from=build /out/team-memory /usr/local/bin/team-memory
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod 0755 /usr/local/bin/docker-entrypoint.sh
ENV DATA_DIR=/data \
    PORT=8080
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=3s \
    CMD wget -qO- "http://127.0.0.1:${PORT}/healthz" >/dev/null || exit 1
# 容器以 root 进入口脚本：先把挂载进来的数据目录 chown 给 memory，再降权执行服务。
# 不能直接 USER memory，否则宿主机 root:root 的 ./data 在 mkdir projects 时会被拒绝。
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
