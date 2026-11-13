#!/bin/bash
# =============================================================================
# steamcmd 的 arm64 替身（镜像内落到 /app/steamcmd/steamcmd.sh）
#
# Valve 只发布 x86 版 steamcmd，arm64 上跑不了；这里把面板发出的 steamcmd
# 语法翻译成 DepotDownloader（arm64 原生）调用，同时保持面板的解析契约。
#
# 面板实际会发出的两种调用：
#   1) 更新游戏（internal/service/dstPath/linux_dst.go）
#      ./steamcmd.sh +login anonymous +force_install_dir DIR +app_update 343050 [ -beta updatebeta ] validate +quit
#   2) 订阅/读取模组（internal/service/mod/mod_service.go）
#      ./steamcmd.sh "+login anonymous" "+force_install_dir" DIR "+workshop_download_item 322330 MODID" "+quit"
#      注意第四个参数把子命令和两个值塞进了同一个 argv，而 DIR 是独立 argv
#      （可能含空格），所以只能拆「+ 开头的打包参数」，绝不能整体重新分词。
#
# 面板用正则  Downloaded item \d+ to "([^"]+)"  从输出里取模组落盘路径；
# 面板还把「目录已存在」当成「已下载过」，因此必须先下到临时目录、成功后再原子
# 改名，失败时把临时目录清掉，避免留下空目录导致永久跳过。
# =============================================================================
set -u

DEPOT=${DEPOT:-/opt/DepotDownloader/DepotDownloader}

# 只拆分「+ 开头的打包参数」，其余 argv（尤其是路径）保持原样，不破坏边界
tokens=()
for arg in "$@"; do
  case "$arg" in
    "+"*)
      read -r -a parts <<< "$arg"
      tokens+=("${parts[@]}")
      ;;
    *)
      tokens+=("$arg")
      ;;
  esac
done
set -- ${tokens[@]+"${tokens[@]}"}

install_dir=""
app_id=""
branch=""
validate=""
workshop_app=""
workshop_id=""

while [ $# -gt 0 ]; do
  case "$1" in
    +force_install_dir)      install_dir="${2:-}"; shift 2 ;;
    +app_update)             app_id="${2:-}"; shift 2 ;;
    +workshop_download_item) workshop_app="${2:-}"; workshop_id="${3:-}"; shift 3 ;;
    -beta)                   branch="${2:-}"; shift 2 ;;
    validate)                validate="-validate"; shift ;;
    *)                       shift ;;
  esac
done

# ---- 分支 1：创意工坊物品（模组） ----
if [ -n "$workshop_id" ]; then
  [ -z "$workshop_app" ] && workshop_app=322330
  root="${install_dir:-/app/data/mod}"
  parent="$root/steamapps/workshop/content/$workshop_app"
  out="$parent/$workshop_id"
  tmp="$parent/.$workshop_id.partial.$$"

  mkdir -p "$parent"
  rm -rf "$tmp"
  echo "steamcmd-shim: depot download workshop item $workshop_id (app $workshop_app) -> $out"
  if ! "$DEPOT" -app "$workshop_app" -pubfile "$workshop_id" -dir "$tmp"; then
    rm -rf "$tmp"
    echo "steamcmd-shim: DepotDownloader failed for workshop item $workshop_id" >&2
    exit 1
  fi
  # 原子发布：成功后才让 <id> 目录出现，面板的「已下载」判断不会被半成品骗过
  rm -rf "$out"
  mv "$tmp" "$out"
  # 面板靠这一行解析路径，格式别改
  echo "Downloaded item $workshop_id to \"$out\""
  exit 0
fi

# ---- 分支 2：游戏本体 / 任意 app 更新 ----
if [ -z "$app_id" ]; then
  echo "steamcmd-shim: no +app_update or +workshop_download_item in arguments" >&2
  exit 2
fi
[ -z "$install_dir" ] && install_dir=/app/dst-dedicated-server

args=(-app "$app_id" -os linux -osarch 64 -dir "$install_dir")
[ -n "$branch" ] && args+=(-branch "$branch")
[ -n "$validate" ] && args+=(-validate)

echo "steamcmd-shim: depot download app $app_id -> $install_dir ${branch:+(branch $branch)}"
exec "$DEPOT" "${args[@]}"
