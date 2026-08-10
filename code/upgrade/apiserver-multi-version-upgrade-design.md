# APIServer 跨多版本升级设计方案

> 基于 cluster-api-provider-bke 代码库分析

## 一、背景与目标

### 1.1 背景

当前 BKE 升级系统支持通过 UpgradePath CRD 定义升级路径，并通过 ClusterVersionReconciler 实现多跳升级（Multi-Hop Upgrade）。但在实际场景中，用户可能需要从较旧版本（如 v1.25）直接升级到较新版本（如 v1.29），这涉及跨多个小版本的升级。

### 1.2 目标

设计 APIServer 跨多版本升级方案，确保：
- 升级过程安全可控
- API 兼容性得到保证
- 数据格式变更正确处理
- 升级失败可回滚

## 二、当前实现分析

### 2.1 APIServer 升级流程

当前 APIServer 升级通过 `EnsureMasterUpgrade` Phase 实现（`pkg/phaseframe/phases/ensure_master_upgrade.go`）：

```
EnsureMasterUpgrade
├── 1. getNeedUpgradeNodes() — 获取需要升级的 Master 节点
├── 2. ensureEtcdAdvertiseClientUrlsAnnotation() — 确保 etcd 注解正确
├── 3. upgradeMasterNodesWithParams() — 逐节点滚动升级
│   └── 对每个节点:
│       ├── 检查 kubelet 版本是否已匹配
│       ├── 标记节点为 NodeUpgrading
│       ├── executeNodeUpgradeWithParams() — 创建 UpgradeControlPlane 命令
│       ├── waitForNodeHealthCheckWithParams() — 等待健康检查（2s间隔，5min超时）
│       └── 标记节点为 NodeNotReady (升级成功)
├── 4. updateAddonVersions() — 更新 kube-proxy/kubectl addon 版本
└── 5. 更新 BKECluster.Status.KubernetesVersion
```

### 2.2 多跳升级机制

```
ClusterVersionReconciler.reconcileUpgrade()
  │
  v
pathService.FindPath(current, desired) → [v1→v2, v2→v3, v3→v4]
  │
  v
hopTarget = path[0].To = v2  (只执行第一跳)
  │
  v
BKECluster 执行 v1→v2 升级
  │
  v
completeDeclarativeUpgrade()
  ├── cv.Status.CurrentVersion = v2
  ├── cv.Status.Phase = Upgrading (因为 v2 != v4)
  └── 删除 upgrade-ready 注解
        │
        v
触发下一跳: v2→v3 → v3→v4 → ... → Ready
```

### 2.3 当前实现的限制

| 限制 | 说明 |
|------|------|
| **逐跳升级** | 每次只升级一个小版本，跨多版本需要多次执行 |
| **无跨版本 PreCheck** | UpgradePath 定义了 PreCheck/PostCheck 但未实际执行 |
| **无 API 兼容性检查** | 未检查 deprecated API 的使用情况 |
| **无 etcd 数据格式迁移** | 未处理 etcd 数据格式在不同版本间的变化 |
| **证书不轮转** | 升级过程不处理证书轮转 |

## 三、跨多版本升级的挑战

### 3.1 API 兼容性

Kubernetes 保证相邻小版本之间的 API 向后兼容性，但跨多个版本时需要注意：

| 版本跨度 | API 变化 | 风险等级 |
|---------|---------|---------|
| 1 个小版本 (如 1.27→1.28) | 少量 deprecated API 移除 | 低 |
| 2 个小版本 (如 1.27→1.29) | 多个 deprecated API 移除 | 中 |
| 3+ 个小版本 (如 1.25→1.29) | 大量 deprecated API 移除，可能有 breaking changes | 高 |

**关键 deprecated API（示例）**：
- v1.25: 移除 PodSecurityPolicy
- v1.26: 移除 FlowControl beta1 API
- v1.27: 移除 CSIStorageCapacity beta API
- v1.28: 移除 CRD Validation Ratcheting alpha
- v1.29: 移除多个 AdmissionRegistration beta API

### 3.2 etcd 数据格式

etcd 在不同版本间可能使用不同的数据格式：

| etcd 版本 | 数据格式变化 | 迁移要求 |
|----------|------------|---------|
| 3.4→3.5 | 存储格式优化 | 需要数据迁移 |
| 3.5→3.6 | WAL 格式变化 | 需要数据迁移 |

### 3.3 证书兼容性

不同 Kubernetes 版本可能使用不同的证书格式或签名算法：

| 组件 | 证书类型 | 跨版本变化 |
|------|---------|-----------|
| API Server | Server/Client 证书 | 可能增加新的 SAN |
| etcd | Server/Peer 证书 | 格式基本稳定 |
| kubelet | Client 证书 | 格式基本稳定 |

