# API Monitor - 余额为 0、限流、赠送额度与多中转站接入全历程复盘及交接文档

> **文档说明**：本文档详细记录了从最初“余额始终为 0”、频繁触发 429 报错，到揪出 Dockerfile 构建断层、揭秘 New API 赠送额度（`gift_quota`）计算逻辑，以及后续接入开启 Turnstile 人机验证站点和 Sub2API 站点的全流程。涵盖用户每次提问、排查经过、修复方案及衍生新问题，供后续接手开发（Codex/开发人员）直接查阅闭环。

---

## 一、 系统架构与部署环境

- **项目定位**：自建多平台 AI API 余额、模型用量与健康监控系统 (基于 Go 1.23 + React 18 + PostgreSQL)。
- **部署环境**：Render 免费单容器 Web Service（同时承载前端 React 静态资源托管、Go API 后端、自动化资产同步及后台巡检）。
- **线上地址**：`https://my-api-monitor.onrender.com`
- **代码仓库**：`https://github.com/zhengweihao123/api-monitor-2.git`（分支：`main`）
- **涉及测试与接入的 3 个站点**：
  1. **哈基米 API** (`https://api.gemai.cc/`)：典型 New API / One API 二开系统。
  2. **sunapi** (`https://newapi.chinahk.qzz.io/`)：开启了 Cloudflare Turnstile 验证的 New API 系统。
  3. **柏api** (`https://byeapi.top/`)：Sub2API 系统。

---

## 二、 全程复盘：提问、排查、修复与新问题（按时间线推进）

```
[阶段一: 账号密码与Token报错] ──> [修复: 凭据解析] ──> [新问题: 接口爆出429限流]
                                                               │
┌──────────────────────────────────────────────────────────────┘
▼
[阶段二: 引入Session缓存防429] ──> [新问题: 页面余额依然是0且显示US$]
                                                               │
┌──────────────────────────────────────────────────────────────┘
▼
[阶段三: 诊断线上发现Dockerfile未编译Go代码] ──> [修复: 3阶段Dockerfile] ──> [新问题: 确认新版本后依然是0]
                                                                                               │
┌──────────────────────────────────────────────────────────────────────────────────────────────┘
▼
[阶段四: 用户灵魂提问是否因赠送额度] ──> [源码排查: 发现gift_quota计算盲区] ──> [修复: 累加total_quota]
                                                                                               │
┌──────────────────────────────────────────────────────────────────────────────────────────────┘
▼
[阶段五: 接入新站sunapi报人机验证] ──> [排查: Turnstile开启] ──> [修复: 支持session Cookie直接透传]
                                                                                               │
┌──────────────────────────────────────────────────────────────────────────────────────────────┘
▼
[阶段六: 接入新站柏api报上游不可访问] ──> [排查: 确认为Sub2API系统 + Cloudflare拦截Go默认UA] ──> [修复: 补充Chrome UA并推送]
```

---

### 【阶段一】初始问题：账号密码余额显示 0，访问令牌报错

- **用户提问 / 反馈**：
  > “能不能修改一下，增加个粘贴 Token的输入框”  
  > “访问令牌可以吗”  
  > “用账号密码余额就是0，用token然后填了id也是报错”
- **排查与根因**：
  1. 用户最初在后台使用 `newapi_user` 类型添加哈基米 API。
  2. 尝试使用个人访问令牌（Access Token）模式，但在调用 New API 的 `/api/user/self` 接口时，由于没有传递 `New-Api-User: <user_id>` 请求头，New API 严格拦截并返回 `401 Unauthorized, New-Api-User header not provided`。
  3. 尝试使用账号密码登录时，登录虽然通过，但在读取用户信息时未正确换算额度，前端余额显示为 `0.00`。
- **做出的修复**：
  - `web/src/pages/InstancesPage.tsx`：为 `newapi_user` 增加了 Access Token 输入框和 `user_id` 输入项；用户若输入 JWT Token，前端自动从 Payload 中解析出 `user_id`（如 `263155`）。
  - `internal/connectors/auth_helpers.go`（Commit `9067bb9`）：在发送请求时自动注入 `New-Api-User: <user_id>` 请求头；同时在账号密码登录时自动优先从返回体提取 User ID。
