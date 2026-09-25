# API Monitor - 哈基米 API 余额为 0 及 429 报错全过程复盘与交接文档

> 本文档详细记录了从问题最初出现、历次用户提问、尝试修复、衍生新问题，到最终通过线上版本诊断揪出“致命根因”的完整全过程，供 Codex 或后续接手开发者直接阅读并闭环修复。

---

## 一、 项目背景与目标资产信息

- **项目名称**：API Monitor (基于 Go 后端 + React 前端 + PostgreSQL + Docker 部署在 Render)
- **目标监控站点**：哈基米 API (`https://api.gemai.cc/`，典型的 New API / One API 二开架构)
- **用户账号信息**：
  - 用户名：`1758652863`
  - 用户 ID：`263155`
  - 邮箱：`2017256753@qq.com`
- **真实账户资产情况**（在 `https://api.gemai.cc/profile` 实测）：
  - 官网页面显示余额：**`¥1,249.86`**
  - 底层接口数据：
    - `/api/status` 返回：`quota_display_type: "CNY"`, `quota_per_unit: 500000`
    - 用户实际额度字段 `quota`: `624,930,000`
    - 实际折算公式：`624,930,000 / 500,000 = 1,249.86 CNY`

---

## 二、 完整复盘：提问、修复与遇到新问题的全流程

### 【阶段一】初始问题：账号密码余额显示 0，访问令牌报错

- **用户提问/反馈**：
  > “用账号密码余额就是0，用token然后填了id也是报错”
  > “访问令牌可以吗”
- **现象分析**：
  1. 用户在前端添加实例类型为 `newapi_user`。
  2. 使用 Access Token 时，由于未携带 `New-Api-User: <user_id>` 请求头，New API 接口报 `401 Unauthorized, New-Api-User header not provided`。
  3. 使用账号密码时，连接测试虽然能通，但监控资产中余额始终显示为 `0.00`。
- **做出的第一次修复**：
  - 在 `web/src/pages/InstancesPage.tsx` 中增加 `user_id` 输入项及 JSON 凭据解析支持。
  - 在 `internal/connectors/auth_helpers.go` 中，给 `newAPIUserHeaders` 添加对 `New-Api-User` 头的注入支持，同时让账号密码登录优先提取返回的用户信息。

---

### 【阶段二】新问题出现：接口直接报 429 Too Many Requests

- **用户提问/反馈**：
  > “直接429”
- **现象分析**：
  - 在修复了凭据解析后，API Monitor 后台的扫描器（Scanner）和前端的测试连接开始频繁工作。
  - 每次扫描任何资产或测试连接时，后端都会向 `https://api.gemai.cc/api/user/login` 发起一次账号密码登录请求以获取 Session。
  - 短时间内发起了多次登录 POST 请求，直接触发了目标站点 New API 的登录防爆破限流规则，服务器返回 `HTTP 429 Too Many Requests`。
- **做出的第二次修复**：
  - 在 `internal/connectors/auth_helpers.go` 中引入了内存 Session 会话缓存（`sessionCache`，带 RWMutex，有效期 4 小时）：
    - 第一次登录成功后，将返回的 Session Cookie、Token、User ID 缓存起来。
    - 后续的所有定时巡检、资产探测全部直接复用缓存的 Cookie 与 Header，不再重复请求 `/api/user/login`，彻底避免触发 429 限流。
  - 在 `internal/connectors/newapi.go` 中新增 `getSiteStatus`，动态请求站点的 `/api/status` 获取配率 `quota_per_unit`（500,000）与货币 `CNY`，并用 `quota / scale` 计算实际余额。

---

### 【阶段三】新问题出现：429 解决了，但资产页面仍旧是 0，且货币为 `US$`

- **用户提问/反馈**：
  > “还是0”
- **现象分析**（结合用户提供的截图 `media_1790333197604.png`）：
  - 截图显示卡片信息：
    - `哈基米api`
    - `当前余额: US$0.00`
    - `额度: 0 / 0 额度`
    - `已消耗: US$0.00`
    - 下方 4 个观察资产（公告、倍率、模型、价格）均显示 `Waiting for first scan`（条目 0）。
  - **当时推断的可能原因**：
    - 实例保存时虽然入库了，但数据库里的 `monitor_targets` 目标仍然保留了最初探测失败时的旧数据（`balance_amount: 0, balance_currency: "USD"`）。
    - 系统的自动巡检周期未到，或者用户保存实例后没有自动触发该实例的重新扫描。
