#!/usr/bin/env bash
# =============================================================================
# 构建 dst-admin-go 的 linux/arm64 镜像（Apple Silicon 本地 / ARM CI 通用）
#
# 用法：
#   ./build.sh                 # 跟上游 main（最新版），前端 dist 从上游前端仓库构建
#   ./build.sh --tag 1.6.1     # 指定上游 tag（前端产物取自该 tag 的 release）
#   ./build.sh --local         # 用当前工作区代码
#
# 版本语义：
#   main   —— 上游 main 的 HEAD；前端取自 dst-manage-web2 的 main（上游发布流水线同款组合）
#   tag    —— 只接受「该 tag 的 release 里带 dist」或显式 FRONTEND_REF；
#             拿不到就直接失败，不会静默换成前端 main（否则构建内容与 tag 不符）
#
# 环境变量：
#   IMAGE            镜像仓库名，默认 dst-admin-go
#   BAKE_GAME_SERVER true 时把游戏本体（约 4.5GB）烘进镜像
#   GOPROXY          Go 模块代理，默认 https://goproxy.cn,direct
#   NPM_REGISTRY     npm 源，默认 https://registry.npmjs.org/
#   FRONTEND_REF     前端 ref（tag 模式下的显式替代版本）
#   UPSTREAM_REPO    上游后端仓库，默认 carrot-hu23/dst-admin-go
#   FRONTEND_REPO    上游前端仓库，默认 carrot-hu23/dst-manage-web2
# =============================================================================
set -euo pipefail

here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo=$(cd "$here/../../.." && pwd)
upstream=${UPSTREAM_REPO:-carrot-hu23/dst-admin-go}
frontend=${FRONTEND_REPO:-carrot-hu23/dst-manage-web2}
frontend_ref=${FRONTEND_REF:-main}
image=${IMAGE:-dst-admin-go}
bake=${BAKE_GAME_SERVER:-false}
goproxy=${GOPROXY:-https://goproxy.cn,direct}
npm_registry=${NPM_REGISTRY:-https://registry.npmjs.org/}
cache_root=${CACHE_DIR:-$repo/.cache}

mode=main
case "${1:-}" in
  ""|"--latest"|"latest") mode=main ;;
  "--tag")                mode=tag; tag=${2:?--tag 需要一个 tag 名} ;;
  "--local")              mode=local ;;
  *)                      mode=tag; tag=$1 ;;
esac

# 清理只做可选动作，且必须保持原退出码（trap 里返回非 0 会污染 $?）
cleanup() {
  if [ -n "${ctx_tmp:-}" ]; then
    rm -rf "$ctx_tmp"
  fi
  return 0
}
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

# ---- 前端：从上游前端仓库现编（按 commit 缓存） ----
build_frontend() {
  local dest=$1 ref_name=$2
  local sha cache tmp
  sha=$(git ls-remote "https://github.com/$frontend.git" "refs/heads/$ref_name" "refs/tags/$ref_name" \
        | head -1 | cut -c1-12)
  [ -n "$sha" ] || { echo "!! 无法解析前端 ref: $frontend@$ref_name" >&2; return 1; }
  cache="$cache_root/web-$ref_name-$sha"

  if [ -f "$cache/index.html" ]; then
    echo "==> 复用前端构建缓存: $cache"
  else
    echo "==> 构建前端 $frontend@$ref_name ($sha) ..."
    tmp=$(mktemp -d)
    git clone --depth 1 --branch "$ref_name" "https://github.com/$frontend.git" "$tmp/web" --quiet
    # 上游 lockfile 的 resolved 指向 npmmirror，新版 npm 会拒绝拉 "remote" 包
    if ! (
      cd "$tmp/web" \
        && npm ci --no-audit --no-fund \
             --registry="$npm_registry" \
             --replace-registry-host=always \
        && npm run build
    ) >"$tmp/web.log" 2>&1; then
      echo "!! 前端构建失败，日志尾部：" >&2
      tail -30 "$tmp/web.log" >&2
      rm -rf "$tmp"
      return 1
    fi
    mkdir -p "$cache"
    cp -a "$tmp/web/dist/." "$cache/"
    rm -rf "$tmp"
  fi

  mkdir -p "$dest"
  cp -a "$cache/." "$dest/"
}

# ---- 前端：取某个 tag 的 release 里自带产物（新命名 + 旧命名都认） ----
fetch_release_dist() {
  local tag_name=$1 dest=$2 tmp name url found
  tmp=$(mktemp -d)
  for name in "dst-admin-go-${tag_name}-linux-amd64.tar.gz" "dst-admin-go.${tag_name}.tar.gz"; do
    url="https://github.com/$upstream/releases/download/${tag_name}/${name}"
    echo "==> 尝试同版本前端产物: $name"
    if curl -fsSL --retry 2 "$url" -o "$tmp/pkg.tgz" 2>/dev/null; then
      tar -xzf "$tmp/pkg.tgz" -C "$tmp" 2>/dev/null || true
      found=$(find "$tmp" -type d -name dist 2>/dev/null | head -1)
      if [ -n "$found" ] && [ -f "$found/index.html" ]; then
        mkdir -p "$dest"
        cp -a "$found/." "$dest/"
        rm -rf "$tmp"
        return 0
      fi
    fi
  done
  rm -rf "$tmp"
  return 1
}

if [ -f "$ctx/dist/index.html" ]; then
  echo "==> 复用上下文中已有的 dist/"
elif [ "$mode" = "tag" ]; then
  if fetch_release_dist "$tag" "$ctx/dist"; then
    echo "==> 使用 $tag 的 release 自带前端产物"
  elif [ -n "${FRONTEND_REF:-}" ]; then
    echo "!! $tag 的 release 里没有 dist，按显式指定的 FRONTEND_REF=$frontend_ref 构建前端" >&2
    build_frontend "$ctx/dist" "$frontend_ref"
  else
    echo "!! $tag 的 release 里没有可用的前端产物，构建中止。" >&2
    echo "!! 不会再静默回退到前端 main（那会让镜像内容与 $tag 不符）。" >&2
    echo "!! 确实要用某个前端版本，请显式指定：FRONTEND_REF=main bash $0 --tag $tag" >&2
    exit 1
  fi
else
  build_frontend "$ctx/dist" "$frontend_ref"
fi

# ---- 构建 ----
# latest 只跟随 main；用 --tag 构建历史版本时不能把 latest 指回旧版本
tags=(-t "${image}:${tag}-arm64")
if [ "$mode" = "main" ]; then
  tags+=(-t "${image}:latest-arm64")
fi

echo "==> buildx 构建 linux/arm64 镜像: ${image}:${tag}-arm64"
docker buildx build \
  --platform linux/arm64 \
  -f "$here/Dockerfile" \
  --build-arg "GOPROXY=$goproxy" \
  --build-arg "BAKE_GAME_SERVER=$bake" \
  "${tags[@]}" \
  --load \
  "$ctx"

echo "==> 完成: $(printf '%s ' "${tags[@]}" | sed 's/-t //g')"
docker image inspect "${image}:${tag}-arm64" --format '{{.Os}}/{{.Architecture}} size={{.Size}}' 2>/dev/null || true
