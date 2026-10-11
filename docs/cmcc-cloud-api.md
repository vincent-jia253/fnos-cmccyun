# 中国移动云盘（139 网盘）开放平台 API 速查表

> 用途：为 fnOS(飞牛 NAS) 实现「把中国移动云盘挂载成本地文件系统」做技术调研。
> 调研时间：2026-06（文档页内 `updateTime` 字段在 2023–2024 之间，部分 2024 年更新）。
> **2026-10-02 增量更新**：见文末 [G. 2026-10 增量](#g-2026-10-增量智能体通道-idaas-v3-与官方-skill) 与独立整合文档
> `research/open-platform-139/LATEST-2026-10-02.md`（含官方 Skill v2.0.0 与 `openapi.yun.139.com` IDaaS v3 通道）。
> 本次全量复核 109 篇官方文档：**既有 A–F 各节结论未变**。
> 结论来源：官方开放平台 `open.yun.139.com` 文档接口（文档站本身是一个 SPA，正文由 `/open-user/api/v4.6/HelpDocuments/*` 提供，见下）；Alist/OpenList `drivers/139` 源码；各站点文档。
>
> **重要区分**：
> - **官方开放平台 API**：申请 AppId/AppKey/SecretKey 后调用，域名 `miniapp.yun.139.com`，走 OAuth 中间页授权 + accessToken。见 [C1](#c1-官方开放平台-api-接口清单)。
> - **网页端私有 API（非官方）**：抓包逆向自 `yun.139.com` 网页端，域名 `yun.139.com` / `user-njs.yun.139.com` / 动态分配的个人云 host，靠浏览器 `Authorization`(Basic) 头 + `Mcloud-Sign` 签名。Alist/OpenList 的 139 驱动用的就是这一套。OpenList 官方文档原话：*"本接口是基于历史产物的逆向接口，项目组不会主动进行维护"*（https://doc.oplist.org/guide/drivers/139 ）。见 [F](#f-官方开放平台缺失的能力与网页端私有-api-补齐)。

**本文档中「未确认」= 在官方文档 / 可核实源码中找不到明确依据，禁止直接照抄实现。**

---

## 目录

- [A. 开放平台接入门槛](#a-开放平台接入门槛)
- [B. 鉴权](#b-鉴权)
- [C. 接口清单](#c-接口清单)
  - [C1. 官方开放平台 API 接口清单](#c1-官方开放平台-api-接口清单)
  - [C2. 网页端私有 API（Alist/OpenList 139 驱动）](#c2-网页端私有-apialistopenlist-139-驱动)
- [D. 文件 ID / 路径模型](#d-文件-id--路径模型)
- [E. 限流、QPS、文件大小与账号限制](#e-限流qps文件大小与账号限制)
- [F. 官方开放平台缺失的能力 与 网页端私有 API 补齐](#f-官方开放平台缺失的能力与网页端私有-api-补齐)
- [G. 2026-10 增量：智能体通道 (IDaaS v3) 与官方 Skill](#g-2026-10-增量智能体通道-idaas-v3-与官方-skill)
- [附：可核实的原始文档来源](#附可核实的原始文档来源)

---

## A. 开放平台接入门槛

来源：官方文档《API基础能力接入指引》(id=51)、《签约接入指引》(id=77)、《移动云盘能力开放服务协议》(id=125)、《FAQ-合作类》(id=267)。

| 项目 | 结论 | 来源 |
|---|---|---|
| 入口 | https://open.yun.139.com ，手机号登录即可（新用户自动注册云盘账号） | id=51 / id=77 |
| 签署开发者 | 需到 **中移互联网能力开放平台 https://dev.10086.cn** 完成合作签约与电子签约（线上签约） | id=77 |
| 注册身份 | 官方接入指引明确要求选 **企业用户注册**：「选择企业用户注册（**非企业认证将无法调用相关能力**）」 | id=77 |
| 协议是否允许个人 | 服务协议第四条「申请条件」写明可以是「**具备完全民事权利能力和民事行为能力、能够独立承担民事责任的自然人**」，个人需提供身份证/护照复印件 | id=125 |
| 两者矛盾 | **注册路径（必须企业认证）与协议正文（允许自然人）表述不一致** → 实际能否以个人身份拿到可调用能力，**未确认**，需以实际审核为准 | — |
| appId / appSecret | 控制台-能力管理-创建应用，成功后获得 **AppId、AppKey(AK)、SecretKey(SK)** | id=51 |
| 基础能力清单 | 官方自述提供的基础能力：**上传、下载、读取用户信息、分享、文件管理** | id=51 |
| 应用审核 | 需要在控制台查审核状态；审核通过后配置 **IP 白名单** 和 **中间页白名单** | id=51 |
| 沙箱 | 官方文档**未提及沙箱/测试环境**（正文无沙箱、sandbox、测试环境字样）→ **未确认（大概率没有）** | id=51/id=53 |
| 费用 | 协议含「计费、分成」条款（可另签单项协议），但基础能力是否收费**未在文档中明确** → 未确认 | id=125 |

---

## B. 鉴权

### B1. 官方开放平台鉴权（OAuth2 类，中间页授权）

来源：id=51（API基础能力接入指引）、id=53（接口调用统一说明）、id=254（二维码扫码登录）、FAQ id=266。

**流程**：
1. 引导用户到中间页授权 URL（带 `appId/appKey/deviceId/uuid/appTitle/version/redirectUrl` 等参数）。
2. 用户登录成功后回跳 `redirectUrl`。
3. 服务端用 `uuid` 调 `accessToken1` 换取 `accesstoken`（唯一凭证，**有效期 30 天**）。
4. 后续所有 API 请求头带 `appid/secretkey/accesstoken/deviceid/channelid`。
5. token 过期用 `refreshToken` 刷新。

**申请授权中间页地址**（`GET`，引导浏览器打开）：
```
https://miniapp.yun.139.com/middle/index.html#/middlePage
   ?pageType=3            # 0pc/1popup/2dialog/3mobile/4ipad/5TV/6mobilePopup
   &appId={appId}
   &appKey={appKey}
   &deviceId={设备唯一标识}
   &uuid={base64(appid_yyyyMMddHHmmssSSS+3位随机数)}
   &appTitle={展示给用户的第三方名称}
   &version={版本号}
   &redirectUrl={回调地址}   # 必须放在链接末尾，带参数需URL编码
```
（`sharedInfo`、`source`、`channelId`、`halfScreen`、`authHeight`、`isAutoGetNum` 为可选）

**换取 token**：
```bash
# 用 uuid 换 accessToken
curl -X POST 'https://miniapp.yun.139.com/open-mpplatform/oauth2/accessToken1' \
  -H 'Content-Type: application/json' \
  -d '{"uuid":"MjAyMTA2MDgxNTU0MTI0N18yMDIxMDcxMzE0NTUzMzMxMjM2Mw=="}'
# → {"code":0,"message":"成功","data":{"accesstoken":"...","expiresIn":1000}}   # expiresIn 单位=小时；文档另处写有效期30天
```

**刷新 token**：
```bash
curl -X POST 'https://miniapp.yun.139.com/open-mpplatform/oauth2/refreshToken' \
  -H 'Content-Type: application/json' \
  -d '{"appId":"****","appSecret":"*****","refreshToken":"<accesstoken>"}'
# → data.accesstoken / data.expiresIn（单位小时）
```
> 文档建议刷新时段：**22:30–24:00、06:00–07:30**，避开 00:00–06:00。

**二维码扫码登录（可选，TV/盒子场景）**：
```bash
curl -X POST 'https://open.yun.139.com/open-mpplatform/oauth2/getMiddlePageQrCodeUrl' \
  -H 'Content-Type: application/json' \
  -d '{"appId":"...","appKey":"...","phone":"<gcm加密，密钥=appKey>","deviceId":"...","channelId":"10001","uuid":"..."}'
# → data = 中间页二维码跳转地址（一天有效期）
```

**请求头格式（所有官方 API 必带）**（id=53）：
```
appid:       {AppId}
secretkey:   {SecretKey}
accesstoken: {accessToken}
deviceid:    {设备唯一标识}     # 必须与换 accessToken 时传的 deviceId 一致，否则报 20010
channelid:   {渠道id}          # 必须与申请能力时勾选的终端类型一致，否则报 20009
```
Content-Type 通常为 `text/xml;UTF-8`（示例均为 XML body）。

**channelId 对照表**（id=53）：

| 终端 | channelId |  | 终端 | channelId |
|---|---|---|---|---|
| H5 | 10001 | | 微信服务号 | 10007 |
| WEB | 10002 | | 其它(硬件等) | 10009 |
| APP(安卓) | 10003 | | 大屏(TV) | 10010 |
| APP(IOS) | 10004 | | 小程序 | 10011 |
| | | | 硬件类型 | 10012 |

**常见鉴权错误码**（FAQ id=266）：`19001` 鉴权失败-没有相关能力（appid/secretkey 错或能力未勾选/未审核）；`12002` token 已失效；`20009` channelid 与勾选终端不匹配；`20010` deviceid 与取 token 时不一致；`20011` 同 appId+手机号再次授权会清除上次 token / token 超 30 天。

### B2. 网页端私有鉴权（Alist/OpenList，非官方）

来源：Alist `drivers/139/util.go`、OpenList 文档。

- 请求头 `Authorization: Basic base64("pc:{account}:{authToken}")`，外加 `Mcloud-Sign: "{时间戳},{随机串},{签名}"`（签名 = md5 变体，见 `calSign`）。
- token 刷新：`POST https://aas.caiyun.feixin.10086.cn:443/tellin/authTokenRefresh.do`，body `<root><token>..</token><account>..</account><clienttype>656</clienttype></root>`。
- 获取 Authorization 的登录分 3 步（OpenList/Alist `loginWithPassword`）：① `mail.10086.cn` 密码登录拿 sid → ② 换 single token → ③ `https://user-njs.yun.139.com/user/thirdlogin` 三方登录拿 authToken。
- 也支持「快速登录」：只填网页 `Authorization`（DevTools 里搜 `hcy/file/list` 请求头），或填 `mail.10086.cn` 的 Cookie + 账号密码让其自动续期。

---

## C. 接口清单

> 官方 API 统一前缀：`https://miniapp.yun.139.com`
> 所有官方接口 **鉴权：有**、**频控限制：有**（文档统一样式写作「使用条件：频控限制：有；接口鉴权：有」，**未给出具体 QPS 数值**）。
> 请求体：官方示例全部为 XML（`Content-Type: text/xml`）。

### C1. 官方开放平台 API 接口清单

#### (1) 文件管理 — 列目录 / 详情 / 重命名 / 删除 / 移动 / 新建目录 / 搜索

| 功能 | 方法 | 完整 URL | 关键请求字段 | 关键响应字段 | 来源(id) |
|---|---|---|---|---|---|
| **查询个人云文件列表** | POST | `https://miniapp.yun.139.com/richlifeApp/devapp/getdisk` | `catalogID`(父节点ID，可裸ID)、`entryShareCatalogID`、`filterType`(0目录+内容/1目录/2内容)、`catalogSortType`、`contentType`、`contentSortType`、`sortDirection`(0正/1反)、`startNumber`(默认-1)、`endNumber`、`catalogType`、`contentSuffix` | `getDiskResult{parentCatalogID,nodeCount,catalogList[],contentList[]}` | 67 |
| **查询文件信息(详情)** | POST | `.../richlifeApp/devapp/getContentInfo` | `ContentID`、`entryShareCatalogID` | `contentInfo{contentID,contentName,contentSuffix,contentSize,contentType,thumbnailURL,bigthumbnailURL,midthumbnailURL,presentURL,presentLURL,presentHURL,digest,parentCatalogId,updateTime,...}` | 70 |
| **重命名文件** | POST | `.../richlifeApp/devapp/updateContentInfo` | `contentID`、`contentName`、`Desc`、`tagList[]`(≤6)、`path` | `fileEtag`、`contentName`(重名时返回改名后) | 66 |
| **重命名目录** | POST | `.../richlifeApp/devapp/updateCatalogInfo` | `catalogID`、`catalogName`、`manualRename` | `dirEtag`、`catalogName` | 75 |
| **删除目录/文件** | POST | `.../richlifeApp/devapp/delCatalogContent` | `catalogIDs[]`(≤200)、`contentIDs[]`(≤200)、`contentURLs`(≤200)、`oprReason` | `result`。**文档只说“删除”，未说明是否进回收站 → 未确认** | 68 |
| **移动目录/文件** | POST | `.../richlifeApp/devapp/moveContentCatalog` | `contentInfoList[]`(≤200)、`catalogInfoList[]`(≤200)、`newCatalogID`、`extInfo` | 操作结果。**无独立“复制”接口 → 官方复制未确认/不存在** | 69 |
| **创建(限定)目录** | POST | `.../richlifeApp/devapp/createCatalogExt` | `parentCatalogID`、`newCatalogName` | `catalogInfo{catalogID,catalogName,catalogType,parentCatalogID,...}`。**限制在 我的应用收藏(00019700101000000071)/appName 文件夹下** | 73 |
| **查询内容信息(批量)** | POST | `.../richlifeApp/devapp/getContentIDSite` | `getContentIDSiteReq`（批量，上限默认 200） | 内容信息列表 | 72 |
| **异步查询个人网盘内容** | POST | `.../richlifeApp/devapp/getIndividualContentAsync` | `contentType`、`contentSortType`、`sortDirection`、`startNumber/endNumber`、`beginDate/endDate`、`minDuration/maxDuration`、`isForceRefresh` | `contentList[]`、`endNumber`、`finish`、`isReady`、`delay` | 71 |
| **搜索文件** | POST | `https://miniapp.yun.139.com/richlifeApp/search/SearchFile` | `conditions{keyword,type(0全部/1视频/2音频/3文档/4图片/5文件夹/6其他),docType,fullFileldPath}`、`showInfo{startNum,stopNum,sortInfos,returnTotalCountFlag}` | `data[]{fileId,name,type(file/folder),createdAt,updatedAt,size,parentFileId,extension,contentHash,thumbnailList[],playInfoList[],idPath,...}`。**返回的是新版 hash 形态 fileId** | 281 |
| 获取能力应用目录id | POST | `http://miniapp.yun.139.com/richlifeApp/devapp/getAppFileId` | `type`(1=我的应用收藏, 2=检查报告) | 目录 id | 285 |

#### (2) 上传（两阶段：先要 URL，再直传存储）

| 功能 | 方法 | 完整 URL | 说明 | 来源(id) |
|---|---|---|---|---|
| **上传(第一/二阶段)** | POST | `https://miniapp.yun.139.com/richlifeApp/devapp/pcUploadFileRequest`（原 `pcUploadFileRequest`） | 第一阶段：`totalSize`、`uploadContentList[]`、`newCatalogName`、`parentCatalogID`、`operation`(0普通上传/3内容备份判重秒传/…)。响应 `uploadResult{uploadTaskID, redirectionUrl, newContentIDList[]{contentID,contentName,isNeedUpload,fileEtag,fileVersion}}`。第二阶段：POST `redirectionUrl`，头 `Authorization: Basic …`(固定填)、`Content-Type …;name=`、`Range`、`contentSize`、`uploadtaskID`、`Content-Length`，body 为二进制。支持断点/分片 | 89 |
| **限定目录下上传** | POST | `.../richlifeApp/devapp/uploadFile` | 同上两阶段，`parentCatalogID` **限制在 我的应用收藏/appName 文件夹下**。响应 `redirectionUrl`(如 `.../storageWeb/servlet/uploadFileServlet?uploadCode=…&txType=0&oprChannel=…`) | 74 |
| **断点/分片上传(第二阶段)** | POST | 第一阶段返回的 `redirectionUrl`（NDSC `storageWeb/servlet/pcUploadFile`） | 头：`Authorization`(Basic固定)、`Content-Type …;name=`、`Range`、`rangeType`(0服务器为准/1强制客户端/2最后一片)、`contentSize`、`uploadtaskID`、`Content-Length`。响应 `resultCode`(0=成功/9414失败/9111 Range不符)、`CorrectRange`。**单线程、顺序分片，不支持并发/乱序** | 89 |
| 同步断点上传任务信息 | POST | `.../richlifeApp/devapp/syncUploadTaskInfo` | 客户端请求同步断点上传任务信息 | 90 |

> **单文件上传大小限制**（id=89）：普通用户 4G / 白银会员 8G / 黄金会员 20G / 钻石会员 50G。
> 秒传：`operation=3` 时，同目录同名同摘要 → 秒传；同名不同摘要 → 自动改名上传（后缀 `_Digest值`）。

#### (3) 下载

| 功能 | 方法 | 完整 URL | 说明 | 来源(id) |
|---|---|---|---|---|
| **下载(取直链)** | POST | `https://miniapp.yun.139.com/richlifeApp/devapp/downloadRequest` | `contentID`(单条或 `A\|B\|C` 最多50条)、`OwnerMSISDN`、`entryShareCatalogID`。响应 `downloadURL`（形如 `.../storageWeb/servlet/pcDownloadFile?code=…&contentID=…&oprChannel=…`）。**直链带临时 code，需 HTTP GET；支持断点续传（Range 由客户端维护）** | 91 |
| 分享下载 | POST | `.../richlifeApp/devapp/dlFromOutLink` | 下载分享文件 | 102 |

> 预览/永久直链：`getContentInfo` 返回的 `presentURL` 形如 `.../storageWeb/servlet/GetFileByURLServlet?root=…&fileid=…&code=…&exp=20210616165658&account=…&…`，**带 `exp`（YYYYMMDDHHMMSS）过期时间 → 临时直链**（id=70）。

#### (4) 读取用户信息 / 配额

| 功能 | 方法 | 完整 URL | 响应 | 来源(id) |
|---|---|---|---|---|
| 获取用户信息 | POST | `.../richlifeApp/devapp/getUserInfo` | `visualProUserInfo{msisdn,userID,areaCode,...}`（手机号已 AES 加密，见下） | 92 |
| **获取个人云空间信息** | POST | `.../richlifeApp/devapp/getDiskInfo` | `diskInfo{freeDiskSize,diskSize}` | 93 |
| 获取家庭云空间信息 | POST | `.../richlifeApp/devapp/andAlbum/openApi/getServiceDiskInfo` | `serviceDiskInfo` | 95 |
| 获取用户账号状态 | POST | `.../richlifeApp/devapp/getUserAccountBrief` | 账号状态 | 96 |
| 会员订购关系查询 | POST | `.../richlifeApp/devapp/queryUserBenefits` | 会员信息 | 97 |
| 查询用户空间信息 | POST | `.../richlifeApp/devapp/getServiceDiskInfoV2` | 空间信息 | 98 |

> 手机号字段已 AES 加密：**密钥 = secretkey 的 32 位小写 md5**；过渡期提供 `{"aes":""}` 密文标签（id=53）。

#### (5) 分享（能力清单含“分享”）

| 功能 | 方法 | URL | 来源(id) |
|---|---|---|---|
| 创建分享链接 | POST | `.../richlifeApp/devapp/getOutLink` | 99 |
| 获取分享详情 | POST | `.../richlifeApp/devapp/getOutLinkInfo` | 101 |
| 下载分享文件 | POST | `.../richlifeApp/devapp/dlFromOutLink` | 102 |
| 转存分享文件 | POST | `.../richlifeApp/devapp/dl2DFromOutLink` | 103 |

#### (6) 家庭云 / 共享群（官方有的接口，摘列）

- 家庭云（menu 634，id 108–118）：邀请加入、查成员、上传、**获取家庭云文件列表 / V3**、下载、删除、相册列表 / V3、家庭云列表。
- 共享群（menu 635，id 120–137,287,289）：上传、成员、入群邀请/码、**查询群主指定目录**、下载、移动、删除、重命名、群消息、群列表、创建共享群、设置权限。
- **官方无“回收站/回收桶”接口**（文件管理菜单下无此类文章）→ 官方开放平台**未提供回收站管理能力**。

#### (7) 通用返回码（id=180）

`resultCode` 分类：`0` 成功；Client 类 `200000400`–`200000411`（参数错/鉴权失败/包过大/未开户/会话超时/空间不足 `200000407`/重名 `200000408`/无权限 `200000409`/超限 `200000411`）；Server 类 `200000500`–`2000009xx`（含目录层级超限 `200000650`）。

### C2. 网页端私有 API（Alist/OpenList 139 驱动）

来源：Alist `drivers/139/*.go`（main 分支，已抓取源码）。**这是逆向的非官方接口，官方随时可改。** Alist 已把「旧个人云 / 新个人云 / 家庭云 / 共享群 / 分享」合并进**同一个 `139` 驱动**，用 `type` 选择：`personal`(旧个人云) / `personal_new`(新个人云) / `family` / `group` / `share`。
> 注：任务里提到的 `drivers/139_nd` 在目前 main 分支**不存在**（GitHub API 列目录只有 `139`、`189`、`189pc`）；所谓「新个人云」现在是 `139` 驱动里的 `personal_new` 类型。

**签名与公共头**（`request` / `personalRequest`）：`Mcloud-Sign: "{ts},{randStr},{sign}"`、`Authorization: Basic {base64(pc:account:authToken)}`、`mcloud-channel:1000101`、`mcloud-client:10701`、`mcloud-version:7.14.0`、`x-SvcType:1`、`Caller:web` 等。

新个人云（`type=personal_new`）host 由 `POST https://user-njs.yun.139.com/user/route/qryRoutePolicy` 返回的 `httpsUrl` 动态决定（modName=`personal`）；DeevTools 里表现为 `hcy/file/list`。

| 功能 | 方法 | 路径（拼在个人云 host 后） | 关键字段 | 来源 |
|---|---|---|---|---|
| **列目录** | POST | `/file/list` | `parentFileId`、`pageInfo{pageCursor,pageSize}`、`orderBy`、`orderDirection`、`imageThumbnailStyleList` → `items[]{fileId,name,size,type(folder/file),createdAt,updatedAt,thumbnailUrls[]}`、`nextPageCursor` | util.go:personalGetFiles |
| **下载直链** | POST | `/file/getDownloadUrl` | `fileId` → `data.cdnUrl`（或 `data.url`） | util.go:personalGetLink |
| **上传(初始化+取分片URL)** | POST | `/file/create` | `name,size,parentFileId,type:"file",contentHash,contentHashAlgorithm:"SHA256",parallelUpload:false,partInfos[],fileRenameMode:"auto_rename"` → `data{fileId,fileName,uploadId,partInfos[{partNumber,uploadUrl}],exist,rapidUpload}` | driver.go:Put |
| 补取后续分片URL | POST | `/file/getUploadUrl` | `fileId,uploadId,partInfos[]` | driver.go:Put |
| **上传分片(直传 EOS)** | PUT | `partInfos[].uploadUrl`（对象存储 EOS） | Header `Content-Type: application/octet-stream`、`Content-Length`、`Origin/Referer=https://yun.139.com/`，body=分片二进制 | driver.go:Put |
| **完成上传** | POST | `/file/complete` | `fileId,uploadId,contentHash,contentHashAlgorithm` | driver.go:Put |
| **新建目录** | POST | `/file/create` | `parentFileId,name,description:"",type:"folder",fileRenameMode:"force_rename"` | driver.go:MakeDir |
| **重命名** | POST | `/file/update` | `fileId,name,description` | driver.go:Rename |
| **移动** | POST | `/file/batchMove` | `fileIds[],toParentFileId` | driver.go:Move |
| **复制** | POST | `/file/batchCopy` | `fileIds[],toParentFileId` | driver.go:Copy |
| **删除(进回收站)** | POST | `/recyclebin/batchTrash` | `fileIds[]` | driver.go:Remove |
| 视频预览 | POST | `/videoPreview/getPreviewInfo` | — | util.go |
| 传输上限提示 | — | Header `X-Yun-Client-Info` 第三段决定单文件上限：`9`(网页)/`2`(App)=50GB、`13`(PC客户端)=500GB | — | util.go:personalRequest 注释 |

旧个人云（`type=personal`，host `https://yun.139.com`）：

| 功能 | 路径 | 来源 |
|---|---|---|
| 列目录 | `/orchestration/personalCloud/catalog/v1.0/getDisk` | util.go:getFiles |
| 新建目录 | `/orchestration/personalCloud/catalog/v1.0/createCatalogExt` | driver.go:MakeDir |
| 改名(目录/文件) | `.../catalog/v1.0/updateCatalogInfo` / `.../content/v1.0/updateContentInfo` | driver.go:Rename |
| 移动/复制/删除 | `/orchestration/personalCloud/batchOprTask/v1.0/createBatchOprTask`（taskType+actionType: 3/304=移动, 309=复制, 2/201=删除） | driver.go:Move/Copy/Remove |
| 下载取链 | `.../uploadAndDownload/v1.0/downloadRequest` | util.go:getLink |
| 上传取链 | `.../uploadAndDownload/v1.0/pcUploadFileRequest` | driver.go:Put |

家庭云：`/orchestration/familyCloud-rebuild/*`（getFileUploadURL / getFileDownLoadURL / createCloudDoc / modifyCatalogInfo / modifyContentInfo / createBatchOprTask / queryContentList v1.2）。
共享群：`/orchestration/group-rebuild/*`（createGroupCatalog / modifyGroupCatalog / modifyGroupContent / queryGroupContentList / getGroupFileDownLoadURL / createBatchOprTask）。

---

## D. 文件 ID / 路径模型

**官方开放平台（两套模型并存）**：

1. **旧 model（catalogID / contentID）**：
   - 目录用 `catalogID`(String 32)，**根目录 = 字符串 `root`**（`CatalogInfo.catalogID` 描述：「根目录默认为 root」；`getdisk` 请求示例 `<catalogID>root</catalogID>`）。来源 id=143、id=67。
   - 文件用 `contentID`(String 32)。来源 id=70、id=179。
   - 特殊固定目录：**我的应用收藏 = `00019700101000000071`**（id=73/id=74）；`getAppFileId(type=1)` 可获取“我的应用收藏”id（id=285）。
   - 全路径形式：`A/B/C`（目录）、`A/B/C/1`（文件，尾部为文件ID），支持在删除/移动里用全路径（id=68/id=69）。

2. **新 model（fileId + idPath）**：搜索接口 `SearchFile` 返回 `fileId`（40 位 hash，如 `63f6d7ab2015fe258fa44a2d9df867e9b5074193`）与 `idPath: "root:/<fileId>/..."`、`parentFileId`、`driveId`、`category`、`isRootDir`、`isSystemDir`（id=281）。**这两套 ID 是否互通、能否把新 fileId 传给旧接口，官方文档未说明 → 未确认。**

**网页端私有（新个人云）**：
- `fileId` = hash 字符串；**根目录 = `/`**（Alist `personal_new` 默认 RootFolderID=`/`；OpenList 文档：根目录留空或填 `/`）。来源：Alist meta.go/driver.go、OpenList 139 文档。
- 旧个人云根目录 = `root`；共享群根 = `0`（types.go 注释「根目录是"0"」）。

---

## E. 限流、QPS、文件大小与账号限制

| 约束 | 值 | 来源 |
|---|---|---|
| 频控 | 每个官方接口「频控限制：有」，但**未公布具体 QPS/日调用量** → 具体数值**未确认** | id=53/id=67 等 |
| 列目录分页 | `startNumber/endNumber`；为 -1 时返回前 200 条；单页最多 200 条 | id=67 |
| 批量删除/移动 | 每次最多 200 个目录 + 200 个内容 | id=68/id=69 |
| 批量下载 | 每次最多 50 个内容 | id=91 |
| 批量查内容信息 | 单次默认上限 200 | id=72 |
| 单文件上传上限（官方） | 普通 4G / 白银 8G / 黄金 20G / 钻石 50G | id=89/id=74 |
| 搜索默认返回上限 | 默认最多 10000 条（`returnTotalCountFlag=true` 解除） | id=281 |
| tag 数 | 每个内容最多 6 个 | id=66/id=179 |
| 文件名后缀过滤 | `contentSuffix` 最多 100 个后缀，竖线分隔 | id=67 |
| accessToken 有效期 | **30 天**（`expiresIn` 单位为小时） | id=51/id=266 |
| 同 appId+手机号重复授权 | 会**清除上次 accessToken**（FAQ 20011） | id=266 |
| deviceid 一致性 | 取 token 与调接口的 deviceid 必须一致（否则 20010） | id=266 |
| channelid 一致性 | 必须与申请能力时勾选终端一致（否则 20009） | id=266 |
| 新版个人云 EOS 单对象 | Alist 文档转述 EOS 文档「无法上传大小大于 > 5 GB 的对象」，故建议分片 ≤5GB；异步分片默认 100MB，网页端分片 20MB | https://alistgo.com/zh/guide/drivers/139.html |
| 新个人云客户端上限 | Header `X-Yun-Client-Info` 决定：网页/App=50GB，PC 客户端=500GB | Alist util.go 注释 |

---

## F. 官方开放平台缺失的能力 与 网页端私有 API 补齐

**核心结论：官方开放平台 API 无法满足「完整可读写的本地文件系统挂载」。**

| 挂载所需能力 | 官方开放平台 | 说明 / 补齐方式 |
|---|---|---|
| 列目录 | ✅ `getdisk` | 按 `catalogID` 分页 |
| 文件详情 | ✅ `getContentInfo` | 含大小/时间/摘要 |
| 下载（直链） | ✅ `downloadRequest` | 返回带临时 `code` 的直链；支持 Range |
| 缩略图/预览 | ✅ `getContentInfo`(thumbnailURL/bigthumbnailURL/presentURL) 与 `SearchFile`(thumbnailList/playInfoList) | 官方**有**缩略图与预览地址 |
| 上传(小文件) | ✅ `uploadFile` / `pcUploadFileRequest` | **但目录被限制在“我的应用收藏/appName”下** |
| 分片/断点上传 | ✅ 两阶段 + `Range/rangeType` | 单线程、顺序分片（不支持并发/乱序） |
| 创建文件夹 | ⚠️ `createCatalogExt` | **只能建在 我的应用收藏/appName 下** |
| 删除 | ✅ `delCatalogContent` | 是否进回收站**未确认** |
| 重命名 | ✅ `updateContentInfo` / `updateCatalogInfo` | |
| 移动 | ✅ `moveContentCatalog` | |
| 复制 | ❌ **官方无复制接口**（文档菜单无） | 私有 API `/file/batchCopy` |
| **回收站管理** | ❌ **官方无回收站接口** | 私有 API `/recyclebin/batchTrash`（仅删除进回收站） |
| **全盘随机读写挂载** | ❌ **不满足**：创建/上传被限制在固定应用目录；官方定位是「把云盘能力嵌入第三方 App」，不是「把云盘挂成本地盘」 | 要完整挂载只能用**网页端私有 API**（Alist/OpenList 方式） |
| 分享/转存 | ✅ | |
| 家庭云/共享群 | ✅ | |

**必须靠网页端私有 API 补齐的能力**：复制、回收站、**以及对任意目录的创建/上传**（官方限制在应用目录）。也就是说——**要做 fnOS 全盘挂载，只能用逆向的网页端 API**（`yun.139.com` + 动态个人云 host + `Authorization`/`Mcloud-Sign`），Alist/OpenList 的 `139` 驱动（`personal_new`）即此方案，**非官方、不稳定、随时可能失效**（OpenList 明确声明不再主动维护）。

**参考实现状态**：
- **Alist/OpenList**：有 `drivers/139`（合并了 personal_new 新个人云），可挂载、读写、EOS 分片直传；OpenList 文档声明为逆向接口、不主动维护。来源：https://github.com/AlistGo/alist/tree/main/drivers/139 、https://doc.oplist.org/guide/drivers/139 。
- **CloudDrive2**：其官网列出的支持网盘为 OneDrive / Google Drive / 百度网盘 / 迅雷云盘 / 123云盘 / 阿里云盘Open / 115open / 天翼云盘 / 光鸭云盘，**列表中不含中国移动云盘/139** → CloudDrive2 **不支持**移动云盘挂载；CloudDrive2 有 fnOS(飞牛) 的 fpk 包。来源：https://www.clouddrive2.com/features.html 、https://www.clouddrive2.com/apps/desktop.html 。
- **Rclone**：未检索到官方 139/移动云盘 backend → **未确认（很可能不支持）**。

---

## G. 2026-10 增量：智能体通道 (IDaaS v3) 与官方 Skill

> 完整整合版见 `research/open-platform-139/LATEST-2026-10-02.md`。此处只留速查结论与对挂载的判定。

### G1. 平台侧新动态

- 首页横幅（新）：**「移动云盘官方skill已开放」，已上架 WorkBuddy 及 OpenClaw**。
  安装口令：`请根据 https://skillhub.cn/install/skillhub.md，安装 @org-vt43r0t0/cm-cloud-manage。`
  介绍页：https://skillhub.cn/skills/org-vt43r0t0/cm-cloud-manage （发布方：中移互联网有限公司，v2.0.0 / 2026-09-30）。
- 公告仍停在 2022 年两条（和彩云改名）；**平台未公告任何 API 变更**；公开文档站 109 篇结构与 2023–2024 一致，
  新增仅一篇《AI开放平台技术文档-JSSDK》(id=326, 2026-03-18，面向 ai.139.com 的第三方 AI 应用接入，与文件 API 无关)。

### G2. 新通道：官方智能体 IDaaS v3

| 项 | 值 |
|---|---|
| 前缀 | `https://openapi.yun.139.com/open-ai-space` + `/richlifeApp/idaasV3/**`（另有 `/richlifeApp/idaas/user/folder/query`） |
| 方法/头 | POST JSON；公共头 `version: 2.0.0`；部分接口带 `x-yun-client-info`（17 段）、`x-DeviceInfo`（相册分享） |
| 鉴权 | **IDaaS v3 SDK 扫码/短链授权**，按设备指纹绑定并加密存凭据；临时票据最多复用 5 分钟；多账号用 `--profile` 隔离 |
| 文件接口 | `file/{list,get,create,createFolder,complete,getUploadUrl,getDownloadUrl,batchGet,batchGetDownloadUrl,batchCopy,batchCopySync,listUploadedParts}`、`task/get` |
| 其他 | `/search/Search{SkillFile,ShareRecord,SubscribeShareCode,NonFileShareRecord,AIStory}`、`/personalDynamic/queryPersonalDynamic`、`/devapp/share/*` 外链、`/personalSaas/album/{share,photo/customization,story/memory}/*`、`/bookshelf/readingRecord/list`、`/api/openclaw/photoOrganize/task[/query]` |
| **策略停用**（代码已实现，仅注释） | `file/batchDeleteAsync`、`file/batchMoveAsync`、`file/update`、`file/batchGetPath`、`file/searchV2` |
| 能力边界 | **不支持云端删除/移动/改名/改描述**；**读取覆盖整个个人云，写操作（上传/建目录/复制/转存）只能落在「智能体专属目录」及其子目录**；相册与分享创建/订阅不受此限。越权写 → 业务码 `16110013`；平台拒绝 `SCOPE_DENIED` = HTTP 403 / `13000006` |
| 换票错误码 | `110044` 缓存失效、`130090` uid 失效、`16110038` Token 有效期不足、`01000003` 令牌失效（HTTP 401 同效）→ 清缓存重取，**只重试一次** |

### G3. 对挂载（`cmccyun.fpk`）的判定 —— **不变**

1. 官方两条通道都限制写入目录（devapp：我的应用收藏/appName；IDaaS v3：智能体专属目录），且 IDaaS v3 策略上不给删除/移动/改名。
   → **「官方合规的全盘可读写挂载」目前不存在；仍只能用网页端私有 API（路线 C）**，本次调研只是强化了这一结论。
2. 私有 API 未见封禁或变更迹象 → `cmccyun.fpk` 架构与风险提示**无需修改**（授权约 30 天过期仍成立）。
3. 可选借鉴：IDaaS v3 的上传三段式（`getUploadUrl` → 分片 → `complete` + `listUploadedParts`）、`x-yun-client-info` 头口径、
   以及上述错误码到用户话术的映射，可用于完善本项目的上传重试与 UI 排错提示。

---

## 附：可核实的原始文档来源

官方文档站是 SPA，正文由以下接口返回（均 `POST`，`Content-Type: application/json`）：

```
GET(实际POST) https://open.yun.139.com/open-user/api/v4.6/HelpDocuments/getOpenTree          body {"type":"API"}   # 文档目录
POST          https://open.yun.139.com/open-user/api/v4.6/HelpDocuments/getOpenListByMenuId  body {"id":<menuId>}  # 某目录下文章列表
POST          https://open.yun.139.com/open-user/api/v4.6/HelpDocuments/getOpenContent       body {"id":<articleId>} # 文章正文(HTML)
```

本文引用的文章 id 对照：

| id | 标题 | id | 标题 |
|---|---|---|---|
| 51 | API基础能力接入指引 | 89 | 上传(UploadFileRequest) |
| 53 | 接口调用统一说明 | 90 | 同步断点上传任务信息 |
| 66 | 重命名文件名称 | 91 | 下载(downloadRequest) |
| 67 | 查询个人云文件列表(getdisk) | 92 | 获取用户信息 |
| 68 | 删除目录/文件 | 93 | 获取个人云空间信息 |
| 69 | 移动目录/文件 | 95–98 | 家庭云空间/账号状态/会员/空间信息 |
| 70 | 查询文件信息 | 99/101/102/103 | 分享相关 |
| 71 | 异步查询个人网盘内容 | 108–118 | 家庭云接口 |
| 72 | 查询内容的信息(批量) | 120–137/287/289 | 共享群接口 |
| 73 | 创建限定目录下子目录 | 142–179 | 数据类型说明(结构体) |
| 74 | 限定目录下上传文件 | 180 | 通用返回码 |
| 75 | 修改目录信息 | 210 | aes加密 |
| 77 | 签约接入指引 | 254 | 二维码扫码登录 |
| 281 | 搜索文件 | 266/267 | FAQ-技术类/合作类 |
| 285 | 获取能力应用目录id | 125 | 能力开放服务协议 |

原始正文可按上面接口自取，或浏览器打开 `https://open.yun.139.com/#/help-document-new/index?type=API&id=<articleId>`。

第三方参考：
- Alist `drivers/139` 源码：https://github.com/AlistGo/alist/tree/main/drivers/139 （`driver.go`/`util.go`/`types.go`/`meta.go`/`upload_parallel.go`）
- Alist 139 使用教程：https://alistgo.com/zh/guide/drivers/139.html
- OpenList 139 文档：https://doc.oplist.org/guide/drivers/139
- CloudDrive2 支持网盘：https://www.clouddrive2.com/features.html
- CloudDrive2 fnOS 支持：https://www.clouddrive2.com/apps/desktop.html

---

### 未确认清单（禁止照抄）

1. 个人能否实际通过审核拿到可调用能力（注册路径要求企业认证，协议正文允许自然人）。
2. 是否有沙箱/测试环境。
3. `delCatalogContent` 删除是否进回收站。
4. 官方是否有“复制”接口（文档未列，倾向没有）。
5. 官方新/旧两套 fileId/catalogID 是否互通。
6. 具体 QPS/日调用量等频控数值。
7. 基础能力是否收费。
8. Rclone 是否支持 139/移动云盘。
9. 基础能力开放平台是否提供 WebDAV 或任何「挂载」形态的官方输出（文档未提及）。
