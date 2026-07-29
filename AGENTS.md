# Sub2API 定制版开发与部署规范

## 适用范围

本文件适用于本仓库全部开发、同步、验证和生产部署操作。任何子目录没有更具体的 `AGENTS.md` 时，必须遵循本文件。

## 基线与版本

- 官方仓库：`origin/main`，开始发布前必须先执行 `git fetch origin main` 并确认最新提交。
- 定制版必须基于官方最新 `main` 合并开发，禁止用官方镜像或在线更新直接覆盖定制功能。
- 开始构建前再次执行 `git fetch origin main`，记录本次发布冻结的官方提交。若同一天因官方 `main` 前进而重建，镜像标签和归档名追加 `main<官方短哈希>`，禁止覆盖已部署候选及其归档。
- 官方比较版本保持纯语义版本，例如 `0.1.163`；定制标识单独使用 `xnkaixin.YYYYMMDD`；构建类型固定为 `custom`。
- `custom` 构建只允许查看官方发布说明，禁止在线更新和在线回退。更新必须重新同步官方源码、合并定制改动并按本文重新部署。

## 工作区保护

- 当前工作区可能包含用户未提交改动。开始前检查 `git status --short --branch`，不得执行 `git reset --hard`、`git checkout --` 或清理用户文件。
- 发布工作优先使用仓库旁的持久 Git worktree，不使用可能被系统清理的 `/tmp` 或 `/private/tmp`。
- 只修改当前任务需要的文件，不顺手重构，不覆盖用户最新改动。
- 代码、日志和注释默认使用中文；不得记录 API Key、Token、Cookie、密码、请求头或未脱敏上游响应。

## 已有定制功能

- 用户端 `/ai-image` AI 生图工作台：固定 `gpt-image-2`，支持档位、比例、张数、自定义 Prompt、提示词模板、参考图、并发队列、预览、下载和缓存折叠。
- 用户端菜单、后台账号列表、分组徽标、DataTable 和相关响应式样式定制。
- 运维监控账号请求异常告警：识别余额/额度不足、全部账号不可用、部分账号故障及网络问题，并记录具体账号、平台、分组、阶段和状态码。
- 中文精准告警邮件、脱敏错误详情、邮件投递记录、夜间静默及北京时间 08:00 汇总。
- 每日低余额提醒，仅面向符合真实付费使用条件的低余额用户，并使用分布式锁和发送记录避免重复。
- 数据库迁移 `186`、`187`、`188` 分别对应账号请求告警、账号明细和告警邮件记录。
- 定制版本标识和在线更新保护。

## 开发与验证

- 优先沿用现有 Vue、TypeScript、Go、Wire、Repository 和 Service 模式，遵循 KISS、YAGNI、SOLID。
- 新增数据库结构必须使用递增迁移，不手工修改生产表结构。
- Go 修改后运行 `gofmt`；前端修改后至少运行 `pnpm typecheck`；发布前必须完成相关 Go 测试、前端构建和完整 Docker 镜像构建。
- Wire 依赖发生变化时同步检查 `backend/cmd/server/wire.go`、`wire_gen.go` 和 ProviderSet，确保生成文件与源码一致。
- 静态检查必须包含 `git diff --check`，并检查重复路由、重复类型、重复 i18n 键和凭据字面量。

## 本地启动与线上数据库

- 每次启动 Sub2API 本地项目供用户验收时，数据库连接默认指向线上 PostgreSQL；除非用户明确要求，不得擅自改用本地数据库、测试数据库或数据副本。
- 本地连接线上数据库必须以不影响线上业务为前提。启动前必须禁用自动迁移、定时任务、邮件通知、后台调度及其他可能产生线上写入或外部副作用的任务。
- 当前完整后端会自动执行迁移并启动多项后台服务，因此默认使用“本地构建页面 + 线上现有 API 代理”访问线上数据；在没有统一验收模式关闭全部副作用前，禁止启动第二个完整后端直接连接线上 PostgreSQL。
- 未经用户单独明确授权，不得通过本地环境修改线上业务数据、执行写入型接口、运行迁移或进行破坏性测试。若当前启动方式无法保证这些边界，必须停止启动并向用户说明风险。
- 数据库连接凭据只能复用现有安全配置，不得输出到终端、日志、文档、截图或提交记录。

## 本地构建

生产镜像只能在本地构建，服务器禁止执行源码构建或 Docker build。目标平台固定为 `linux/amd64`：

```bash
docker buildx build \
  --builder colima \
  --platform linux/amd64 \
  --load \
  --build-arg VERSION=0.1.163 \
  --build-arg BUILD_TYPE=custom \
  --build-arg EDITION=xnkaixin.YYYYMMDD \
  -t weishaw/sub2api:xnkaixin-0.1.163-YYYYMMDD-full \
  .
```

构建后必须先在本地启动候选镜像，验证 `/health`、版本信息、登录页、`/ai-image` 和运维监控页面。然后导出并生成校验值：

```bash
docker save weishaw/sub2api:xnkaixin-0.1.163-YYYYMMDD-full | gzip > sub2api-xnkaixin-0.1.163-YYYYMMDD-full.tar.gz
shasum -a 256 sub2api-xnkaixin-0.1.163-YYYYMMDD-full.tar.gz > sub2api-xnkaixin-0.1.163-YYYYMMDD-full.tar.gz.sha256
```

## 生产环境

- SSH：`root@64.83.14.17`
- Compose 目录：`/home/docker/sub2api`
- 应用服务和容器：`sub2api`
- PostgreSQL 容器：`sub2api-postgres`
- Redis 容器：`sub2api-redis`
- Nginx 容器：`nginx`
- 外部健康检查：`https://api.xnkaixin.eu.cc/health`

部署前必须只读检查当前容器、镜像、Compose 配置、磁盘和健康状态，并完成以下备份：

- PostgreSQL 一致性备份。
- `docker-compose.yml`、环境文件和当前镜像 ID/标签快照。
- 为当前镜像创建明确的回滚标签。

上传本地镜像归档和 SHA256 文件，在服务器校验后执行 `docker load`。服务器只允许导入镜像和切换应用容器，不得构建镜像。

## 无中断切换

- 只重建 `sub2api`，不得重启 PostgreSQL、Redis、Nginx 或整个 Compose 项目。
- 更新 Compose 中应用镜像标签后，固定使用：

```bash
docker compose up -d \
  --no-deps \
  --force-recreate \
  --pull never \
  sub2api
```

- 切换后持续检查容器 health、restart count、应用日志和外部 HTTPS 健康接口。
- 确认迁移 `186`、`187`、`188` 已成功应用，且数据库、Redis、Nginx 容器未重启。
- 验证版本展示、`/ai-image`、运维告警规则、账号明细和邮件记录接口。

## 回滚

- 任一关键检查失败，立即将 Compose 镜像恢复为部署前回滚标签，并用同一条 `--no-deps --force-recreate --pull never` 命令只重建 `sub2api`。
- 迁移 `186` 至 `188` 为向后兼容的新增表/规则，回滚应用时不主动删除表和数据。
- 回滚后再次验证外部 `/health`、容器 health、restart count 和核心 API。

## 禁止事项

- 禁止在生产服务器 `git pull`、编译前端、编译 Go 或执行 `docker build`。
- 禁止使用 `docker compose down` 或重启全部服务完成常规发布。
- 禁止直接点击官方在线更新覆盖 `custom` 构建。
- 禁止在命令输出、提交记录、文档或告警中写入真实凭据和用户敏感信息。