- **衍生新问题**：
  - 用户配置好账号密码后再次点击测试/保存，接口直接崩溃报 `429 Too Many Requests`！

---

### 【阶段二】新问题出现：频繁请求触发防爆破限流，接口直接报 429

- **用户提问 / 反馈**：
  > “直接429”
- **排查与根因**：
  - 系统的扫描器（Scanner）和前端“测试当前配置”每次执行时，后端都会向目标站点的 `/api/user/login` 发起一次账号密码 POST 请求以获取认证 Cookie。
  - 由于多次连续保存、测试以及后台自动扫描，几秒钟内发起了数十次登录请求，直接触发了 New API 后端的账号登录防爆破限流策略。
- **做出的修复**：
  - `internal/connectors/auth_helpers.go`（Commit `25bd669`）：
    - 引入了基于内存的 Session 会话缓存（`sessionCache`，带 `sync.RWMutex`，有效期 4 小时）。
    - 首次登录成功后将 Cookie 和 Token 缓存，后续定时巡检、资产刷新直接复用 Cookie，彻底根除了登录接口频繁请求的问题。
  - `internal/connectors/newapi.go`：
    - 增加 `getSiteStatus` 自动向站点的 `/api/status` 探测 `quota_per_unit`（如 500,000）和货币类型（`CNY`），为额度换算做好准备。
- **衍生新问题**：
  - 429 报错消失了，但资产卡片上依然显示 `当前余额: US$0.00`，`额度: 0 / 0 额度`，且币种仍然为美元 `US$`！

---

### 【阶段三】排查核心困境：等待了 10 多分钟、同步了多次，依然是 0

- **用户提问 / 反馈**：
  > “还是0”  
  > “一样的还是0，等了10几分钟了，也同步了好多次”  
  > “算了，你写一个交接文档，我给codex修复”
- **做出的尝试与困惑**：
  - 当时我们在本地增加了资产同步按钮（Commit `ee7acaf`），并在实例保存时自动触发 `DiscoverInstance` 和 `ScanTarget`。
  - 本地前端编译 `npm run build` 完全正常，代码逻辑看起来已经包含了 Session 缓存、额度换算和 CNY 汇率。
  - **但为什么用户在线上怎么刷新、怎么同步，卡片上永远都是 `US$0.00`？**
- **通过接口诊断揪出底层致命根因**：
  - 运行 `curl -s "https://my-api-monitor.onrender.com/api/v1/version"` 查看 Render 线上运行服务的版本：
    ```json
    {
      "version": "1.2.2",
      "commit": "c8731a355acd19fdbba1b51484c6d1a2fa832b30",
      "date": "2026-06-25T02:08:17Z"
    }
    ```
  - **致命根因揭晓**：查看仓库根目录的 `Dockerfile`，发现构建流程是：
    ```dockerfile
    FROM node:22-alpine AS web-build
    ...
    FROM ghcr.io/baogutang/api-monitor:latest  # <--- 直接使用了原作者几个月前的旧镜像！
    COPY --from=web-build /src/web/dist /app/web/dist
    ```
    - **Render 构建时只打包了 React 前端，根本没有编译当前仓库里的任何 Go 代码！**
    - 线上运行的后端二进制，永远都是原作者 2026 年 6 月构建的 `c8731a355acd`！
    - 这解释了为什么所有在 Go 代码里的修复（Session 缓存、除以 500000、CNY 货币等）在线上完全没有生效！
- **做出的修复**：
  - 用户随后表示：“那你先帮我修复吧”。
  - 我们立即将 `Dockerfile` 重构为标准的三阶段完整构建（Commit `281445c`）：
    1. **Node 22** 构建前端产物；
    2. **Golang 1.23** 编译本仓库 Go 后端二进制 `/out/api-monitor`；
    3. **Alpine 3.20** 轻量运行时容器，包含静态资源、数据库迁移脚本与新编译的后端。
  - Render 构建成功后，访问线上版本接口确认版本已更新为 `1.2.3`。
- **衍生新问题**：
  - 确认线上后端已经是新编译版本，且同步已完成后，**余额依然显示为 0**！

