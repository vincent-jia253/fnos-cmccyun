# cmccyun-src — Go 实现说明

移动云盘（139 个人云）FUSE 挂载守护进程 + 网页管理台。**纯 Go、`CGO_ENABLED=0`**，
可静态交叉编译到 linux/amd64 与 linux/arm64，不依赖 libfuse。

依赖只有一个：`github.com/hanwen/go-fuse/v2 v2.9.0`。

```
main.go                  CLI 入口（run/mount/unmount/status/token/path/fnos-init/prepare/version）
internal/api/            网页私有接口客户端
  ├── client.go          请求构造、动态个人云 host、目录/文件/上传/删除/改名/回收站；
  │                      令牌有效期解析 + AAS 自动续期 + AuthState；
  │                      鉴权失败即续期重试一次（并发只续期一次 + 退避）
  ├── sign.go            Mcloud-Sign 签名（若干摘要变体按序尝试）
  ├── types.go           JSON 结构体
  ├── retry.go           分片上传重试（退避 500ms→5s，仅网络类错误）
  ├── recover_test.go    鉴权恢复回归测试（续期重试/并发/退避/分类）
internal/app/callback_test.go  续期回调不得在持服务锁时执行（死锁回归）
internal/app/deadlock_test.go  看门狗：5 秒超时扫全部控制台入口，超时打印 goroutine 栈（v1.3.0）
internal/app/ui_test.go        界面回归：`.sp` 只能当按钮里的加载指示器，占位必须用 `.grow`（v1.3.1）；「授权剩余天数」的悬停说明与可见解释必须存在（v1.3.2）
internal/vfs/dead_test.go      `vfs.Dead` 的 ENOTCONN 判定（v1.3.0）
  └── upload.go          分片上传（默认 100 MB/片），UploadReader 供覆盖写入使用
internal/vfs/            FUSE 文件系统
  ├── fs.go              节点树、远程流读取（窗口化区间读）、覆盖写入接线、懒卸载、Dead() 僵尸挂载识别
  ├── overlay.go         CD2 式覆盖写入：extents 区间表 + overlay io.ReaderAt
  ├── backend.go         后端抽象（真实云盘 / 本地假后端）
  └── fake.go            调试用本地目录后端
internal/config/         config.json 读写 + 默认值
internal/app/
  ├── app.go             Service：挂载、重挂载、状态、applyEnv；mounted_at 状态（实际挂载点）
  ├── mountpoint.go      挂载位置管理：卷探测、路径校验、目录浏览/新建、SetMountPoint 与回滚
  ├── statowner.go       stat → uid/gid（属主跟随用）
  ├── web.go             HTTP 管理台（REST + 静态页）
  ├── ui.go              内嵌的前端页面：页签式控制台（挂载设置/授权/云盘浏览/运行日志）+ 挂载状态卡 + 目录选择器
  └── ring.go            500 行环形日志缓冲
```

> 锁纪律：`Service` 方法名带 `Locked` 后缀表示**调用方必须已持有 `s.mu`**，不带后缀的自己加锁；
> 不得混用，也不得在持锁时做文件系统 IO。详见「实现要点 / 踩过的坑」第 9 条。

## CLI

| 命令 | 说明 |
|---|---|
| `cmccyun run [-c 配置]` | 启动服务（管理台 + 按 `auto_mount` 自动挂载），等待 SIGINT/SIGTERM 后卸载退出；写 pidfile |
| `cmccyun mount` / `unmount` | 只做挂载 / 卸载 |
| `cmccyun status` | 打印版本、账号、是否有授权、挂载状态、挂载点、最近错误 |
| `cmccyun token <Authorization>` | 写入授权并保存（等价于管理台里保存） |
| `cmccyun path` | 输出挂载点（脚本里用） |
| `cmccyun fnos-init` | 由 fnOS 安装/设置向导调用：把 `wizard_*` 环境变量落到 config.json（非破坏性，字段为空则不覆盖） |
| `cmccyun prepare [用户名]` | root 侧准备：建目录（新建目录属主跟随父目录）、按 `os/user` 查到的 uid/gid chown（fnOS 生命周期脚本调用；不给用户名则不 chown） |

## 环境变量