### 3.4 组件版本倾斜

Kubernetes 组件版本倾斜策略：

```
API Server: v1.29
    │
    ├── kubelet: v1.28 ~ v1.29 (±1 小版本)
    ├── kube-proxy: v1.28 ~ v1.29 (±1 小版本)
    └── kubectl: v1.28 ~ v1.29 (±1 小版本)
```

## 四、设计方案

### 4.1 升级策略选择

#### 方案一：逐跳升级（当前实现）

```
v1.25 → v1.26 → v1.27 → v1.28 → v1.29
   │        │        │        │        │
   └────────┴────────┴────────┴────────┘
           每跳执行完整升级流程
```

**优点**：
- 每跳变更小，风险低
- 符合 Kubernetes 官方推荐
- 当前已实现

**缺点**：
- 升级时间长（每跳需要完整升级流程）
- 多次中断服务

#### 方案二：快速跨版本升级（新增）

```
v1.25 ──────────────────────────────→ v1.29
   │                                    │
   └────────────────────────────────────┘
           一次升级，跳过中间版本
```

**前提条件**：
- 所有中间版本的 deprecated API 已清理
- etcd 数据格式兼容或已迁移
- 证书格式兼容

### 4.2 跨版本 PreCheck 设计

#### 4.2.1 API 兼容性检查

```
APIServer 跨版本 PreCheck
├── 1. 扫描集群中使用的 API
│   └── 通过 API Server 审计日志或资源扫描
├── 2. 检查 deprecated API
│   └── 对比目标版本的 removed APIs 列表
├── 3. 生成兼容性报告
│   ├── 可安全移除的 API
│   ├── 需要迁移的 API
│   └── 阻塞升级的 API
└── 4. 决策
    ├── 无阻塞 → 继续升级
    └── 有阻塞 → 中止升级，提供迁移建议
```

**实现代码结构**：

```go
// pkg/upgrade/precheck/api_compatibility.go

type APIChecker struct {
    client        kubernetes.Interface
    targetVersion string
}

func (c *APIChecker) Check(ctx context.Context) (*CheckResult, error) {
    // 1. 获取目标版本移除的 API 列表
    removedAPIs := GetRemovedAPIs(c.targetVersion)

    // 2. 扫描集群中使用的 API
    usedAPIs, err := c.scanUsedAPIs(ctx)
    if err != nil {
        return nil, err
    }

    // 3. 检查兼容性
    var issues []Issue
    for _, api := range usedAPIs {
        if removedAPIs.Contains(api) {
            issues = append(issues, Issue{
                Type:     "DeprecatedAPI",
                API:      api,
                Severity: "Error",
                Message:  fmt.Sprintf("API %s removed in %s", api, c.targetVersion),
            })
        }
    }

    return &CheckResult{Issues: issues}, nil
}
```

#### 4.2.2 etcd 数据格式检查

```
etcd PreCheck
├── 1. 检查当前 etcd 版本
├── 2. 检查目标版本 etcd 版本
├── 3. 检查数据格式兼容性
│   ├── 如果兼容 → 继续
│   └── 如果不兼容 → 执行数据迁移
└── 4. 备份 etcd 数据
```

#### 4.2.3 证书兼容性检查

```
证书 PreCheck
├── 1. 检查当前证书格式
├── 2. 检查目标版本证书要求
├── 3. 检查证书有效期
│   ├── 有效期 < 30天 → 建议先轮转证书
│   └── 有效期 >= 30天 → 继续
└── 4. 检查 SAN 配置
```

### 4.3 升级流程设计

#### 4.3.1 快速跨版本升级流程

```
用户请求: v1.25 → v1.29
  │
  v
1. PreCheck 阶段
  ├── API 兼容性检查
  ├── etcd 数据格式检查
  ├── 证书兼容性检查
  └── 资源充足性检查
  │
  v
2. 备份阶段
  ├── etcd 快照备份
  ├── 配置备份
  └── 证书备份
  │
  v
3. 升级阶段
  ├── 3.1 etcd 升级（如需要）
  │   ├── 逐节点升级 etcd
  │   └── 验证 etcd 集群健康
  ├── 3.2 API Server 升级
  │   ├── 逐 Master 节点升级
  │   ├── 更新静态 Pod manifest
  │   └── 验证 API Server 健康
  ├── 3.3 Controller Manager 升级
  ├── 3.4 Scheduler 升级
  └── 3.5 Worker 节点升级
      ├── 逐节点升级 kubelet
      └── 验证节点健康
  │
  v
4. PostCheck 阶段
  ├── 验证所有组件版本
  ├── 验证集群健康
  ├── 验证 deprecated API 已清理
  └── 验证业务应用正常
  │
  v
5. 完成
```

#### 4.3.2 升级状态机