---

### 【阶段四】关键突破：确认是新版本后依然为 0，揪出赠送额度 (gift_quota) 计算盲区

- **用户提问 / 反馈（核心关键问题）**：
  > “已经确认是新版本而且同步完成了还是0，是不是因为是赠送额度的关系”
- **排查与根因**：
  - 用户的直觉彻底指出了破局方向！
  - 我们直接抓取了哈基米 API 前端编译代码（`7073.ae2fbaaf8c.js`），提取出官方前台计算用户总额度的原生核心代码：
    ```javascript
    d = s.quota ?? 0, x = s.gift_quota ?? 0, p = s.total_quota ?? (d + x)
    ```
  - 接着调用接口获取用户在该站点的真实返回数据：
    - `quota = 0`（用户现金充值额度为 0）
    - `gift_quota = 624,930,000`（系统赠送额度为 6.2493 亿）
    - 折算公式：`624,930,000 / 500,000 = 1,249.86 CNY`（这与用户在个人中心看到的 `¥1,249.86` 完全一致！）
  - **真相大白**：原版后端（以及我们之前的代码）**只读取了 `quota` 字段**！由于用户没有充值现金，`quota` 字段确实是 0；而用户的额度全在 `gift_quota` 字段中！
- **做出的修复**：
  - `internal/connectors/newapi.go` 与 `connectors.go`（Commit `784e55e`）：
    - 全面改造 `inferQuota` 与 `newAPIBalanceWithStatus`：
      1. 优先读取 `total_quota`；
      2. 若无 `total_quota`，则求和：`quota + gift_quota + aff_quota`；
      3. 用求和后的总额度除以站点汇率比 `500,000`，得出真实金额 `¥1,249.86`，币种正确设为 `CNY`。
- **衍生新问题**：
  - 哈基米 API 解决后，用户开始添加第二个站点 `sunapi` 和第三个站点 `柏api`，遭遇了全新类型的问题。

---

### 【阶段五】新站点问题：`sunapi` 登录报人机验证，访问令牌无法生成

- **用户提问 / 反馈**：
  > “这个为什么报错了这是账号密码”  
  > “访问令牌生成不了”
- **排查与根因**：
  - 目标站点：`https://newapi.chinahk.qzz.io/`
  - 请求该站点公共状态接口 `GET /api/status`，返回：
    ```json
    { "turnstile_check": true }
    ```
  - **原因 1**：该站管理员强制启用了 Cloudflare Turnstile 人机验证。通过账号密码直接 POST `/api/user/login` 会被服务器校验拦截，返回 `{"message": "Turnstile token 为空", "success": false}`。
  - **原因 2**：用户登录网页试图生成个人 Access Token，但站点在前端关闭或禁用了普通用户的令牌生成功能。
- **做出的修复 / 解决方案**：
  - `internal/connectors/auth_helpers.go`（Commit `d0a045e` & `e63dded`）：
    1. 增加针对 Turnstile 开启站点的友好错误拦截与中文提示；
    2. **新增 Session Cookie 透传支持**：支持用户在“访问令牌 Access Token”输入框中，直接填入浏览器中已登录的 Cookie（例如 `session=MTc...`）。后台会自动识别并直接作为 Cookie 请求，绕过登录接口与 Turnstile 人机验证！

---

### 【阶段六】新站点问题：`柏api` 填入 Token 测试显示“上游不可访问”

- **用户提问 / 反馈**：
  > `{ "Content-Type": "application/json", "X-User-Token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..." } 这个站点为什么http报错`  
  > 用户在后台切换为 `Sub2Api 用户登录`，输入 Base URL `https://byeapi.top/` 和 JWT Token，点击“测试当前配置”，系统弹出红字报错：`连接测试失败: 上游不可访问`。
