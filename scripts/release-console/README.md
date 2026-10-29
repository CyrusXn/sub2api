# Sub2API 服务器发布

生产构建已迁到服务器。本机不再启动 Docker、编译前后端或导出镜像包；开发与官方代码合并仍在唯一主项目完成，验收并推送定制提交后才发布。

## 日常使用

登录已核验主机身份的生产服务器后执行：

```bash
sub2api-release status
sub2api-release build <完整的40位定制提交SHA>
sub2api-release deploy <完整的40位定制提交SHA>
```

`build` 只构建并验证二进制，不启动连接生产数据库的候选、不切流。`deploy` 包含构建、备份、预热、版本验证、蓝绿切流和自动清理；同一提交已经由健康主节点与活跃槽位运行时，不重复部署或覆盖上一版本备份。

源码固定从 `https://github.com/CyrusXn/sub2api.git` 的 `xnkaixin` 分支获取。必须指定已审核提交，脚本验证其属于定制分支，不会自动合并官方更新或直接发布未审核的最新分支头。相同提交的服务器镜像经过再次验证后复用，不覆盖原标签。

如仍希望从 Mac 触发，保留了可选入口：

```bash
bash scripts/release-console/release-console.sh status
bash scripts/release-console/release-console.sh deploy
```

这个入口检查工作区干净，发送当前完整提交 SHA；复用本机受保护的服务器配置，以固定主机指纹验证 SSH。Mac 上不执行构建。服务器直接运行发布命令不依赖 Mac 开机。

## 资源与目录

- 发布工具：`/home/docker/sub2api/.server-release/bin/`
- 统一入口：`/usr/local/bin/sub2api-release`
- 裸源码仓库：`/home/docker/sub2api/.server-release/repo.git`
- 构建临时目录：`/home/docker/sub2api/.server-release/source-<SHA>`
- 构建器：`sub2api-release`，独立 Docker BuildKit 容器，限制 2 核 CPU、3 GiB 内存、禁止额外交换空间、编译步骤并发数 1。
- 构建入口从原 Dockerfile 生成仅供此次构建使用的副本，给 Go 编译增加 `GOFLAGS=-p=1`、`GOMAXPROCS=2`、`GOMEMLIMIT=1800MiB`、`GOGC=30`，降低编译峰值内存；业务源码保持指定提交的原样。原 Dockerfile 的对应结构变化时停止并要求审核适配。
- 开始构建至少需要 20 GiB 磁盘和 4.5 GiB 可用内存，否则停止，不挤占线上服务资源。
- 发布与清理使用 `/home/docker/sub2api/.server-release/release.lock` 排他锁。

前端使用 `pnpm run build`：先检查 i18n 和 `vue-tsc -b`，通过后才执行 Vite。Vite checker 的 `enableBuild: false` 只取消打包阶段的重复类型检查，开发检查仍开启；不能用裸 `vite build` 跳过前置检查。

当前脚本只在构建开始前检查资源，并限制专用构建器的 CPU/内存；**尚无持续内存监控或自动熔断**。执行人须按项目 `AGENTS.md` 的“构建内存保护与超时处置”每 5 秒持续监看宿主机余量、构建器 cgroup 事件及线上健康，达到中止条件时只取消已确认的构建任务，保护现有流量。不能持续监看时，不在生产主机开始构建；无人值守发布须先实现并验证该保护。同机限额不等于磁盘 I/O 等资源的完全隔离。

首次安装时，将本目录 `server-release.sh` 与 `remote-bluegreen.sh` 安装到服务器的工具目录，权限设为 `0700`；入口脚本内容如下。必须传入脚本真实路径，不能用会改变脚本目录解析的符号链接。

```bash
#!/usr/bin/env bash
exec bash /home/docker/sub2api/.server-release/bin/server-release.sh "$@"
```

## 发布与清理边界