| 变量 | 作用 |
|---|---|
| `CMCCYUN_ETC` | 配置目录（等价于 `-c`），用于指向 `/vol4/@appconf/cmccyun` |
| `CMCCYUN_CONFIG` | 直接指定 config.json 路径 |
| `CMCCYUN_MOUNT` | 挂载点（仅当配置里为空时填充） |
| `CMCCYUN_STAGE` / `CMCCYUN_LOG` | 暂存目录 / 日志文件 |
| `CMCCYUN_PORT` / `CMCCYUN_LISTEN` | 管理台端口 / 监听地址 |
| `CMCCYUN_AUTH` | 授权串（仅当配置里为空时填充） |
| `CMCCYUN_READONLY` | `1/true/ro` 强制只读 |
| `CMCCYUN_PIDFILE` | pidfile 路径，缺省 `<配置目录>/cmccyun.pid` |
| `CMCCYUN_DEBUG_FAKE` | **调试**：把本地目录当作云盘后端挂载，无需授权（测试脚本用它） |

## config.json

```jsonc
{
  "authorization": "Basic cGM6...",     // 网页 Authorization，0600 保存
  "mount_point": "/vol4/1000/移动云盘",      // 任意位置；写进个人空间后「文件」里直接可见
  "allow_non_empty": false,             // 目标目录非空时需显式确认（挂载会遮住原内容）
  "read_only": false,
  "uid": 0, "gid": 0,                   // 0 = 用进程自身身份
  "file_perm": "0666", "dir_perm": "0777",
  "dir_cache_ttl": 60,                  // 目录缓存秒数
  "upload_part_mb": 100,                // 上传分片大小
  "client_type": "13",                  // X-Yun-Client-Info 第三段：13=PC 客户端
  "capacity_gb": 2048,
  "stage_dir": "/vol4/@appdata/cmccyun/staging",
  "log_file": "/vol4/@appdata/cmccyun/cmccyun.log",
  "web_listen": "0.0.0.0", "web_port": 19898,
  "auto_mount": true
}
```

## 实现要点 / 踩过的坑

1. **大文件读必须用窗口化区间读。** 云端 `getContentInfo` 返回的是带 `exp` 的临时直链，
   按单次 `Read` 大小去开区间请求会踩到短读。`fs.go` 里的 `remoteStream` 改为：
   以 8 MB 为窗口打开一次区间流，窗口内的 `Read` 复用同一连接，越界/seek 才重开，
   中间的空洞用 `io.CopyN(io.Discard, …)` 跳过，并在 `Release` 时 `closeLocked()`。
2. **挂载内容要让别的用户可见。** 挂载带 `allow_other()`，因此需要 `/etc/fuse.conf` 里
   `user_allow_other`（安装脚本自动补，`cmccyun_ensure_fuse_conf`）。守护进程从 v1.2.0 起
   默认以 root 运行（要能在任意位置建挂载点），也可以在 `<配置目录>/run_as` 里指定普通用户。
3. **卸载要懒，但别懒错地方。** `vfs.Unmount` 先 `unmount`，失败退化为 `MNT_DETACH`，再失败走
   `fusermount3 -uz`；始终报告清楚的原因，避免"卸载失败"卡住重启。
4. **重挂载要重读配置。** `Service.Remount` 会重新 `config.Load` + `applyEnv`，
   这样在外部（fnOS 设置向导 / 手工改 config.json）改完再点重挂载才生效。
5. **pidfile。** `run` 自己写 pid（`runuser` 场景下父进程 pid 不可用），
   lifecycle 脚本据此精确 kill。
6. **挂载点可以随时改，所以卸载必须按「实际挂着的那里」。** 配置里的 `mount_point` 是目标，
   不等于现状。切换位置时先卸载、再改配置、再挂新的；卸载/自救都以「实际挂载点」为准
   （内存里的 `mountedAt`，并落盘到 `<配置目录>/mounted_at` 供重启后识别）。
   最初按配置路径卸载，结果切换位置时旧挂载被漏成僵尸 —— 被 `TestSetMountPointMovesMount` 抓到。
7. **任意位置需要 root。** 在别人的个人空间（`/vol4/1000`，属主是账号本人、`d---------+`）里建目录、
   挂 FUSE，普通应用用户没有权限；CloudDrive2 之类同机方案同样以 root 运行。
   挂载内容的归属由配置里的 uid/gid 决定，选择个人空间时会自动跟随为「该用户:Users」
   （`suggestOwner`：拿 uid 去 `/etc/passwd` 查主组，因为 `/volN/<uid>` 本身的组是 root）。
8. **挂载位置安全护栏**（`mountpoint.go`）：必须绝对路径、必须在存储卷内、不能是卷根、
   不能与应用运行目录上下重叠、非空目录需显式确认。
   目标落在**当前挂载点内部**的一律按「先卸下、再核对/创建真实目录、最后挂上」处理（`MountPointInfo.Masked`），
   否则 `os.Stat`/`Mkdir` 会落到云盘里；反过来 `CreateMountDir` 会拒绝在挂载点内新建目录。
   只把**真正挂载了文件系统的卷**算作可用卷（`VolumeRoots` 用 `vfs.IsMountPoint` 过滤，飞牛预建的
   `/vol00`、`/vol01` 这类占位目录既不可选也不可见）。
