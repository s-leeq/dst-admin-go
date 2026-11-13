# Docker 部署（linux/arm64）

Apple Silicon（M1/M2/M3/M4）与 ARM 服务器用的镜像构建脚本，**跟随上游最新代码**，功能与
`scripts/docker/` 的 amd64 镜像对齐。

## 背景：为什么要单独一套

上游 `release.yml` 只构建 `linux/amd64`。arm64 上真正的障碍不是面板（纯 Go、
`CGO_ENABLED=0`，交叉编译即可），而是：

| 组件 | 障碍 | 这里的做法 |
|---|---|---|
| 饥荒服务端 app 343050 | 只有 x86 / x86_64 二进制 | 用 **box64** 翻译执行 `bin64/dontstarve_dedicated_server_nullrenderer_x64`（面板 `bin=2664` 分支） |
| steamcmd | Valve 只发布 x86 32 位版本 | 用 **DepotDownloader**（arm64 原生，自带 .NET 运行时，镜像里不再装 dotnet）+ `steamcmd-shim.sh` 冒充 `steamcmd.sh` |
| 面板本体 | 无 arm64 产物 | `GOARCH=arm64` 现编 |
| 前端 dist | 仓库里没有（`.gitignore`），最近 release 停在 1.6.1 | 从 `carrot-hu23/dst-manage-web2` 现编，按 commit 缓存 |

> 与 Docker Hub 上 `hujinbo23/dst-admin-go-arm64` 的区别：那个镜像只有 `1.5.2` 一个 tag
> （2025-10 构建，没有 `latest`），老式 `/app/dst_config` 布局，每次启动都联网
> `apt install` amd64 库，且官方描述里明确写了**模组订阅不可用**。
> 本目录的镜像跑上游当前代码、`/app/data` 单卷，模组订阅靠 DepotDownloader 的
> `-pubfile` 恢复（见 `steamcmd-shim.sh`）。

## 构建

```bash
# 默认：跟上游 main（最新版），前端 dist 现编
bash scripts/docker/arm64/build.sh

# 指定上游 tag
bash scripts/docker/arm64/build.sh --tag 1.6.1

# 用当前工作区代码（需要根目录已有 dist/）
bash scripts/docker/arm64/build.sh --local
```

构建上下文是仓库根。前端产物按 `dst-manage-web2` 的 commit 缓存在
`.cache/web-<ref>-<sha>`（已 gitignore），第二次构建不再重编前端。首次构建还要编译
box64（ARM 上约 5–15 分钟），之后这一层走缓存，增量构建分钟级。

### 版本与源码布局

Dockerfile 同时支持两种布局：v2 之后（有 `cmd/`、`internal/`，用 `go build ./cmd/server`）
和 v2 之前（`main.go` 在根，如 `1.6.1`，用 `go build .`）。

`--tag` 模式的前端产物按以下顺序解析，**任何一步失败都不会静默换成前端 main**：

1. 该 tag 的新命名 release 资产 `dst-admin-go-<tag>-linux-amd64.tar.gz`；
2. 旧命名 `dst-admin-go.<tag>.tar.gz`（1.6.1 这类老版本用的是这个，包内同样带 `dist/`）；
3. 只有显式给出 `FRONTEND_REF=<ref>` 才会改用该 ref 构建前端；
4. 都没有就报错退出（避免"说是 1.6.1，实际装的是 main 的前端"）。

```bash
# 例：构建 1.6.1（旧布局 + 旧资产命名）
bash scripts/docker/arm64/build.sh --tag 1.6.1

# 例：某个 tag 的 release 里确实没前端产物，明确指定前端 ref
FRONTEND_REF=main bash scripts/docker/arm64/build.sh --tag 1.6.1
```

注意两点：

- `latest-arm64` 只跟随 `main`；用 `--tag` 构建历史版本不会把 `latest` 指回旧版。
- v2 之前的老版本（如 1.6.1）没有 `persistent_storage_root`，存档按它自己的逻辑落在
  `~/.klei/DoNotStarveTogether`（容器内 `/root/.klei`），**不是** `/app/data`；
  用老版本时请挂载 `/root/.klei`，`/app/data` 那套布局只对 v2 之后成立。

> 注意：上游最近一次正式 release 是 `1.6.1`（2026-02），之后 main 一直往前走到
> 2026-08（v2 重构 + 容器化改造），**且前端 dist 不再随 release 发布**——所以默认跟
> main，而不是跟 release tag。

## 运行

