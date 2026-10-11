# fnos-cmccyun · 移动云盘（139 云盘）挂载到飞牛 fnOS

把**中国移动云盘 / 139 云盘**（`yun.139.com` 个人云）挂载成飞牛 fnOS 上的一个本地文件夹，
在「文件」应用里像本地磁盘一样浏览、下载、上传、改名、删除；附一个网页管理台用于填授权、换挂载点、切只读、看日志。

纯 Go 实现的 FUSE 文件系统 + fnOS 应用包（`.fpk`），自带 linux/amd64 与 linux/arm64 双架构二进制，
**不依赖 libfuse、无外部运行依赖**。

> Mount 中国移动云盘 (139 Cloud) as a local FUSE filesystem on 飞牛 fnOS,
> shipped as a native `.fpk` app package. Pure Go, no libfuse, no CGO.

- 应用名 `cmccyun`，显示名「移动云盘」，管理台端口 **19898**
- 当前版本 **v1.3.2**，平台 `all`（amd64 + arm64）

---

## 界面预览

> 仓库内**不含截图**（截图里有真实账号、挂载路径等个人信息）。
> 想自己补图，把 PNG 放到 `docs/screenshots/` 并在本节引用即可，例如：
> `![管理台](docs/screenshots/console.png)`

管理台分四个页签：**挂载设置 / 授权 / 云盘浏览 / 运行日志**。
顶部状态卡实时显示「已挂载 / 未挂载」、挂载路径、账号、读写模式、属主 `uid:gid`、
授权剩余天数、开机自动挂载、进程 PID（每 5 秒刷新）。

---

## 功能

- **挂载点想放哪就放哪**：内置文件管理器式「文件夹树 + 目录内容」两栏选择器，
  推荐挂进自己的个人空间（如 `/vol4/1000/移动云盘`），这样在「文件」首页就能直接看到云盘。
  目录不存在可一键创建；接管非空目录需二次确认（原文件只是被遮住，卸载即恢复）；切换位置会先卸载旧位置，失败自动回滚。
- **授权全自动续期**：`Authorization`（网页登录态）有效期约 **30 天**。守护进程每 6 小时检查一次，
  剩余不足 15 天时自动用 AAS 接口续期并写回 `config.json`；且**任何接口只要被服务端以鉴权原因拒绝，
  就立刻强制续期并重试一次**。管理台有「授权 剩余 N 天」徽标与「刷新令牌」按钮。
- **覆盖写入不落本地盘**：改写云端大文件的局部区间时，只把改动的区块暂存到本地（CD2 式 overlay），
  不下载整文件、不整文件重传；管理台显示暂存占用。
- **上传**：分片上传（分片大小可配，默认 100 MB），失败自动退避重试；
  默认带 PC 客户端标识，云端单文件上限比网页版宽松得多。
- **残留挂载自愈**：进程被强杀 / 掉电后 `/proc/mounts` 里留下的 `ENOTCONN` 空壳，
  启动或点挂载时自动清理再重挂，并给出一键清理按钮，不会被误认为「已挂载」。
- **界面**：页签分区、状态卡与气泡提示、按钮加载态与禁用、请求超时有明确报错；深色模式与手机窄屏适配。

---

## 两条必须先知道的限制

1. **用的是非官方接口。** 移动云官方开放平台（`open.yun.139.com`）**不能**整盘挂载——
   它把目录限制在「我的应用收藏 / 你的应用名」下。本应用走的是网页版私有接口
   （`yun.139.com` + 动态个人云 host，`Authorization: Basic base64("pc:账号:token")` + `Mcloud-Sign` 签名），
   与 Alist / OpenList 的 `139 (personal_new)` 驱动同一路子。**云端改版就可能失效**，
   届时需要重新抓包适配，改动集中在 `cmccyun-src/internal/api/`。
   接口调研见 [`docs/cmcc-cloud-api.md`](docs/cmcc-cloud-api.md)。
2. **授权会过期。** 见上「授权全自动续期」；只有连续期本身都失败（令牌彻底作废）时才需要重新抓一条粘贴。

---

## 安装

### 方式 A：应用中心（推荐）