1. 从指定提交导出干净源码，在服务器构建 `linux/amd64` 定制镜像，注入版本、完整提交和定制标识。
2. 用禁用网络的 `-version` 运行验证候选二进制，不触发服务启动或数据库迁移。
3. 创建 PostgreSQL、Compose、环境、Nginx 与活跃上游快照及回滚标签，并核验 SHA256。
4. 只替换不承接流量且连接已排空的 API 槽位，验证候选健康及公开版本，更新唯一后台主节点并平滑切流。
5. 核验活跃槽位、后台节点、外部 HTTPS 的版本及健康，并确认 Nginx、PostgreSQL、Redis 的容器身份、启动时间和重启次数未改变。
6. 写入成功记录后，保留当前版本、上一版本完整回滚备份、待发布候选及任意容器引用的镜像。删除上上版本及更早的完整备份和无引用旧应用镜像。
7. 清空 **Sub2API 专用** BuildKit 缓存、删除临时源码、停止构建器。下一次发布重新下载构建依赖，这是避免持续积累的取舍。

最新备份损坏或缺失、回滚镜像与上一版本不匹配、健康检查失败时，禁止轮转备份。清理失败使发布返回失败，不能宣称完整发布流程完成。数据库卷、其他项目镜像、其他项目的构建缓存不在清理范围内。

切流或最终验收失败时，自动将 API 流量切回发布前的健康槽位，保留全部回滚资产；这不等于数据库或后台节点的完整降级。中断后以同一提交重试 `deploy`，会补做验收和清理，不重新备份覆盖上一版本。

失败后保留源码和候选供排查；只清理构建缓存与临时源码可执行：

```bash
sub2api-release cleanup-build
```

此命令用于任务结束后的清理，不是运行中构建的紧急中止命令。先保留脱敏诊断、确认构建/发布已结束且无并发任务，再执行；不删除回滚备份或发布状态文件。内存告急与重试的阈值、阶段边界及恢复门槛见项目 `AGENTS.md`。

SSH 超时不代表远端任务停止：核验主机身份后，先只读检查锁及持锁进程、发布进程、`candidate`、`pending-candidate`、`pending-commit`、`last-success`、活跃槽位、镜像提交和健康。未确认阶段前不得重跑 `deploy`、执行清理或停止构建器；公网 200 不能证明切流结果。确认无并发任务及恢复阶段后，才以原审核 SHA 继续；不要删状态文件绕过恢复逻辑。

需要重试成功发布后的资产轮转，可执行 `sub2api-release cleanup-release`。此操作必须有成功发布记录，并重新验证当前线上镜像和版本；不能用它强行清理一次失败发布留下的回滚资产。

`sub2api-release rollback` 沿用现有蓝绿机制，将新请求切回上一次健康 API 槽位，不执行数据库降级。涉及后台节点、配置或数据库的完整恢复，应按保留的完整备份单独处理。

## 独立后端验收（不切正式流量）

`isolated-preview.py` 只运行由上述 `build <SHA>` 生成并校验的镜像，不构建镜像，也不执行 `deploy`、蓝绿预热或 Nginx 修改。代码仍须在唯一主目录完成审核、提交、推送，工作区干净；未明确纳入的其他会话改动不得擅自提交。构建阶段仍须按 AGENTS 每 5 秒持续监看并遵循中止门槛，本运行器的运行期保护不替代构建监控。

隔离验收可使用同目录 `guarded-build.py <完整SHA>` 监管 `sub2api-release build`。先采样 60 秒生产健康、内存和 Nginx 5xx 基线，再每 5 秒记录阶段、宿主内存、构建器 cgroup 及事件增量、健康、5xx。达到项目中止门槛时只取消自己的 build 进程组；确认它正在使用专用构建器且容器 ID 未变时才停止该构建器。访问日志轮转或监控失败也取消本次构建，保留诊断。该包装器没有 deploy 入口，不改变现有正式发布命令；建议在受保护的 systemd 临时服务中运行，避免 SSH 断开后失去监管。

