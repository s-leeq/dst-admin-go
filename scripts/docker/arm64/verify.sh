#!/usr/bin/env bash
# 冒烟测试：验证镜像里各组件真的能跑，而不是"文件存在"。
# 任一检查失败即以非 0 退出（早期版本用管道 + || true，会把失败吞掉）。
# 用法: ./verify.sh <image:tag>
set -uo pipefail

image=${1:-dst-admin-go:latest-arm64}
cid=""

cleanup() {
  if [ -n "$cid" ]; then
    docker rm -f "$cid" >/dev/null 2>&1
  fi
  return 0
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

run() {
  docker run --rm --platform linux/arm64 --entrypoint /bin/bash "$image" -c "$1"
}

echo "== 架构 =="
arch=$(docker image inspect "$image" --format '{{.Os}}/{{.Architecture}}' 2>/dev/null) || fail "镜像不存在: $image"
[ "$arch" = "linux/arm64" ] || fail "架构不是 linux/arm64: $arch"
echo "ok: $arch"

echo "== box64 =="
out=$(run '/opt/box64/bin/box64 --version' 2>&1) || fail "box64 执行失败"
echo "$out" | grep -q '^Box64 arm64' || fail "box64 版本输出异常: $out"
echo "ok: $(echo "$out" | head -1)"

echo "== x86_64 运行库 =="
run 'test -e /usr/lib/x86_64-linux-gnu/libc.so.6 \
  && test -e /usr/lib/x86_64-linux-gnu/libstdc++.so.6 \
  && test -e /usr/lib/x86_64-linux-gnu/libgcc_s.so.1 \
  && test -e /usr/lib/x86_64-linux-gnu/libcurl.so.4' || fail "缺少 box64 需要的 x86_64 运行库"
echo ok

echo "== box64 翻译 x86_64 程序（amd64 busybox，需要网络） =="
run 'set -e
  cd /tmp
  apt-get update -qq >/dev/null 2>&1
  apt-get download busybox:amd64 >/dev/null 2>&1
  dpkg-deb -x busybox_*.deb /tmp/bb
  /opt/box64/bin/box64 /tmp/bb/bin/busybox uname -m' >/tmp/verify-box64.out 2>/dev/null \
  || fail "box64 无法运行动态链接的 x86_64 程序（详见 /tmp/verify-box64.out）"
grep -qx 'x86_64' /tmp/verify-box64.out || fail "box64 输出异常: $(tail -3 /tmp/verify-box64.out)"
echo "ok: uname -m -> x86_64"

echo "== DepotDownloader（自带运行时，不应依赖系统 dotnet） =="
run 'test ! -d /usr/share/dotnet || exit 1
  /opt/DepotDownloader/DepotDownloader 2>&1 | grep -q "DepotDownloader v"' || fail "DepotDownloader 不可执行或仍依赖系统 dotnet"
echo ok

echo "== steamcmd shim：参数边界 + 原子落盘 =="
run 'set -e
  base="/tmp/mod root"
  rm -rf "$base"; mkdir -p "$base"

  # 成功路径：假下载器在 -dir 指定的临时目录里造文件
  cat > /tmp/ok-depot <<"EOF"
#!/bin/sh
prev=""
for a in "$@"; do
  [ "$prev" = "-dir" ] && { mkdir -p "$a"; echo "-- modinfo" > "$a/modinfo.lua"; }
  prev="$a"
done
EOF
  chmod +x /tmp/ok-depot

  out=$(DEPOT=/tmp/ok-depot bash /app/steamcmd/steamcmd.sh \
        "+login anonymous" "+force_install_dir" "$base" \
        "+workshop_download_item 322330 123456" "+quit")

  echo "$out" | grep -qF "Downloaded item 123456 to \"$base/steamapps/workshop/content/322330/123456\""
  test -f "$base/steamapps/workshop/content/322330/123456/modinfo.lua"
  # 临时目录必须已被改名，不留残留
  test -z "$(ls -A "$base/steamapps/workshop/content/322330" | grep partial || true)"

  # 失败路径：假下载器失败，不能留下会被面板误判为"已下载"的目录
  cat > /tmp/fail-depot <<"EOF"
#!/bin/sh
prev=""
for a in "$@"; do
  [ "$prev" = "-dir" ] && { mkdir -p "$a"; echo half > "$a/partial.bin"; }
  prev="$a"
done
exit 1
EOF
  chmod +x /tmp/fail-depot

  if DEPOT=/tmp/fail-depot bash /app/steamcmd/steamcmd.sh \
       "+login anonymous" "+force_install_dir" "$base/other root" \
       "+workshop_download_item 322330 999" "+quit" >/dev/null 2>&1; then
    exit 1
  fi
  test ! -e "$base/other root/steamapps/workshop/content/322330/999"
  test -z "$(ls -A "$base/other root/steamapps/workshop/content/322330" 2>/dev/null | grep partial || true)"' \
  || fail "shim 行为不符合预期（参数边界 / 原子落盘 / 失败清理）"
echo ok

echo "== 面板 HTTP 冒烟 =="
cid=$(docker run -d --platform linux/arm64 -p 127.0.0.1::8082 \
  --entrypoint /bin/bash "$image" -c \
  'mkdir -p /app/data && cp /app/docker_dst_config.default /app/data/dst_config && cd /app && exec ./dst-admin-go') \
  || fail "容器启动失败"
sleep 1
port=$(docker port "$cid" 8082/tcp 2>/dev/null | head -1 | sed 's/.*://')
[ -n "$port" ] || fail "无法获取映射端口"
okcode=""
for _ in $(seq 1 30); do
  okcode=$(curl -s -o /dev/null -w '%{http_code}' -m 2 "http://127.0.0.1:$port/" || true)
  [ "$okcode" = "200" ] && break
  sleep 0.5
done
[ "$okcode" = "200" ] || { docker logs "$cid" 2>&1 | tail -10 >&2; fail "面板没有在 :8082 正常响应（最后状态 $okcode）"; }
echo "ok: GET / -> 200"
docker rm -f "$cid" >/dev/null 2>&1
cid=""

echo "== 前端产物与默认配置 =="
run 'test -f /app/dist/index.html \
  && grep -qx "bin=2664" /app/docker_dst_config.default \
  && grep -qx "persistent_storage_root=/app/data" /app/docker_dst_config.default' \
  || fail "dist 或默认配置不符合预期"
echo ok

echo "全部检查通过"
