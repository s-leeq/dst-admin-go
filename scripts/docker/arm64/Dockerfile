# syntax=docker/dockerfile:1
#
# dst-admin-go —— linux/arm64 镜像（Apple Silicon / ARM 服务器）
#
# 与 scripts/docker/Dockerfile（amd64）的差异：
#   1. 面板二进制用 GOARCH=arm64 编译（CGO_ENABLED=0，纯静态，跨架构无痛）；
#   2. 饥荒服务端只有 x86_64 二进制，用 box64 动态翻译执行 bin64/_x64；
#   3. Valve 只发布 x86 版 steamcmd，arm64 改用 DepotDownloader（自带 .NET 运行时，
#      不需要系统装 dotnet），并由 steamcmd-shim.sh 冒充 steamcmd.sh，让面板的
#      「更新游戏 / 订阅模组」照常可用。
#
# 构建上下文 = 仓库根（需要 go.mod / static / config.yml / dist）：
#   bash scripts/docker/arm64/build.sh
#
# 兼容两种源码布局：
#   - v2 之后（有 cmd/、internal/）：go build ./cmd/server
#   - v2 之前（1.6.1 一类，main.go 在根）：go build .

ARG GO_VERSION=1.24

# =============================================================================
# 阶段 1：编译 arm64 面板二进制
# =============================================================================
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS panel
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} \
    CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=arm64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN if [ -d cmd ]; then \
      go build -trimpath -ldflags="-s -w" -o /out/dst-admin-go ./cmd/server; \
    else \
      go build -trimpath -ldflags="-s -w" -o /out/dst-admin-go .; \
    fi

# =============================================================================
# 阶段 2：基础层（系统工具 + box64 需要的 x86_64 运行库）
#
# box64 执行 x86_64 程序时必须能找到目标架构的 libc/libstdc++/libgcc/libcurl，
# 而 Debian 的 arm64 归档里没有 amd64 包，只能显式引入 amd64 源。
# 这些库在构建期装好，容器启动时不再联网。
# =============================================================================
FROM debian:bookworm-slim AS base
ENV DEBIAN_FRONTEND=noninteractive
RUN dpkg --add-architecture amd64 \
 && echo 'deb [arch=amd64] http://deb.debian.org/debian bookworm main' > /etc/apt/sources.list.d/amd64.list \
 && echo 'deb [arch=amd64] http://deb.debian.org/debian bookworm-updates main' >> /etc/apt/sources.list.d/amd64.list \
 && echo 'deb [arch=amd64] http://deb.debian.org/debian-security bookworm-security main' >> /etc/apt/sources.list.d/amd64.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl wget screen procps sudo unzip tzdata \
      libc6:amd64 libstdc++6:amd64 libgcc-s1:amd64 libcurl4:amd64 \
 && rm -rf /var/lib/apt/lists/*

FROM base AS box64
RUN apt-get update \
 && apt-get install -y --no-install-recommends git cmake build-essential python3 \
 && git clone --depth 1 https://github.com/ptitSeb/box64 /src/box64 \
 && cmake -S /src/box64 -B /src/box64/build \
      -DARM_DYNAREC=ON \
      -DCMAKE_BUILD_TYPE=RelWithDebInfo \
      -DCMAKE_INSTALL_PREFIX=/opt/box64 \
 && cmake --build /src/box64/build -j"$(nproc)" \
 && cmake --install /src/box64/build \
 && rm -rf /src/box64 /var/lib/apt/lists/*

# =============================================================================
# 阶段 3：最终镜像
# =============================================================================
FROM base
ARG DEPOTDOWNLOADER_VERSION=3.4.0
ARG BAKE_GAME_SERVER=false

ENV TZ=Asia/Shanghai \
    DST_ADMIN_CONTAINER_MODE=true \
    PATH=/opt/box64/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo "$TZ" > /etc/timezone

# DepotDownloader（arm64）：替代 steamcmd 下载游戏本体与创意工坊物品。
# release 里是 self-contained 单文件（自带 .NET 运行时），实测在无 dotnet 的
# 纯 debian:bookworm-slim 里可直接运行，所以不需要再装 dotnet-runtime。
RUN wget -q -O /tmp/dd.zip \
      "https://github.com/SteamRE/DepotDownloader/releases/download/DepotDownloader_${DEPOTDOWNLOADER_VERSION}/DepotDownloader-linux-arm64.zip" \
 && unzip -q /tmp/dd.zip -d /opt/DepotDownloader \
 && rm -f /tmp/dd.zip \
 && chmod 755 /opt/DepotDownloader/DepotDownloader \
 && /opt/DepotDownloader/DepotDownloader 2>&1 | head -1

COPY --from=box64 /opt/box64 /opt/box64

WORKDIR /app
COPY --from=panel /out/dst-admin-go /app/dst-admin-go
COPY scripts/docker/arm64/docker-entrypoint.sh /app/docker-entrypoint.sh
COPY scripts/docker/arm64/steamcmd-shim.sh /app/steamcmd/steamcmd.sh
COPY scripts/docker/arm64/docker_dst_config /app/docker_dst_config.default
COPY config.yml /app/config.yml
COPY static /app/static
COPY dist /app/dist
RUN chmod 755 /app/dst-admin-go /app/docker-entrypoint.sh /app/steamcmd/steamcmd.sh \
 && mkdir -p /app/data /app/dst-dedicated-server

# 可选：把游戏本体（约 4.5GB）烘进镜像，构建期只下一次，运行期完全离线
RUN if [ "$BAKE_GAME_SERVER" = "true" ]; then \
      /opt/DepotDownloader/DepotDownloader -app 343050 -os linux -osarch 64 \
        -dir /app/dst-dedicated-server -validate; \
    fi

EXPOSE 8082/tcp 10888/udp 10998/udp 10999/udp

ENTRYPOINT ["./docker-entrypoint.sh"]