候选验证完成、保存诊断并确认没有发布任务后，执行 `sub2api-release cleanup-build` 释放专用构建器。将已审核的运行器安装到服务器工具目录（权限 `0700`），在已核验 SSH 身份的受保护终端运行：

```bash
python3 /home/docker/sub2api/.server-release/bin/isolated-preview.py <完整SHA> <服务器候选镜像标签>
```

首次运行通过隐藏输入设置测试管理员 `admin@example.com` 的初始密码，不把密码写进命令行。程序必须保持运行；需要长时间验收时使用服务器受保护的终端会话管理工具，不能裸后台运行后失去监管。Ctrl+C、SIGTERM、启动失败、测试健康失败、生产容器变化、生产健康连续三次失败、磁盘不足 20 GiB、内存低于 1 GiB 一次或低于 1.5 GiB 持续 15 秒时，只停止带隔离标签及 Compose 项目标识的测试容器，保留数据卷和日志。断电、SIGKILL 或守护进程自身崩溃不能保证执行退出清理，应先只读核实状态再恢复，不能据此承诺完全隔离。

隔离配置固定如下，不能用生产 `.env` 替代：

- 项目 `sub2api-preview`，服务端目录 `/home/docker/sub2api-preview`（`0700`），配置和随机密钥文件 `0600`。
- 端口仅 `127.0.0.1:18090`；数据库、Redis 不发布端口，使用独立内部网络和独立数据卷，无生产卷/网络挂载。
- 全新测试数据库 `sub2api_preview`、独立 JWT/TOTP 密钥、空业务设置；不复制生产用户、API Key、账单、邮件或支付配置。
- 应用 `api_only`、禁用 Ops 后台监控，仅通过 `OPS_REQUEST_ERRORS_ENABLED=true` 开启请求错误记录与查询，不触发告警通知；每个容器最多 0.5 核，内存分别为应用 768 MiB / PostgreSQL 512 MiB / Redis 128 MiB，禁止额外 swap，日志每容器最多 10 MiB。不自动重启；运行期每 5 秒采样资源和健康。
- 运行器持有与发布相同的排他锁。验收期间禁止并发发布；完成验收后先退出运行器释放锁。

通过 SSH 隧道把本机 `18090` 转发至服务器 `127.0.0.1:18090`，使用独立浏览器入口访问 `http://127.0.0.1:18090`。前后端均来自同一个候选镜像，不再使用“新页面代理旧 API”的方式验证后端改动。不要将测试端口加入生产 Nginx。

首次启动后只导入验收需要的账号、分组及代理配置；生产读取须只读，禁止恢复全库备份或复制全局设置。真实上游调用仍可能计费；测试使用记录、账号健康状态、余额扣减只写测试库，不会同步回生产。验收应同时核对候选提交、占位首包与真实内容双计时、账号测试终态、组内故障切号及身份回复空值透传，并对比生产容器身份/启动时间/重启次数和健康基线。共享主机仍共享磁盘 I/O 等资源，不保证绝对零影响。

首次启动前，将审核过的上游公开域名列表保存为测试目录内 `upstream-hosts.json`（JSON 字符串数组，权限 `0600`），默认仅允许 `api.openai.com`。运行器开启 URL allowlist 和私网地址限制；不要使用通配符。此检查不代替代理、DNS 和网络通路核验。

## 工具测试

```bash
bash -n scripts/release-console/server-release.sh scripts/release-console/remote-bluegreen.sh scripts/release-console/release-console.sh
python3 scripts/release-console/tests/server-release.test.py
python3 scripts/release-console/tests/isolated-preview.test.py
python3 scripts/release-console/tests/guarded-build.test.py
node --test scripts/release-console/tests/*.test.mjs
git diff --check
```

安全测试使用临时目录和命令替身，覆盖失败时不切流或不清理、最新备份损坏时拒绝删除、保留候选与被引用镜像、构建缓存清理范围。测试不会连接生产服务器。
