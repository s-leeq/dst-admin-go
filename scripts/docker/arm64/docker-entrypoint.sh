#!/bin/bash
# linux/arm64 容器入口：与 scripts/docker/docker-entrypoint.sh 行为一致，
# 只把「用 steamcmd 下载游戏本体」换成「用 DepotDownloader 下载 linux64 depot」。

ulimit -Sn 10000 2>/dev/null || true

data_dir='/app/data'
dst_server='/app/dst-dedicated-server'
depot='/opt/DepotDownloader/DepotDownloader'
server_bin="$dst_server/bin64/dontstarve_dedicated_server_nullrenderer_x64"

mkdir -p "$data_dir"
if [ ! -f "$data_dir/dst_config" ]; then
  cp /app/docker_dst_config.default "$data_dir/dst_config"
fi
if [ ! -f "$data_dir/password.txt" ]; then
  printf 'username=admin\npassword=123456\ndisplayName=admin\nphotoURL=xxx\n' > "$data_dir/password.txt"
fi
mkdir -p "$data_dir/backup" "$data_dir/mod" "$data_dir/DoNotStarveTogether/Cluster_1"

mkdir -p "$dst_server"
retry=1
while [ ! -e "$server_bin" ]; do
  if [ $retry -gt 3 ]; then
    echo "Download Dont Starve Together Server failed after three times"
    exit -2
  fi
  echo "Not found Dont Starve Together Server, start to installing, try: ${retry}"
  "$depot" -app 343050 -os linux -osarch 64 -dir "$dst_server" || true
  sleep 3
  ((retry++))
done

# 翻译层执行的可执行位要显式补上（DepotDownloader 落盘后可能没有 +x）
chmod 755 "$dst_server"/bin64/* 2>/dev/null || true

echo "DepotDownloader installed at /opt/DepotDownloader"
echo "DST dedicated server installed at $dst_server"
echo "box64: $(box64 --version 2>/dev/null | head -1)"

cd /app
exec ./dst-admin-go
