# DeclarativeUpgradeCatalog 代码走读

## 文件位置

`pkg/upgrade/catalog.go`

## 核心作用

`DeclarativeUpgradeCatalog` 是声明式升级 DAG 的**组件目录表**，定义了 ReleaseImage 升级时所有组件的执行规格。

其核心作用是：**将旧版 PhaseFlow 硬编码的升级 Phase 映射为声明式 DAG 可消费的组件清单**。

## 数据结构

### UpgradeComponentSpec

每条 `UpgradeComponentSpec` 定义一个升级组件的执行规格：

| 字段 | 含义 | 示例 |
|------|------|------|
| `Name` | DAG 节点名 / VersionContext key | `etcd`, `bkeagent`, `coredns` |
| `Version` | 组件 manifest 版本 | `v1.0.0` |
| `Mode` | 执行方式：`inline`(Go Phase) / `manifest`(YAML 清单) | inline 走 ComponentFactory，manifest 走 YamlInstaller |
| `ManifestPath` | manifest 模式的 YAML 路径 | `provider/v1.0.0/component.yaml` |
| `LegacyPhase` | 对应的旧 PhaseFlow Phase 名 | `EnsureEtcdUpgrade` |
| `InlineHandler` | ComponentFactory 注册的 handler key | `EnsureEtcdUpgrade` |

### UpgradeExecutionMode

```go
type UpgradeExecutionMode string

const (
    UpgradeExecutionManifest UpgradeExecutionMode = "manifest"
    UpgradeExecutionInline   UpgradeExecutionMode = "inline"
)
```

- `inline`：通过 ComponentFactory 注册的 Go Phase Handler 执行（复用现有 Phase 代码）
- `manifest`：通过 YamlInstaller 加载并 Apply YAML 清单文件执行

## 组件清单

当前 `DeclarativeUpgradeCatalog` 包含 9 个组件，分为两种执行模式：

### inline 模式（6 个，Go 代码执行）

| 组件名 | Inline Handler | 旧 Phase 名 | 说明 |
|--------|---------------|------------|------|
| `pre-upgrade-resources` | `EnsurePreUpgradeResources` | `EnsurePreUpgradeResources` | 升级前资源预创建（CRD/ConfigMap/Secret） |
| `bkeagent` | `EnsureAgentUpgrade` | `EnsureAgentUpgrade` | BKE Agent 二进制升级 |
| `etcd` | `EnsureEtcdUpgrade` | `EnsureEtcdUpgrade` | etcd 集群升级 |
| `kubernetes-master` | `EnsureMasterUpgrade` | `EnsureMasterUpgrade` | Master 控制面组件升级 |
| `kubernetes-worker` | `EnsureWorkerUpgrade` | `EnsureWorkerUpgrade` | Worker 节点 kubelet 升级 |
| `containerd` | `EnsureContainerdUpgrade` | `EnsureContainerdUpgrade` | 容器运行时升级 |

### manifest 模式（3 个，YAML 清单执行）

| 组件名 | ManifestPath | 旧 Phase 名 | 说明 |
|--------|-------------|------------|------|
| `provider` | `provider/v1.0.0/component.yaml` | `EnsureProviderSelfUpgrade` | Provider 自身升级 |
| `kube-proxy` | `kube-proxy/v1.0.0/component.yaml` | - | kube-proxy DaemonSet 升级 |
| `coredns` | `coredns/v1.0.0/component.yaml` | `EnsureComponentUpgrade` | CoreDNS Deployment 升级 |

## DAG 执行顺序

DAG 构建器遍历 `DeclarativeUpgradeCatalog`，为每个组件创建 `ComponentNode`，按依赖关系拓扑排序后逐个执行：

```
pre-upgrade-resources (inline)
    ↓
provider (manifest) + bkeagent (inline)     ← 并行
    ↓
containerd (inline)
    ↓
etcd (inline)
    ↓
kubernetes-master (inline)
    ↓
kubernetes-worker (inline)
    ↓
kube-proxy (manifest) + coredns (manifest)  ← 并行
```

## 执行路径

```
DAG Scheduler 遍历 DeclarativeUpgradeCatalog
    │
    ├─ Mode=inline
    │   └─ ComponentFactory.Resolve(handler, version)
    │       └─ Phase.NeedExecute() → Phase.Execute()
    │           (复用现有 PhaseFlow Phase 代码)
    │
    └─ Mode=manifest
        └─ ManifestStore.GetComponentManifests(name, version)
            └─ YamlInstaller.Apply()
                (加载 YAML 清单 → SSA Apply)
```

## 辅助函数

### ManifestComponentManifestPath

```go
func ManifestComponentManifestPath(componentName, version string) string {
    return componentName + "/" + version + "/component.yaml"
}
```

拼接 manifest 模式组件的 YAML 文件相对路径，如 `provider/v1.0.0/component.yaml`。

### InlineUpgradeHandlers

```go
func InlineUpgradeHandlers() []string
```

从 `DeclarativeUpgradeCatalog` 中提取所有 `Mode=inline` 的 handler 名称列表，供 ComponentFactory 批量注册时使用。

## 常量定义

```go
const ComponentManifestVersion = "v1.0.0"
```

所有组件的 manifest 版本统一为 `v1.0.0`（manifest 内容本身可随版本更新，此版本号是 manifest 结构格式的版本）。

```go
const (
    InlineHandlerPreUpgradeResources = "EnsurePreUpgradeResources"
    InlineHandlerEtcdUpgrade         = "EnsureEtcdUpgrade"
    InlineHandlerMasterUpgrade       = "EnsureMasterUpgrade"
    InlineHandlerWorkerUpgrade       = "EnsureWorkerUpgrade"
    InlineHandlerContainerdUpgrade   = "EnsureContainerdUpgrade"
    InlineHandlerAgentUpgrade        = "EnsureAgentUpgrade"
)
```

Inline handler 名称与 `phaseframe.Phase.Name()` 和 `ComponentVersion.spec.inline.handler` 保持一致，确保三方对齐。

## 设计意义

1. **解耦**：将升级组件定义从 BKECluster Controller 硬编码中提取为独立目录表，DAG 构建器只需遍历此表
2. **迁移桥梁**：`LegacyPhase` 字段记录旧 PhaseFlow Phase 名，支持声明式升级与旧版 PhaseFlow 并行运行和渐进式迁移
3. **统一入口**：inline 和 manifest 两种执行模式统一在同一数据结构中，DAG Scheduler 按类型分发
4. **可扩展**：新增组件只需在 catalog 中添加一条 `UpgradeComponentSpec`，无需修改 DAG 构建逻辑