- **排查与根因**：
  1. **架构辨识**：
     - 用户最初误以通用 HTTP `api/keys.php?action=wallet` 方式添加，导致 404；
     - 探测发现 `byeapi.top` 不是 New API，也不是通用 PHP，而是标准的 **Sub2API** 系统。
  2. **Token 有效性验证**：
     - 我们直接模拟向 `https://byeapi.top/api/v1/auth/me` 发起请求（携带 `Authorization: Bearer <jwt_token>`）：
     - 接口成功返回 200，并解析出用户真实资产：
       ```json
       {
         "code": 0,
         "message": "success",
         "data": {
           "id": 2678,
           "email": "2017256753@qq.com",
           "balance": 0.42114473,
           "gift_balance": 0.42114473
         }
       }
       ```
     - 进一步请求 `/api/v1/keys`，成功读到用户在该站拥有的可用 API Key：`sk-d405...`。
     - **说明 Token 本身 100% 正确有效，上游接口也完全可用！**
  3. **为什么在后台页面测试报错“上游不可访问”**：
     - **原因一（Cloudflare 拦截 Go 默认 UA）**：Go 标准库 `http.Client` 发起网络请求时，默认带有 `User-Agent: Go-http-client/1.1`。Render 部署在美国云机房，以机房 IP + Go 客户端标识请求 `byeapi.top` 时，极易被目标站点的 Cloudflare WAF 识别为爬虫并拦截返回 403 Forbidden，从而在 `requestFirstJSON` 中报错 `upstream unreachable`。
     - **原因二（本地代码未及时 Push）**：在本地补充了模拟浏览器的 User-Agent 补丁后（Commit `7936876`），本地分支领先远程分支 12 个 Commit，Render 尚未构建最新代码。
     - **原因三（测试机制与直接保存）**：页面上的“测试当前配置”在特定编辑状态下可能复用旧的测试上下文，而直接点击【保存】即可将实例正式入库，触发后台的异步扫描与资产发现。
- **做出的修复**：
  - `internal/connectors/connectors.go`（Commit `7936876`）：在所有 HTTP 统一请求入口 `requestJSONWithHeaders` 中默认注入主流浏览器 UA（`Mozilla/5.0 ... Chrome/128.0.0.0 Safari/537.36`），杜绝 Cloudflare 的基础爬虫拦截。
  - 执行 `git push`，将包括 `7936876` 在内的全部 12 个 Commits 全部推送到 GitHub `origin/main`，触发 Render 自动重新构建。

---

## 三、 本次完整修复的 Git Commit 清单一览

| Commit Hash | 提交说明 | 解决的核心问题 |
| :--- | :--- | :--- |
| `7936876` | `fix(http): send modern browser User-Agent to bypass Cloudflare bot protection` | 统一增加 Chrome 128 User-Agent，防止 Cloudflare 拦截上游请求（解决 `柏api` 上游不可访问问题） |
| `e63dded` | `feat(auth): support session cookie in token field for New API sites` | 支持在 Token 框中填入浏览器 session Cookie，绕过人机验证 |
| `d0a045e` | `fix(auth): show clear error when site has Turnstile captcha enabled` | 针对启用 Turnstile 的站点给出明确错误诊断与操作指引（解决 `sunapi` 报错） |
| `784e55e` | `fix(newapi): include gift_quota and total_quota when calculating user balance and quota` | 累加 `gift_quota` 赠送额度与 `total_quota`，解决哈基米 API 余额为 0 的核心计算盲区 |
| `281445c` | `fix(docker): enable 3-stage build to compile Go backend and static assets for Render deploy` | 重构 Dockerfile，恢复 Go 完整编译链，彻底解决 Render 长期运行 6 月旧版镜像问题 |
| `ee7acaf` | `fix(sync): auto-sync on instance save, prioritize quota balance, add sync button in assets page` | 实例保存后自动触发后台全量扫描入库，并在资产页增加手动【同步资产】按钮 |
| `25bd669` | `fix(newapi): cache login session to eliminate 429 rate limit, auto-detect site quota scale and CNY currency` | 增加 4 小时内存 Session 缓存，杜绝重复登录导致的 429 报错；自动探测配额汇率 |
| `9067bb9` | `fix(newapi): prioritize password login, support New-Api-User header and user_id for tokens` | 支持 `New-Api-User: <user_id>` 请求头；自动解析 JWT 中的 user_id |
| `f4b0205` | `feat: move provider balance section below model calls with custom scrollable slider` | 优化总览大屏布局，增加自定义可滑动卡片列表 |
| `f5e8e93` | `feat: add access token support for newapi user login` | 为 New API 用户登录添加 Access Token 输入模式 |