```bash
mkdir -p ~/dstsave/data

docker run -d \
  --name dst-admin \
  --platform linux/arm64 \
  -p 8082:8082 \
  -p 10888:10888/udp \
  -p 10998:10998/udp \
  -p 10999:10999/udp \
  -v ~/dstsave/data:/app/data \
  -v ~/dstsave/dst-dedicated-server:/app/dst-dedicated-server \
  -e TZ=Asia/Shanghai \
  dst-admin-go:latest-arm64
```

- `/app/data` 是唯一必须挂载的目录（面板设置、存档 `DoNotStarveTogether/Cluster_1`、
  备份 `backup/`、模组缓存 `mod/`、账号 `password.txt`、SQLite）。
- `/app/dst-dedicated-server` 可选；不挂的话容器重建会重新下载约 4.5GB 本体。
- 三个 UDP 端口必须映射，否则玩家进不来。

首次登录 `admin` / `123456`。

## 关键设置

`Dockerfile` 里已写入 `DST_ADMIN_CONTAINER_MODE=true`，面板会锁定
`steamcmd / force_install_dir / cluster` 三个字段（路径由镜像内固定）。默认
`docker_dst_config` 里 `bin=2664`：

```
2664 → cd <install>/bin64 ; screen ... box64 ./dontstarve_dedicated_server_nullrenderer_x64
 64  → 直接执行 x64 二进制，arm64 上会 Exec format error
 86  → box86 跑 x64 二进制，同样不行
 32  → bin/ 下是 32 位 x86，arm64 容器里没有 AArch32 支持
```

## 模组订阅为什么能用

面板下载模组走的是 `steamcmd +workshop_download_item 322330 <id>`，并用正则
`Downloaded item \d+ to "([^"]+)"` 解析落盘路径。`steamcmd-shim.sh` 把它翻译成：

```
DepotDownloader -app 322330 -pubfile <id> -dir <mod_download_path>/steamapps/workshop/content/322330/<id>
```

然后补打那一行 `Downloaded item ...`，所以面板的模组列表、`modinfo.lua` 读取、配置界面
都照常工作。仅支持**公开**创意工坊物品（匿名可下）。

## CI（推荐）

`.github/workflows/arm64-image.yml`：

- 默认构建上游 `main`，也支持指定 ref / tag；
- 在 GitHub 原生 arm64 runner（`ubuntu-24.04-arm`）上构建，不需要 QEMU；
- 推送 `ghcr.io/<owner>/dst-admin-go:<tag|main-sha>-arm64`、`:sha-<sha>-arm64`、
  `:latest-arm64`；
- 每天定时检查，同一提交已构建过就跳过。

本地更新：

```bash
docker pull ghcr.io/<owner>/dst-admin-go:latest-arm64
docker compose up -d
```

> ⚠️ `docker compose pull` 只对 registry 里的镜像生效；本地 `build.sh` 出来的镜像请用
> `docker compose build && docker compose up -d`，或先 push 到 registry 再 pull。

## 冒烟测试

```bash
bash scripts/docker/arm64/verify.sh dst-admin-go:latest-arm64
```

检查项都要求「真的跑通」，任一项失败即非 0 退出（不用 `|| true` 或管道吞错误）：

- 架构必须是 `linux/arm64`；
- `box64 --version` 有输出，并用 amd64 busybox 实测能翻译动态链接的 x86_64 程序；
- x86_64 的 `libc/libstdc++/libgcc/libcurl` 存在；
- DepotDownloader 可执行，且 `/usr/share/dotnet` 不存在（证明它自带运行时）；
- shim 行为：含空格路径不被拆散、成功后原子改名、失败不留半成品目录；
- 真起一次面板容器并 `curl /` 断言 200。

## 已知限制

- box64 有翻译开销，实测约为 x86_64 原生性能的 60–80%，内存高 30–40%；建议容器
  `--memory=4g`，玩家数 ≤ 10。
- DepotDownloader 匿名下载只覆盖公开工坊物品；需要登录态的工坊内容拿不到。
- 存档路径固定为 `/app/data/DoNotStarveTogether/Cluster_1`，迁移旧档直接挂到这个路径。
- 模组下载先落到 `<mod_download_path>/steamapps/workshop/content/322330/.<id>.partial.<pid>`，
  成功后原子改名成 `<id>`；面板把"目录存在"当"已下载"，所以失败时不会留下空目录骗过它。
- 若在 fork 上推送 tag，会同时触发上游的 `release.yml`（它要求同账号下有
  `dst-manage-web2` fork），只想出 arm64 镜像时用本 workflow 的 `workflow_dispatch`
  或等定时任务即可。
