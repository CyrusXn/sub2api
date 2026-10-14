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

需要重试成功发布后的资产轮转，可执行 `sub2api-release cleanup-release`。此操作必须有成功发布记录，并重新验证当前线上镜像和版本；不能用它强行清理一次失败发布留下的回滚资产。

`sub2api-release rollback` 沿用现有蓝绿机制，将新请求切回上一次健康 API 槽位，不执行数据库降级。涉及后台节点、配置或数据库的完整恢复，应按保留的完整备份单独处理。

## 本机验证

```bash
bash -n scripts/release-console/server-release.sh scripts/release-console/remote-bluegreen.sh scripts/release-console/release-console.sh
python3 scripts/release-console/tests/server-release.test.py
node --test scripts/release-console/tests/*.test.mjs
git diff --check
```

安全测试使用临时目录和命令替身，覆盖失败时不切流或不清理、最新备份损坏时拒绝删除、保留候选与被引用镜像、构建缓存清理范围。测试不会连接生产服务器。
