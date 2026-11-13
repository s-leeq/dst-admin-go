#!/usr/bin/env bash
# 本地冒烟测试：不启动游戏，只验证镜像里的关键部件是否就位。
# 用法: ./verify.sh <image:tag>
set -euo pipefail
image=${1:-dst-admin-go:latest-arm64}

echo "== 架构 =="
docker image inspect "$image" --format '{{.Os}}/{{.Architecture}} size={{.Size}}'

# 注意用 -c 而不是 -lc：login shell 会重读 /etc/profile 覆盖镜像里设好的 PATH
echo "== PATH / box64 =="
docker run --rm --platform linux/arm64 --entrypoint /bin/bash "$image" -c \
  'echo "PATH=$PATH" | tr ":" "\n" | head -3; /opt/box64/bin/box64 --version | head -2'

echo "== box64 翻译 x86_64 程序（用 amd64 busybox 验证，需要网络） =="
docker run --rm --platform linux/arm64 --entrypoint /bin/bash "$image" -c '
  set -e
  apt-get update -qq >/dev/null 2>&1
  cd /tmp && apt-get download busybox:amd64 >/dev/null 2>&1
  dpkg-deb -x busybox_*.deb /tmp/bb
  /opt/box64/bin/box64 /tmp/bb/bin/busybox uname -m
' 2>&1 | tail -2
# 注：box64 对 Go 编译的 x86_64 程序有已知 pthread 兼容问题，与 DST（C++/LuaJIT）无关

echo "== x86_64 运行库 =="
docker run --rm --platform linux/arm64 --entrypoint /bin/bash "$image" -c \
  'ls -1 /usr/lib/x86_64-linux-gnu/libc.so.6 /usr/lib/x86_64-linux-gnu/libstdc++.so.6 /usr/lib/x86_64-linux-gnu/libcurl.so.4 >/dev/null && echo ok'

echo "== DepotDownloader（.NET 8 arm64 原生） =="
docker run --rm --platform linux/arm64 --entrypoint /bin/bash "$image" -c \
  '/opt/DepotDownloader/DepotDownloader 2>&1 | head -2 || true'

echo "== steamcmd shim 语法 =="
docker run --rm --platform linux/arm64 --entrypoint /bin/bash "$image" -c \
  'bash -n /app/steamcmd/steamcmd.sh && echo syntax-ok'

echo "== 面板二进制 =="
docker run --rm --platform linux/arm64 --entrypoint /bin/bash "$image" -c \
  'ls -l /app/dst-admin-go && /app/dst-admin-go --help 2>&1 | head -3 || true'

echo "== 前端产物与默认配置 =="
docker run --rm --platform linux/arm64 --entrypoint /bin/bash "$image" -c \
  'ls /app/dist/index.html >/dev/null && echo dist-ok; grep -E "^bin=|^persistent_storage_root=" /app/docker_dst_config.default'

echo "全部检查完成"
