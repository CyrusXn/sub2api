# 余额中心告警邮件动态文案实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 使用真实站点、账号、倍率、余额和阈值生成简洁的倍率变化及低余额邮件。

**架构：** 在现有 `BalanceCenterSnapshot` 中增加仅供运行时邮件生成使用的账号名，并由上游探测构建器从真实 `Account.Name` 传入。保留现有告警决策和队列流程，仅替换 `buildBalanceCenterAlertEmail` 的文案生成逻辑，通过新旧倍率比较动态选择上涨或下降。

**技术栈：** Go、`testify/require`、现有余额中心服务与上游倍率探测代码。

---

## 文件结构

- 修改：`backend/internal/service/balance_center.go`：承载快照账号名、账号标签清理和两类邮件文案生成。
- 修改：`backend/internal/service/upstream_billing_probe.go`：把真实 `Account.Name` 传入余额中心快照。
- 修改：`backend/internal/service/balance_center_test.go`：验证上涨、下降和低余额动态邮件文案。
- 修改：`backend/internal/service/upstream_billing_probe_test.go`：验证真实账号名进入快照。

### 任务 1：先锁定动态邮件文案行为

**文件：**
- 测试：`backend/internal/service/balance_center_test.go`

- [ ] **步骤 1：编写失败的倍率上涨、下降和低余额测试**

新增直接调用邮件构建函数的表格测试，核心断言如下：

```go
accountID := int64(935)
snapshot := &BalanceCenterSnapshot{
    SiteName:         "派大星",
    AccountName:      "【派大星】heavy",
    AccountID:        &accountID,
    ConvertedBalance: testPtrFloat64(3.25),
}

subject, body := buildBalanceCenterAlertEmail(snapshot, BalanceCenterAlertDecision{
    Type: BalanceCenterAlertMultiplierChanged,
    OldValue: testPtrFloat64(0.01),
    NewValue: testPtrFloat64(0.02),
})
require.Equal(t, "派大星-heavy账号-倍率上涨", subject)
require.Equal(t, "派大星 heavy账号倍率上涨，0.01->0.02", body)
```

下降测试把旧值、新值改为 `0.02`、`0.01`，断言“倍率下降”。低余额测试传入阈值 `5`，断言主题为 `派大星-余额低于5元`、正文为 `派大星余额：3.25元`。

- [ ] **步骤 2：运行测试验证失败**

运行：

```bash
cd backend && go test ./internal/service -run 'TestBuildBalanceCenterAlertEmail' -count=1
```

预期：FAIL，原因是 `BalanceCenterSnapshot` 尚无 `AccountName`，且邮件主题、正文仍为旧格式。

### 任务 2：让真实账号名进入余额中心快照

**文件：**
- 修改：`backend/internal/service/upstream_billing_probe.go:2148-2171`
- 测试：`backend/internal/service/upstream_billing_probe_test.go:628-637`

- [ ] **步骤 1：编写失败的快照账号名测试**

扩展现有 `TestBuildBalanceCenterProbeSnapshotUsesBracketedSiteName`：

```go
require.Equal(t, "鱼鱼", result.SiteName)
require.Equal(t, "【鱼鱼】008 bugteam", result.AccountName)
```

- [ ] **步骤 2：运行测试验证失败**

运行：

```bash
cd backend && go test ./internal/service -run 'TestBuildBalanceCenterProbeSnapshotUsesBracketedSiteName' -count=1
```

预期：FAIL，原因是快照尚未保存真实账号名。

- [ ] **步骤 3：编写最少实现代码**

在 `BalanceCenterSnapshot` 增加运行时字段：

```go
AccountName string `json:"account_name,omitempty"`
```

在 `buildBalanceCenterProbeSnapshot` 构建结果时传入：

```go
AccountName: strings.TrimSpace(account.Name),
```

- [ ] **步骤 4：运行快照测试验证通过**

运行：

```bash
cd backend && go test ./internal/service -run 'TestBuildBalanceCenterProbeSnapshotUsesBracketedSiteName' -count=1
```

预期：PASS。

### 任务 3：生成简洁动态邮件

**文件：**
- 修改：`backend/internal/service/balance_center.go:287-311`

- [ ] **步骤 1：实现账号标签清理与方向判断**

账号标签优先使用真实账号名，并删除开头的站点前缀；清理后为空时回退账号 ID：

```go
func balanceCenterAlertAccountLabel(snapshot *BalanceCenterSnapshot) string {
    accountName := strings.TrimSpace(snapshot.AccountName)
    if siteName := strings.TrimSpace(snapshot.SiteName); siteName != "" {
        accountName = strings.TrimSpace(strings.TrimPrefix(accountName, "【"+siteName+"】"))
    }
    if accountName != "" {
        return accountName
    }
    return balanceCenterAccountLabel(snapshot.AccountID)
}
```

倍率变化时比较真实新旧倍率：新值小于旧值显示“下降”，其他已触发的变化显示“上涨”。

- [ ] **步骤 2：实现倍率变化邮件**

```go
subject := fmt.Sprintf("%s-%s账号-倍率%s", siteName, accountName, direction)
body := fmt.Sprintf("%s %s账号倍率%s，%s->%s", escapedSiteName, escapedAccountName, direction, value(decision.OldValue), value(decision.NewValue))
```

- [ ] **步骤 3：实现低余额邮件**

```go
subject := fmt.Sprintf("%s-余额低于%s元", siteName, value(decision.Threshold))
body := fmt.Sprintf("%s余额：%s元", escapedSiteName, value(snapshot.ConvertedBalance))
```

- [ ] **步骤 4：运行邮件测试验证通过**

运行：

```bash
cd backend && go test ./internal/service -run 'TestBuildBalanceCenterAlertEmail' -count=1
```

预期：PASS。

### 任务 4：回归验证并提交

**文件：**
- 修改：`backend/internal/service/balance_center.go`
- 修改：`backend/internal/service/upstream_billing_probe.go`
- 修改：`backend/internal/service/balance_center_test.go`
- 修改：`backend/internal/service/upstream_billing_probe_test.go`

- [ ] **步骤 1：格式化 Go 文件**

```bash
gofmt -w backend/internal/service/balance_center.go backend/internal/service/upstream_billing_probe.go backend/internal/service/balance_center_test.go backend/internal/service/upstream_billing_probe_test.go
```

- [ ] **步骤 2：运行余额中心相关测试**

```bash
cd backend && go test ./internal/service -run 'Test(BalanceCenter|BuildBalanceCenter)' -count=1
```

预期：PASS。

- [ ] **步骤 3：执行静态差异检查**

```bash
git diff --check
git diff -- backend/internal/service/balance_center.go backend/internal/service/upstream_billing_probe.go backend/internal/service/balance_center_test.go backend/internal/service/upstream_billing_probe_test.go
```

预期：无空白错误，差异仅包含本计划指定的动态邮件文案与测试。

- [ ] **步骤 4：提交实现**

```bash
git add backend/internal/service/balance_center.go backend/internal/service/upstream_billing_probe.go backend/internal/service/balance_center_test.go backend/internal/service/upstream_billing_probe_test.go
git commit -m "fix(邮件): 使用真实余额和倍率生成告警文案"
```