- **做出的第三次修复**：
  - 在 `internal/api/api.go` 的 `upsertInstance` 中：保存实例成功后，立即启动后台 Goroutine 执行 `s.scanner.DiscoverInstance` 及 `s.scanner.ScanTarget`，强制同步最新余额。
  - 在 `web/src/pages/AssetsPage.tsx` 中：在资产组卡片右上角增加了一个显式的【同步资产】按钮，方便用户手动一键重新探测。
  - 前端执行 `npm run build` 验证通过，提交 Commit `ee7acaf` 并推送至 GitHub `origin/main`。

---

### 【阶段四】核心困境：等待了 10 多分钟、同步了多次，依然是 0

- **用户提问/反馈**：
  > “一样的还是0，等了10几分钟了，也同步了好多次”
- **困惑点**：
  - 代码在本地无论怎么看都是完全正确的：
    - Session 缓存逻辑写了；
    - `/api/status` 换算逻辑写了；
    - 汇率货币 `CNY` 解析写了；
    - 自动扫描与手动同步按钮写了。
  - **为什么无论怎么刷新、等待、同步，线上依然是 `US$0.00`（而不是 `CNY` 或 `¥`）？**

---

### 【阶段五】终极真相大白：通过线上接口诊断，揪出最致命的底层根因

为了查明为什么线上的表现和本地代码完全对不上，我们用命令行直接请求了 Render 线上服务的版本诊断接口：

```bash
curl -s "https://my-api-monitor.onrender.com/api/v1/version"
```

返回的 JSON 如下：
```json
{
  "version": "1.2.2",
  "commit": "c8731a355acd19fdbba1b51484c6d1a2fa832b30",
  "date": "2026-06-25T02:08:17Z",
  "repository": "baogutang/api-monitor",
  "updateAvailable": false,
  "selfUpdateEnabled": false
}
```

#### 🚨 致命根因揭晓：`Dockerfile` 根本没有编译 Go 后端！

我们去检查仓库根目录的 `Dockerfile`，赫然发现了这一段内容：

```dockerfile
FROM node:22-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM ghcr.io/baogutang/api-monitor:latest
USER root
COPY --from=web-build /src/web/dist /app/web/dist
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
USER app

ENTRYPOINT ["/entrypoint.sh"]
```

#### 💥 为什么会造成全部问题？
1. **Render 上的 Docker 构建过程**：
   - 阶段 1：用 Node.js 构建了前端 React 静态页面 (`/src/web/dist`)。
   - 阶段 2：直接拉取了原作者 2026 年 6 月发布的公共基础镜像 `ghcr.io/baogutang/api-monitor:latest`。
   - 阶段 3：只把前端 `web/dist` 拷进镜像，然后启动。
2. **完全缺失了 Go 编译环节**！
   - 本项目在 `internal/connectors/newapi.go`、`internal/connectors/auth_helpers.go`、`internal/api/api.go` 里写的所有 Go 代码修复，**Render 在构建时压根一行都没有编译**！
   - 线上容器里运行的二进制文件，一直都是原作者 2026 年 6 月打包的旧版程序（Commit: `c8731a355acd`）。
3. **这就完美解释了之前遇到的所有诡异现象**：
   - 为什么一直报 429？—— 因为旧镜像里没有 Session 缓存，每次扫描都重复去调登录接口。
   - 为什么余额始终是 0？—— 因为旧镜像根本没有 New API 额度（`quota / 500000`）的折算逻辑。
   - 为什么币种始终是 `US$`？—— 因为旧镜像把货币硬编码成了 `USD`，而动态获取 `/api/status` 的 `getSiteStatus` 代码在线上根本不存在！
   - 为什么同步了多次毫无变化？—— 因为旧二进制执行逻辑永远和旧版一样！

---

## 三、 本地已经编写并就绪的代码清单

本仓库当前的代码（Git Commit: `ee7acaf`）已经具备了所有修复逻辑，只需要正确编译即可：