9. **锁纪律（v1.3.0 修死锁后定为规范）。** `Service` 的方法名带 `Locked` 后缀 = **调用方必须已持有
   `s.mu`**，不带后缀 = **自己加锁**，两者不得混用。踩过的坑：`Update()` 整段持锁后调用
   `validateMountPath()`，而后者经 `curMountPoint()` 又去 `Lock()` —— `sync.Mutex` 不可重入，
   于是该 goroutine 永久阻塞、**后续所有控制台接口排在它后面一起卡**（现象：「正在挂载」永远转圈）。
   现在拆成 `validateMountPath{,Opt}`（自锁）/ `validateMountPath{,Opt}Locked`（需持锁），
   `strayMountLocked`、`curMountPointLocked` 同理；并且**绝不在持锁时做文件系统 IO**
   （`Status()` 在锁内取状态，放锁后才探测挂载点），回归测试见 `internal/app/deadlock_test.go`。
10. **僵尸挂载识别（v1.3.0）。** `kill -9` / 掉电会在 `/proc/mounts` 留下空壳挂载点，
    访问即 `ENOTCONN`。`vfs.Dead(dir)` 判定「是挂载点但 `os.Stat` 报 `ENOTCONN`」；
    挂载前把它当成"未挂载"并先卸载重挂，避免「明明坏了却说自己已挂载」。
11. **界面的类名不要一物两用（v1.3.1）。** 整个管理台是 `ui.go` 里的一个内嵌 HTML/JS 字符串，
    类名没有作用域 —— v1.3.0 把「撑开空隙的占位元素」和「按钮里的加载指示器」都叫 `.sp`，
    结果顶栏占位元素继承了 `width:13px;border:2px solid;border-radius:50%;animation:rot infinite`，
    被渲染成一个**贯穿顶栏的旋转圆弧**（用户原话「有个线一直在转」）。
    现在占位统一用 `.grow`，并在 `ui_test.go` 里把「`.sp` 只能出现在 `<i>` 上、不能带 flex」写成断言。
    排查手法：F12 Console 里扫所有 `animationIterationCount==='infinite'` 的元素，`rot` 是转圈、`pulse` 是呼吸灯。
    内嵌页面响应头带 `Cache-Control: no-store`，升级后浏览器不会留着旧页面。
12. **界面上的数字要给出来路（v1.3.2）。** 顶栏那个「授权 剩余 N 天」用户看不懂就问「这什么情况」，
    这类「看着像倒计时但不知道是什么」的数值一律要带说明：悬停 `title` + 页面上看得见的一段文字
    （手机悬停不方便）。文案集中放在 JS 常量 `AUTH_TIP` 里，三处复用；`ui_test.go` 断言它还在。

## 构建

```bash
export CGO_ENABLED=0
GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X github.com/vincent-jia253/fnos-cmccyun/internal/app.Version=1.3.2" -o cmccyun .
```

调试（假后端）：

```bash
CMCCYUN_DEBUG_FAKE=/tmp/cct/backend CMCCYUN_ETC=/tmp/cct/etc ./cmccyun run
```

单元自测：`CMCCYUN_TEST_FUSE=1 go test ./internal/...`（**43 项**：extents 区间合并/合并拆分、
覆盖写入不整文件下载、截断收缩与增长、新建文件先 flush 后 write、令牌续期与形状回退、
鉴权失败续期重试（并发/退避/错误分类）、续期回调不持服务锁（死锁回归）、分片重试退避、
假后端目录列表、**死锁看门狗（逐个调用全部控制台入口，5 秒超时即失败并打印 goroutine 栈）**、
**`vfs.Dead` 的 `ENOTCONN` 判定**、**界面类名回归（`.sp` 只能当按钮里的加载指示器，不许当占位元素）**、**「授权剩余天数」的悬停说明与可见解释不得丢失（v1.3.2）**，
以及挂载位置相关的路径规则校验 / 非空目录确认 / 卷外与卷根拒绝 /
目录浏览 / 新建目录 / 切换位置后旧位置已卸载 / `mounted_at` 重启恢复 / 旧位置僵尸挂载自救）；
FUSE 用例需要 /dev/fuse（不加 `CMCCYUN_TEST_FUSE=1` 会自动跳过）；`-race` 亦通过。
