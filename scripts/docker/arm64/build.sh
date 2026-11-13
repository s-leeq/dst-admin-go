#!/usr/bin/env bash
# =============================================================================
# 构建 dst-admin-go 的 linux/arm64 镜像（Apple Silicon 本地 / ARM CI 通用）
#
# 用法：
#   ./build.sh                 # 跟上游 main（最新版），前端 dist 从上游前端仓库构建
#   ./build.sh --tag 1.6.1     # 指定上游 tag
#   ./build.sh --local         # 用当前工作区代码
#
# 说明：上游最近一次正式 release 停在 1.6.1（2026-02），但 main 已经迭代到
# 2026-08，且前端 dist 不再随 release 发布；所以默认「跟 main」，并把
# carrot-hu23/dst-manage-web2 的前端产物现编出来（按 commit 缓存，编一次就够）。
#
# 环境变量：
#   IMAGE            镜像仓库名，默认 dst-admin-go
#   BAKE_GAME_SERVER true 时把游戏本体（约 4.5GB）烘进镜像
#   GOPROXY          Go 模块代理，默认 https://goproxy.cn,direct
#   NPM_REGISTRY     npm 源，默认沿用本机 npm 配置
#   UPSTREAM_REPO    上游后端仓库，默认 carrot-hu23/dst-admin-go
#   FRONTEND_REPO    上游前端仓库，默认 carrot-hu23/dst-manage-web2
# =============================================================================
set -euo pipefail

here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo=$(cd "$here/../../.." && pwd)
upstream=${UPSTREAM_REPO:-carrot-hu23/dst-admin-go}
frontend=${FRONTEND_REPO:-carrot-hu23/dst-manage-web2}
image=${IMAGE:-dst-admin-go}
bake=${BAKE_GAME_SERVER:-false}
goproxy=${GOPROXY:-https://goproxy.cn,direct}
cache_root=${CACHE_DIR:-$repo/.cache}

mode=main
case "${1:-}" in
  ""|"--latest"|"latest") mode=main ;;
  "--tag")                mode=tag; tag=${2:?--tag 需要一个 tag 名} ;;
  "--local")              mode=local ;;
  *)                      mode=tag; tag=$1 ;;
esac

cleanup() { [ -n "${ctx_tmp:-}" ] && rm -rf "$ctx_tmp"; }
trap cleanup EXIT

# ---- 解析版本 ----
if [ "$mode" = "main" ]; then
  git -C "$repo" fetch "https://github.com/$upstream.git" main --quiet
  tag="main-$(git -C "$repo" rev-parse --short FETCH_HEAD)"
  ref=FETCH_HEAD
elif [ "$mode" = "tag" ]; then
  git -C "$repo" fetch "https://github.com/$upstream.git" --tags --quiet
  ref=$tag
else
  tag=$(git -C "$repo" describe --tags --always 2>/dev/null || echo local)
  ref=""
fi
echo "==> 目标版本: $tag (mode=$mode)"

# ---- 准备后端构建上下文 ----
if [ "$mode" = "local" ]; then
  ctx=$repo
else
  ctx_tmp=$(mktemp -d)
  ctx=$ctx_tmp
  git -C "$repo" archive --format=tar "$ref" | tar -x -C "$ctx"
  # 临时上下文里没有本分支的 arm64 脚本，覆盖进去
  mkdir -p "$ctx/scripts/docker/arm64"
  cp -f "$here/Dockerfile" "$here/docker-entrypoint.sh" "$here/steamcmd-shim.sh" "$here/docker_dst_config" \
        "$ctx/scripts/docker/arm64/"
fi

# ---- 准备前端 dist ----
build_frontend() {
  local dest=$1
  local sha cache
  sha=$(git ls-remote "https://github.com/$frontend.git" refs/heads/main | cut -c1-12)
  cache="$cache_root/web-$sha"

  if [ -f "$cache/index.html" ]; then
    echo "==> 复用前端构建缓存: $cache"
  else
    echo "==> 构建前端 $frontend@main ($sha) ..."
    local tmp
    tmp=$(mktemp -d)
    git clone --depth 1 "https://github.com/$frontend.git" "$tmp/web" --quiet
    # 上游 lockfile 里的 resolved 指向 npmmirror，新版 npm 会拒绝拉 "remote" 包，
    # 所以强制把 registry host 替换成本机的源；esbuild 的 postinstall 已实测能过。
    (
      cd "$tmp/web" \
        && npm ci --no-audit --no-fund \
             --registry="${NPM_REGISTRY:-https://registry.npmjs.org/}" \
             --replace-registry-host=always \
        && npm run build
    ) >"$tmp/web.log" 2>&1 || { echo "!! 前端构建失败，日志尾部：" >&2; tail -30 "$tmp/web.log" >&2; exit 1; }
    mkdir -p "$cache"
    cp -a "$tmp/web/dist/." "$cache/"
    rm -rf "$tmp"
  fi

  mkdir -p "$dest"
  cp -a "$cache/." "$dest/"
}

if [ ! -f "$ctx/dist/index.html" ]; then
  # tag 模式优先用同版本 release 里的前端产物（版本最严格对齐）
  if [ "$mode" = "tag" ]; then
    url="https://github.com/$upstream/releases/download/${tag}/dst-admin-go-${tag}-linux-amd64.tar.gz"
    tmpkg=$(mktemp -d)
    if curl -fsSL "$url" -o "$tmpkg/pkg.tgz"; then
      tar -xzf "$tmpkg/pkg.tgz" -C "$tmpkg"
      pkgdir=$(find "$tmpkg" -maxdepth 1 -type d -name "dst-admin-go-*" | head -1)
      mkdir -p "$ctx/dist" && cp -a "$pkgdir/dist/." "$ctx/dist/"
      echo "==> 使用 release 自带前端产物: $tag"
    else
      echo "!! $tag 没有 linux-amd64 release 产物，回退到前端仓库构建" >&2
      build_frontend "$ctx/dist"
    fi
    rm -rf "$tmpkg"
  else
    build_frontend "$ctx/dist"
  fi
fi

# ---- 构建 ----
echo "==> buildx 构建 linux/arm64 镜像: ${image}:${tag}-arm64"
docker buildx build \
  --platform linux/arm64 \
  -f "$here/Dockerfile" \
  --build-arg "GOPROXY=$goproxy" \
  --build-arg "BAKE_GAME_SERVER=$bake" \
  -t "${image}:${tag}-arm64" \
  -t "${image}:latest-arm64" \
  --load \
  "$ctx"

echo "==> 完成: ${image}:${tag}-arm64 / ${image}:latest-arm64"
docker image inspect "${image}:${tag}-arm64" --format '{{.Os}}/{{.Architecture}} size={{.Size}}' 2>/dev/null || true