```
                    ┌─────────────┐
                    │   Ready     │
                    └──────┬──────┘
                           │ 用户请求升级
                           v
                    ┌─────────────┐
                    │ PreChecking │
                    └──────┬──────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              v            v            v
        ┌─────────┐  ┌─────────┐  ┌─────────┐
        │ 通过    │  │ 警告    │  │ 失败    │
        └────┬────┘  └────┬────┘  └────┬────┘
             │            │            │
             v            v            v
        ┌─────────┐  ┌─────────┐  ┌─────────┐
        │Upgrading│  │ 用户确认│  │ Failed  │
        └────┬────┘  └────┬────┘  └─────────┘
             │            │
             v            v
        ┌─────────┐  ┌─────────┐
        │PostCheck│  │Upgrading│
        └────┬────┘  └─────────┘
             │
        ┌────┴────┐
        │         │
        v         v
   ┌────────┐ ┌────────┐
   │ 通过   │ │ 失败   │
   └───┬────┘ └───┬────┘
       │          │
       v          v
   ┌────────┐ ┌────────┐
   │ Ready  │ │ Failed │
   └────────┘ └────────┘
```

### 4.4 回滚设计

#### 4.4.1 回滚触发条件

| 条件 | 触发方式 |
|------|---------|
| PreCheck 失败 | 自动中止 |
| 升级过程中组件失败 | 自动回滚或人工确认 |
| PostCheck 失败 | 人工确认回滚 |
| 业务应用异常 | 人工确认回滚 |

#### 4.4.2 回滚流程

```
回滚触发
  │
  v
1. 停止升级流程
  │
  v
2. 评估回滚范围
  ├── 已升级的组件
  ├── 未升级的组件
  └── 数据格式变化
  │
  v
3. 执行回滚
  ├── 3.1 恢复 etcd 数据（如需要）
  ├── 3.2 降级 API Server
  ├── 3.3 降级 Controller Manager
  ├── 3.4 降级 Scheduler
  └── 3.5 降级 Worker 节点
  │
  v
4. 验证回滚结果
  ├── 验证组件版本
  ├── 验证集群健康
  └── 验证业务应用
  │
  v
5. 完成回滚
```

## 五、实现计划

### 5.1 阶段一：PreCheck 框架（2 人月）

| 任务 | 工作量 | 说明 |
|------|--------|------|
| PreCheck 框架设计 | 0.5 | 定义 PreCheck 接口和执行流程 |
| API 兼容性检查 | 0.5 | 扫描 deprecated API |
| etcd 数据格式检查 | 0.3 | 检查 etcd 版本兼容性 |
| 证书兼容性检查 | 0.3 | 检查证书格式和有效期 |
| 测试与文档 | 0.4 | 单元测试、集成测试、文档 |

### 5.2 阶段二：快速跨版本升级（3 人月）

| 任务 | 工作量 | 说明 |
|------|--------|------|
| 升级流程改造 | 1.0 | 支持跳过中间版本 |
| etcd 数据迁移 | 0.8 | 处理 etcd 数据格式变化 |
| 回滚机制实现 | 0.8 | 实现自动/手动回滚 |
| 测试与文档 | 0.4 | 端到端测试、文档 |

### 5.3 阶段三：增强功能（1 人月）

| 任务 | 工作量 | 说明 |
|------|--------|------|
| 升级进度可视化 | 0.3 | 升级进度展示 |
| 升级通知机制 | 0.3 | 升级前/后通知 |
| 升级报告生成 | 0.2 | 生成升级报告 |
| 测试与文档 | 0.2 | 补充测试和文档 |

## 六、风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| **API 不兼容** | 业务应用异常 | PreCheck 扫描 deprecated API |
| **etcd 数据丢失** | 集群状态丢失 | 升级前备份 etcd |
| **升级失败无法回滚** | 集群不可用 | 实现回滚机制 |
| **证书过期** | 组件通信失败 | PreCheck 检查证书有效期 |
| **升级时间过长** | 业务中断时间长 | 优化升级流程，支持并行升级 |

## 七、总结

APIServer 跨多版本升级是一个复杂的工程，需要综合考虑 API 兼容性、数据格式变化、证书兼容性等多个因素。当前 BKE 已实现逐跳升级机制，可以作为基础方案。对于快速跨版本升级需求，需要增加 PreCheck 框架、数据迁移机制和回滚机制。

**推荐策略**：
1. **生产环境**：优先使用逐跳升级，风险最低
2. **测试环境**：可尝试快速跨版本升级，提高效率
3. **关键业务集群**：无论哪种方式，都必须先备份 etcd

---

**文档版本**：v1.0
**创建日期**：2026-08-10
**基于代码版本**：cluster-api-provider-bke main 分支