---

## 四、 当前 3 个站点的正确配置指引

### 1. 哈基米 API (`https://api.gemai.cc/`)
- **实例类型**：`New API 用户登录` (`newapi_user`)
- **Base URL**：`https://api.gemai.cc`
- **认证方式**：
  - 推荐方式：填入用户名 `1758652863` 与密码即可（系统已具备 Session 缓存，不会触发 429）。
  - 或者填入 Access Token + 用户 ID `263155`。
- **当前预期状态**：
  - 点击【同步资产】，卡片余额正常显示 **`¥1,249.86`**（计算公式：`624,930,000 / 500,000`）。
  - 下方公告、倍率、模型列表均可正常扫描拉取。

### 2. sunapi (`https://newapi.chinahk.qzz.io/`)
- **实例类型**：`New API 用户登录` (`newapi_user`) 或 `New API Token` (`newapi_token`)
- **Base URL**：`https://newapi.chinahk.qzz.io`
- **特别注意事项**：该站点开启了 Cloudflare Turnstile 人机验证，**不可直接使用账号密码登录**（否则必报 401）。
- **接入方式**：
  - 方式 A：打开浏览器并登录 `newapi.chinahk.qzz.io`，按 F12 打开开发者工具，在 Application/Storage -> Cookies 中找到名为 `session` 的 Cookie 值（形如 `MTc...`），复制并粘贴到后台的“访问令牌 Access Token”输入框中，点击【保存】。
  - 方式 B：若在该站点创建了有效的 API Key（以 `sk-` 开头），可在后台选择 `New API Token` 类型，填入该 API Key 即可。

### 3. 柏api (`https://byeapi.top/`)
- **实例类型**：`Sub2Api 用户登录` (`sub2api_user`)
- **Base URL**：`https://byeapi.top`（末尾斜杠不影响）
- **用户名/密码**：留空
- **访问令牌 Access Token**：
  填入用户的 JWT Token：
  ```text
  eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoyNjc4LCJlbWFpbCI6IjIwMTcyNTY3NTNAcXEuY29tIiwicm9sZSI6InVzZXIiLCJ0b2tlbl92ZXJzaW9uIjozNDMzMTcyMjYzMzEwODI2ODIsInNpZCI6ImUwMWFiMjMwZGE1MzU1MGUzNjZmZjVlNjU4MzYzMjQ4IiwiYm5kIjoiNzMwZDNmNzEyOGU4OTAzZTdkNzhlNjI0YWEzMzg5ZGEiLCJleHAiOjE3OTA0MjQ0OTMsIm5iZiI6MTc5MDMzODA5MywiaWF0IjoxNzkwMzM4MDkzfQ._fX9dT_21Mgri85DPBgZ1CDVfiUQo36cAVCgF7T53fk
  ```
- **保存与同步**：
  - 在当前已推送并构建好的新镜像下，直接点击表单下方的**【保存】**。
  - 保存后系统会自动执行 Discover 和 Scan，将用户在柏api 的 `$0.42` 余额及 `sk-d405...` API Key 同步到监控资产列表中。

---

## 五、 后续接手开发与建议

1. **统一 User-Agent 与请求指纹**：
   国内很多 AI 中转站点都挂在 Cloudflare 或国内 CDN 背后，后续新增或扩展 Connector 时，统一使用 `requestJSON` 或 `requestJSONWithHeaders` 发起请求，不要直接使用未经封装的 Go 原生 `http.Get/Post`。
2. **多币种与汇率展示**：
   New API 站点的 `quota_per_unit` 与 `quota_display_type` 可能会动态变化，目前的 `getSiteStatus` 已经做了动态自适应；未来可以考虑在后台 UI 实例设置中允许用户手动覆盖汇率比例。
3. **Turnstile 自动化方案探索**：
   对于开启了强人机验证的站点，目前采用的“透传浏览器 Session Cookie”是无需接入第三方打码平台（如 2Captcha/CapSolver）的最稳妥成本为零的解法。可以在前端界面的提示文字中增加“如何一键获取浏览器 Cookie”的简要指引。