1. 应用中心 → 左下角「设置」→ 打开**手动安装**开关（或执行 `sudo appcenter-cli manual-install enable`）。
2. 拖入 `cmccyun.fpk`（本仓库 [Releases](https://github.com/vincent-jia253/fnos-cmccyun/releases) 里下载）。
3. 选择**安装卷**（应用本体与数据会装到该卷，例如 `/vol4` 对应卷 4）。
4. 按向导填写：挂载目录名、只读/读写、授权串（可留空，装完在管理台里填）。
5. 安装完成后应用会自动启动，fnOS 桌面出现「移动云盘」图标（**仅管理员可见**），
   也可以直接访问 `http://<NAS_IP>:19898/`。

### 方式 B：命令行

```bash
# --volume 必填：目标卷序号（/vol4 → 4）。省略会报默认卷为 0 的错误。
cat > /tmp/cmccyun-install.env <<'EOF'
wizard_mount_subdir=移动云盘
wizard_readonly=rw
wizard_authorization=
EOF

sudo appcenter-cli install-fpk ./cmccyun.fpk --volume 4 --env /tmp/cmccyun-install.env
sudo appcenter-cli start cmccyun      # 安装流程通常已自动启动
```

安装时会自动创建 `cmccyun` 系统用户、把 `/etc/fuse.conf` 补上 `user_allow_other`
（挂载内容才能被 SMB /「文件」读到）。守护进程自 v1.2.0 起默认以 **root** 运行，
这样才能在任意位置创建挂载点。

---

## 获取 Authorization（关键一步）

1. 电脑浏览器打开 <https://yun.139.com> 并登录。
2. `F12` → **Network** → 过滤 `yun.139.com` → 随便点一个接口（如 `/orchestration/personalCloud/*`）。
3. 在 **Request Headers** 里找到 `Authorization`，复制它的值（形如 `Basic cGM6MTM4MDAxMzgwMDA6ZXlK...`）。
4. 打开管理台 `http://<NAS_IP>:19898/` →「授权」页粘贴 →「保存并挂载」。

> 不要把开头的 `Authorization: ` 一起粘进去，只要冒号后面的值。
> 这串东西等价于网页登录态，**请勿外发**；它在 NAS 上以 `0600` 保存在 `config.json` 里。

---

## 构建

依赖：[Go](https://go.dev/) 1.24+、[fnpack](https://developer.fnnas.com/)（飞牛官方打包工具）、`git`。

```bash
git clone https://github.com/vincent-jia253/fnos-cmccyun.git
cd fnos-cmccyun
GO=go VERSION=1.3.2 ./build.sh     # 交叉编译双架构二进制 → 同步 manifest 版本 → fnpack build
# 产物：./cmccyun.fpk
```

只编译源码（不打包）：

```bash
cd cmccyun-src
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X github.com/vincent-jia253/fnos-cmccyun/internal/app.Version=1.3.2" -o cmccyun .
```

---

## 仓库结构

```
fnos-cmccyun/                 # 仓库根 = fpk 打包工程
├── manifest                  # 应用元信息（版本 / 端口 / 桌面入口 / changelog）
├── config/{privilege,resource}   # 运行身份 + 数据共享目录声明
├── cmd/                      # fnOS 生命周期脚本（install / config / upgrade / uninstall / main）
├── wizard/                   # 安装 / 设置 / 卸载向导（JSON）
├── app/                      # 进包内容：fnos-common.sh 与 ui/（源码）+ 双架构二进制（build.sh 生成）
├── build.sh                  # 一键构建
├── cmccyun-src/              # Go 源码（~4000 行）
│   ├── main.go               # CLI：run / mount / unmount / status / token / path / fnos-init …
│   └── internal/
│       ├── api/              # 网页私有接口客户端：签名、动态 host、上传、令牌续期、重试
│       ├── vfs/              # FUSE 文件系统：目录缓存、覆盖写入 overlay、残留挂载识别
│       └── app/              # 守护进程、配置、挂载点管理、网页管理台（内嵌 HTML/CSS/JS）
├── tests/                    # 测试与运维脚本（见下）
├── tools/                    # 图标生成、假 139 服务、接口抓取辅助
└── docs/
    ├── notes.md              # 开发/实测记录：设计取舍、真机验证清单、常见运维与排错
    └── cmcc-cloud-api.md     # 移动云盘接口调研（官方开放平台全表 + 网页私有 API 清单）
```

---

## 测试

```bash
cd cmccyun-src
go test ./...                    # 单元测试（FUSE 相关用例需 CMCCYUN_TEST_FUSE=1）
go vet ./...
```

端到端 / 真机脚本（`tests/`，需 root 的用 `sudo bash`）：

| 脚本 | 作用 |
|---|---|
| `make-fixtures.sh` + `fake-daemon.sh` + `functional.sh` | 用本地假后端跑端到端 FUSE 测试（读一致性、并发读、写/改名/删除、上传、局部改写、`ftruncate`），无需真云盘 |
| `fnos-lifecycle.sh` | 真机上验证 fnOS 生命周期：status → start → 挂载可见 → 跨用户读写 → 幂等 start → stop → 0 残留挂载 |
| `fnos-fuse-check.sh` | 校验 `allow_other`、mount 与后端 `md5` 一致、root 与普通用户都能读写 |
| `fnos-reinstall.sh` | 「停 → 卸 → 清应用中心记录 → 全新安装 → 校验」全自动流程 |
| `fnos-upgrade-sim.sh` | 模拟应用中心覆盖升级，校验配置原样保留 |
| `auth-recovery-demo.sh` | 用假 139 服务端到端验证「鉴权失败 → 自动续期 → 重试成功」 |
| `restart.sh` / `sync_fpk.py` | 起隔离调试实例 / 把 fpk 内容同步进已安装目录 |

跑 `functional.sh` 前要先 `make-fixtures.sh` → 编译二进制到 `/tmp/cmccyun-build/cmccyun` → `restart.sh`，
它**不会自己搭环境**（否则会看到一堆 `No such file or directory` 的假失败）。

---

## 常见运维

```bash
sudo appcenter-cli status cmccyun   # 状态
sudo appcenter-cli start  cmccyun   # 启动
sudo appcenter-cli stop   cmccyun   # 停止（别用 pkill，进程名是 cmccyun-linux-a）
sudo /var/apps/cmccyun/cmd/main status   # 应用自身状态（只看退出码：0 运行中 / 3 未运行）
```

日志：

| 路径 | 内容 |
|---|---|
| `/vol<N>/@appdata/cmccyun/cmccyun.log` | 守护进程日志（与管理台同源） |
| `/vol<N>/@appdata/cmccyun/stdout.log` | 生命周期脚本输出（怀疑卡住时 `kill -QUIT` 的 goroutine 栈也在这里） |
| `/var/log/apps/cmccyun.log` | 安装 / 升级脚本诊断输出 |

**残留挂载**（异常退出时）：

```bash
mount | grep cmccyun
sudo fusermount3 -uz /path/to/mountpoint   # 懒卸载，必成功
```

v1.3.0 起启动与点挂载时都会自动清理这类残留，上面只是兜底。

**卸载**：在应用中心界面点卸载（会先停服务、卸挂载）。`appcenter-cli uninstall` 在飞牛上会被拒绝。

更多排错（授权过期、挂载点被遮住、普通用户看不到内容、上传失败……）见
[`docs/notes.md` 第 8 节](docs/notes.md)。

---

## 已知取舍

- **管理台没有鉴权**，监听 `0.0.0.0:19898`，仅建议在可信内网使用。
  想收紧可在 `config.json` 里把 `web_listen` 改成 `127.0.0.1`（从此只能从 NAS 本机访问）。
- **守护进程以 root 运行**（为了能挂到任意位置，与 CloudDrive2 等同机方案一致）；
  挂载内容的可见性由配置里的 uid/gid 与权限位决定。介意的话可在配置目录放 `run_as` 文件指定普通用户
  （代价：只能挂在该用户有权限的位置）。
- **覆盖写入仍有上传流量**：服务端要求顺序分片，所以局部改写上传量约等于文件大小（省的是本地磁盘与整文件下载）。
- 非官方接口不保证长期可用；接口失效时改动集中在 `cmccyun-src/internal/api/`。
- `CMCCYUN_DEBUG_FAKE=<本地目录>` 是调试后门（挂本地目录而非云盘），**正式环境不要设置**。

---

## 免责声明

本项目为个人自用工具，**与飞牛（fnOS）及中国移动官方无任何关系**，未获其授权或认可。
它通过逆向的网页版私有接口访问你的云盘，可能因云端改版而失效，也可能不符合服务条款。
请自行评估风险，仅用于你自己的账号，不要用于批量、商业或侵犯他人权益的用途。
因使用本软件造成的任何数据丢失、账号受限等后果，作者不承担责任。

## License

[MIT](LICENSE)