1. **`internal/connectors/auth_helpers.go`**:
   - `sessionCache` 内存缓存已就绪，TTL 4 小时，加读写锁，拦截重复登录防 429。
   - 自动传递 `New-Api-User: <user_id>` 与 Cookie。
2. **`internal/connectors/newapi.go`**:
   - `getSiteStatus` 已就绪，自动探测 `/api/status` 中的 `quota_per_unit` 与 `quota_display_type`。
   - `newAPIBalanceWithStatus` 已就绪，优先采用 `quota / scale` 计算实际金额，并将货币设定为 `CNY`。
3. **`internal/api/api.go`**:
   - 实例创建/修改后，异步 Goroutine 自动执行 `DiscoverInstance` 和 `ScanTarget`。
4. **`web/src/pages/AssetsPage.tsx`**:
   - 【同步资产】按钮已加入卡片，且前端 `npm run build` 已实测通过（0 错误）。
5. **`entrypoint.sh`**:
   - 已将 Windows CRLF 换行符转换为 Linux 标准 LF 换行符。

---

## 四、 Codex 接手需要执行的具体修复步骤

### 步骤 1：重构 `Dockerfile`，恢复 Go 完整多阶段编译

将根目录下的 `Dockerfile` 修改为以下内容：

```dockerfile
# -------------------------------------------------------------
# 阶段 1：构建前端 React 静态资源
# -------------------------------------------------------------
FROM node:22-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# -------------------------------------------------------------
# 阶段 2：编译 Go 后端二进制
# -------------------------------------------------------------
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
# 使用官方代理，确保海外/云端构建通畅
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
RUN go mod download

COPY . .
# 拷贝前端产物供嵌入或校验
COPY --from=web-build /src/web/dist ./web/dist

ARG VERSION=1.2.3
ARG COMMIT=unknown
ARG DATE=unknown

# 静态交叉编译 Linux 二进制
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X api-monitor/internal/version.Version=${VERSION} -X api-monitor/internal/version.Commit=${COMMIT} -X api-monitor/internal/version.Date=${DATE}" \
    -o /out/api-monitor ./cmd/api-monitor

# -------------------------------------------------------------
# 阶段 3：轻量运行容器 (Alpine)
# -------------------------------------------------------------
FROM alpine:3.20

# 安装 CA 根证书（调用 HTTPS 上游接口必须）
RUN apk add --no-cache ca-certificates tzdata

RUN addgroup -S app && adduser -S app -G app
WORKDIR /app

# 从构建阶段复制编译好的二进制文件与必要资源
COPY --from=build /out/api-monitor /usr/local/bin/api-monitor
COPY migrations ./migrations
COPY --from=web-build /src/web/dist ./web/dist
COPY entrypoint.sh /entrypoint.sh

# 确保脚本换行符与执行权限
RUN sed -i 's/\r$//' /entrypoint.sh && chmod +x /entrypoint.sh /usr/local/bin/api-monitor

USER app
EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
```

---

### 步骤 2：提交代码并推送到 GitHub 触发 Render 构建

在项目根目录下执行：

```bash
git add Dockerfile entrypoint.sh
git commit -m "fix(docker): restore multi-stage go build to compile custom backend on render"
git push https://<GITHUB_PERSONAL_ACCESS_TOKEN>@github.com/zhengweihao123/api-monitor-2.git main
```

---

### 步骤 3：验证线上部署与最终功能验收

1. **验证 Render 已经运行最新二进制**：
   访问 `https://my-api-monitor.onrender.com/api/v1/version`，检查返回内容中的 `version` 和 `date`，确认不再是旧的 `c8731a355acd`。
2. **验证哈基米 API 资产同步**：
   - 打开 `https://my-api-monitor.onrender.com/assets` 页面。
   - 对 `哈基米api` 卡片点击【同步资产】（或重新保存一次实例）。
   - **验收标准**：
     - 当前余额正确显示：`¥1,249.86`（或 `1249.86 CNY`）。
     - 不再报 429 限流错误。
     - 4 个观察资产（公告、倍率、模型、价格）成功拉取到实时条目，不再显示 `Waiting for first scan`。
