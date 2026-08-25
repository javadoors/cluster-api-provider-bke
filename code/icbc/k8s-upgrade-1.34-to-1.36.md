# K8s 1.34 → 1.36 升级方案详细设计

| 字段 | 值 |
|------|-----|
| **文档类型** | 升级方案设计 |
| **升级范围** | Kubernetes v1.34 → v1.36 |
| **配套组件** | containerd, etcd, kubelet, apiserver, controller-manager, scheduler |
| **状态** | `draft` |
| **创建日期** | 2026-08-25 |

---

## 目录

1. [版本兼容性矩阵](#1-版本兼容性矩阵)
2. [升级策略设计](#2-升级策略设计)
3. [组件升级详细方案](#3-组件升级详细方案)
4. [升级流程设计](#4-升级流程设计)
5. [回滚策略](#5-回滚策略)
6. [工作量详细评估](#6-工作量详细评估)
7. [风险与缓解措施](#7-风险与缓解措施)
8. [与工行协作计划](#8-与工行协作计划)

---

## 1. 版本兼容性矩阵

### 1.1 K8s 与配套组件版本对应关系

| 目标版本 | K8s | containerd | etcd | kubelet | 说明 |
|---------|-----|-----------|------|---------|------|
| **v1.34** | v1.34.x | v1.7.20 | v3.5.18 | v1.34.x | 当前版本 |
| **v1.35** | v1.35.x | v1.7.22 | v3.5.19 | v1.35.x | 第一跳 |
| **v1.36** | v1.36.x | v1.7.24 | v3.5.20 | v1.36.x | 目标版本（最新稳定版） |

> **注意**：截至 2026 年 8 月，K8s 最新稳定版本为 v1.36。v1.37 尚未发布。

### 1.2 版本倾斜策略约束

| 组件 | 约束规则 | 影响 |
|------|---------|------|
| **kubelet** | kubelet 不能比 apiserver 新，最多旧 2 个版本 | 必须先升级 master，再升级 worker |
| **kube-proxy** | 版本必须与 apiserver 一致 | 与 master 同步升级 |
| **kubectl** | 版本与 apiserver 相差不超过 1 个版本 | 与 master 同步升级 |
| **etcd** | 每个 K8s 版本有推荐的 etcd 版本 | 需要与 K8s 版本配套升级 |
| **containerd** | 需要支持目标 K8s 版本的 CRI | 需要与 K8s 版本配套升级 |

### 1.3 API 废弃清单（需 PreCheck 扫描）

| 版本 | 废弃 API | 替代 API | 影响范围 |
|------|---------|---------|---------|
| **v1.35** | `flowcontrol.apiserver.k8s.io/v1beta3` | `v1` | FlowSchema, PriorityLevelConfiguration |
| **v1.36** | `discovery.k8s.io/v1beta1` | `v1` | EndpointSlice |

---

## 2. 升级策略设计

### 2.1 升级路径选择

**推荐方案：逐版本升级**

```
v1.34 → v1.35 → v1.36
```

**理由**：
1. kubeadm 官方建议逐版本升级，不支持跨多个版本直接升级
2. 每个中间版本的 API 变更需要逐步适配
3. etcd 数据格式需要逐步迁移
4. 降低升级风险，每跳都可以验证和回滚

**备选方案：跳版本升级（不推荐）**

```
v1.34 → v1.36（直接跳版本）
```

**风险**：
- kubeadm 可能不支持
- API 变更累积，兼容性风险高
- etcd 数据格式可能不兼容
- 回滚粒度粗，只能回滚到 v1.34

### 2.2 升级顺序设计

**核心原则**：
1. **先控制面，后工作节点**：确保 kubelet 不会比 apiserver 新
2. **先 etcd，后 K8s**：确保 etcd 版本与 K8s 版本兼容
3. **先 containerd，后 kubelet**：确保容器运行时支持目标 K8s 版本
4. **逐节点滚动**：每个节点升级完成后等待 Ready，再升级下一个

**升级 DAG 设计（单跳示例：v1.34 → v1.35）**：

```
ReleaseImage v1.35.0 DAG:

Batch 1: [pre-upgrade-resources]
    └─ 创建升级所需的 CRD/RBAC 资源
    └─ 执行 PreCheck（API 兼容性、etcd 版本、节点健康）

Batch 2: [bkeagent]
    └─ 升级所有节点的 bkeagent 到 v1.35 兼容版本

Batch 3: [containerd]
    └─ 升级所有节点的 containerd 到 v1.7.22
    └─ 验证 CRI 接口兼容性

Batch 4: [etcd]
    └─ 滚动升级 etcd 到 v3.5.25
    └─ 每个节点：备份 → 升级 → 健康检查
    └─ 验证 etcd 集群健康

Batch 5: [kubernetes-master]
    └─ 滚动升级控制面组件
    └─ apiserver: v1.34 → v1.35
    └─ controller-manager: v1.34 → v1.35
    └─ scheduler: v1.34 → v1.35
    └─ 每个节点：drain → upgrade → uncordon → 健康检查

Batch 6: [kubernetes-worker]
    └─ 滚动升级 kubelet
    └─ kubelet: v1.34 → v1.35
    └─ 每个节点：drain → upgrade → uncordon → 健康检查

Batch 7: [kube-proxy, coredns]
    └─ kube-proxy DaemonSet 更新到 v1.35
    └─ coredns Deployment 更新到兼容版本

Batch 8: [post-upgrade-verification]
    └─ 执行 PostCheck（版本验证、集群健康、应用健康）
```

### 2.3 多跳升级编排

**完整升级流程（v1.34 → v1.36）**：

```
用户操作：修改 ClusterVersion.Spec.DesiredVersion = v1.36.0

系统自动编排：
┌─────────────────────────────────────────────────────────────────────┐
│  Hop 1: v1.34 → v1.35                                              │
│  ├─ 加载 ReleaseImage v1.35.0                                      │
│  ├─ 执行升级 DAG                                                    │
│  ├─ 验证升级成功                                                    │
│  └─ 更新 ClusterVersion.Status.CurrentVersion = v1.35.0            │
└─────────────────────────────────────────────────────────────────────┘
                                    ↓
┌─────────────────────────────────────────────────────────────────────┐
│  Hop 2: v1.35 → v1.36                                              │
│  ├─ 加载 ReleaseImage v1.36.0                                      │
│  ├─ 执行升级 DAG                                                    │
│  ├─ 验证升级成功                                                    │
│  └─ 更新 ClusterVersion.Status.CurrentVersion = v1.36.0            │
└─────────────────────────────────────────────────────────────────────┘
```

**关键设计**：
- 每跳之间自动执行集群健康检查
- 如果某一跳失败，自动停止并触发回滚到该跳的起始版本
- 用户可以在任意跳之间暂停升级（通过设置 `ClusterVersion.Spec.Paused = true`）

---

## 3. 组件升级详细方案

### 3.1 containerd 升级方案

**升级范围**：v1.7.20 → v1.7.22 → v1.7.24

**升级策略**：
- **滚动升级**：逐节点升级，不影响其他节点
- **配置保留**：保留 containerd 配置文件，仅更新二进制
- **CRI 兼容性**：验证 CRI 接口与目标 K8s 版本兼容

**升级步骤**：

```
对每个节点：
1. 标记节点为 ContainerdUpgrading
2. 停止 kubelet（避免 Pod 重启）
3. 停止 containerd
4. 备份当前 containerd 二进制和配置
5. 下载新版本 containerd 二进制
6. 更新 containerd 配置文件（sandbox_image 等）
7. 启动 containerd
8. 验证 containerd 健康（crictl info）
9. 启动 kubelet
10. 验证节点 Ready
11. 标记节点为 ContainerdUpgraded
```

**回滚步骤**：

```
对每个节点：
1. 停止 kubelet
2. 停止 containerd
3. 恢复备份的 containerd 二进制和配置
4. 启动 containerd
5. 启动 kubelet
6. 验证节点 Ready
```

**工作量分解**：

| 任务 | 说明 | 工作量 |
|------|------|--------|
| containerd 升级脚本开发 | 编写 containerd 升级/回滚脚本 | 2 天 |
| 配置渲染适配 | 适配不同版本的 containerd 配置模板 | 1 天 |
| CRI 兼容性测试 | 验证每个版本的 CRI 接口兼容性 | 2 天 |
| 滚动升级逻辑 | 实现节点级滚动升级和失败处理 | 2 天 |
| 集成测试 | containerd 升级与 K8s 升级的集成测试 | 2 天 |
| **小计** | - | **9 天** |

### 3.2 etcd 升级方案

**升级范围**：v3.5.18 → v3.5.19 → v3.5.20

**升级策略**：
- **滚动升级**：逐节点升级，保持 etcd 集群可用
- **数据备份**：每个节点升级前备份 etcd 数据
- **健康检查**：每个节点升级后验证 etcd 健康

**升级步骤**：

```
对每个 etcd 节点：
1. 标记节点为 EtcdUpgrading
2. 备份 etcd 数据（etcdctl snapshot save）
3. 备份当前 etcd Static Pod manifest 文件
4. 拉取新版本 etcd 镜像（crictl pull）
5. 更新 etcd Static Pod manifest（镜像版本）
6. 等待 Kubelet 检测文件变化，重建 etcd Pod
7. 验证 etcd 健康（etcdctl endpoint health）
8. 验证 etcd 版本（etcdctl --version）
9. 标记节点为 EtcdUpgraded
10. 等待 etcd 集群同步完成
```

**回滚步骤**：

```
对每个 etcd 节点：
1. 恢复备份的 manifest 文件（旧版本镜像）
2. 等待 Kubelet 重建 etcd Pod
3. 恢复 etcd 数据（如果数据格式不兼容，从 snapshot 恢复）
4. 验证 etcd 健康
```

**工作量分解**：

| 任务 | 说明 | 工作量 |
|------|------|--------|
| etcd 升级脚本开发 | 编写 etcd 升级/回滚/备份脚本 | 2 天 |
| 数据格式兼容性验证 | 验证 etcd 数据格式跨版本兼容性 | 2 天 |
| 滚动升级逻辑 | 实现 etcd 集群滚动升级和失败处理 | 2 天 |
| 健康检查开发 | 实现 etcd 健康检查和版本验证 | 1 天 |
| 集成测试 | etcd 升级与 K8s 升级的集成测试 | 2 天 |
| **小计** | - | **9 天** |

### 3.3 控制面组件升级方案

**升级范围**：
- apiserver: v1.34 → v1.35 → v1.36
- controller-manager: v1.34 → v1.35 → v1.36
- scheduler: v1.34 → v1.35 → v1.36

**升级策略**：
- **滚动升级**：逐 master 节点升级，保持控制面可用
- **Static Pod 方式**：通过替换 manifest 文件触发 Kubelet 重启 Pod
- **API 兼容性**：升级前扫描废弃 API，升级后验证 API 可用

**升级步骤**：

```
对每个 master 节点：
1. 标记节点为 MasterUpgrading
2. 备份 etcd 数据（如果是 etcd 节点）
3. 备份当前 Static Pod manifest 文件（apiserver, controller-manager, scheduler）
4. 拉取新版本 K8s 镜像（kube-apiserver, kube-controller-manager, kube-scheduler）
5. 更新 Static Pod manifest（镜像版本）
6. 等待 Kubelet 检测文件变化，逐个重建 Static Pod
7. 验证 apiserver 健康（kubectl get --raw=/healthz）
8. 验证 controller-manager 健康
9. 验证 scheduler 健康
10. 验证节点 Ready
11. 标记节点为 MasterUpgraded
```

**API 废弃处理**：

```
升级前（PreCheck）：
1. 扫描集群中使用的废弃 API
2. 生成废弃 API 使用报告
3. 如果存在废弃 API，提示用户迁移

升级后（PostCheck）：
1. 验证新 API 可用
2. 验证旧 API 已移除（如果已废弃）
3. 验证自定义资源正常
```

**工作量分解**：

| 任务 | 说明 | 工作量 |
|------|------|--------|
| 控制面升级脚本开发 | 编写控制面升级/回滚脚本 | 2 天 |
| API 废弃扫描工具 | 开发废弃 API 扫描和报告工具 | 3 天 |
| 滚动升级逻辑 | 实现控制面滚动升级和失败处理 | 2 天 |
| 健康检查开发 | 实现控制面组件健康检查 | 1 天 |
| 集成测试 | 控制面升级与 API 兼容性测试 | 3 天 |
| **小计** | - | **11 天** |

### 3.4 kubelet 升级方案

**升级范围**：v1.34 → v1.35 → v1.36

**升级策略**：
- **滚动升级**：逐 worker 节点升级，不影响业务
- **drain 优先**：升级前 drain 节点，驱逐 Pod
- **倾斜策略**：确保 kubelet 不比 apiserver 新

**升级步骤**：

```
对每个 worker 节点：
1. 标记节点为 WorkerUpgrading
2. drain 节点（kubectl drain --ignore-daemonsets --delete-emptydir-data）
3. 停止 kubelet
4. 备份当前 kubelet 二进制和配置
5. 下载新版本 kubelet 二进制
6. 更新 kubelet 配置文件（如需要）
7. 启动 kubelet
8. 验证 kubelet 版本（kubelet --version）
9. 验证节点 Ready（kubectl get node）
10. uncordon 节点（kubectl uncordon）
11. 验证 Pod 重新调度
12. 标记节点为 WorkerUpgraded
```

**工作量分解**：

| 任务 | 说明 | 工作量 |
|------|------|--------|
| kubelet 升级脚本开发 | 编写 kubelet 升级/回滚脚本 | 2 天 |
| drain/uncordon 逻辑 | 实现节点 drain 和 uncordon 逻辑 | 1 天 |
| 滚动升级逻辑 | 实现 worker 滚动升级和失败处理 | 2 天 |
| 倾斜策略验证 | 验证 kubelet 与 apiserver 版本倾斜策略 | 1 天 |
| 集成测试 | kubelet 升级与业务 Pod 兼容性测试 | 2 天 |
| **小计** | - | **8 天** |

### 3.5 kube-proxy / coredns 升级方案

**升级范围**：
- kube-proxy: v1.34 → v1.35 → v1.36
- coredns: 根据 K8s 版本配套升级

**升级策略**：
- **声明式组件管理**：通过 ComponentVersion (type: yaml/helm) 定义完整资源清单，纳入 DAG 统一调度
- **YamlInstaller 升级**：kube-proxy/coredns 通过 SSA Apply 应用新版本清单，自动处理字段变更和资源裁剪
- **完整清单替换**：不仅更新镜像版本，还更新 ConfigMap、RBAC、Service 等关联资源

**升级步骤**：

```
kube-proxy (type: yaml):
1. 加载目标版本 ComponentVersion（bke-manifests/kube-proxy/v1.36.0/）
2. 渲染完整资源清单（DaemonSet + ConfigMap + RBAC + Service）
3. YamlInstaller.Apply:
   a. 按依赖顺序应用资源（RBAC → ConfigMap → DaemonSet）
   b. 使用 SSA Apply 更新 DaemonSet（镜像版本 + 配置变更）
   c. Prune 不再需要的旧版本资源
4. 等待 DaemonSet 滚动更新完成
5. 健康检查:
   a. 验证 kube-proxy Pod Ready
   b. 验证 iptables/ipvs 规则正常
   c. 验证 Service ClusterIP 转发正常

coredns (type: yaml/helm):
1. 加载目标版本 ComponentVersion（bke-manifests/coredns/v1.11.x/）
2. 渲染完整资源清单（Deployment + ConfigMap + Service + RBAC）
3. YamlInstaller.Apply:
   a. 按依赖顺序应用资源（RBAC → ConfigMap → Deployment → Service）
   b. 使用 SSA Apply 更新 Deployment（镜像版本 + CoreDNS 配置变更）
   c. Prune 不再需要的旧版本资源
4. 等待 Deployment 滚动更新完成
5. 健康检查:
   a. 验证 coredns Pod Ready
   b. 验证 DNS 解析正常（nslookup kubernetes.default）
   c. 验证外部域名解析正常
```

**与镜像替换方案的区别**：

| 维度 | 仅更新镜像版本（非通用） | 声明式完整清单替换（通用） |
|------|----------------------|------------------------|
| **升级粒度** | 仅镜像 tag | 镜像 + ConfigMap + RBAC + Service |
| **配置变更** | 不处理 | SSA Apply 自动处理字段变更 |
| **资源裁剪** | 不支持 | Prune 自动清理废弃资源 |
| **版本追踪** | 无 | ComponentVersion 生命周期追踪 |
| **回滚能力** | 仅回滚镜像 | 回滚完整清单 |
| **DAG 集成** | 不集成 | 纳入 DAG 统一调度，支持依赖管理 |

**工作量分解**：

| 任务 | 说明 | 工作量 |
|------|------|--------|
| ComponentVersion 清单编写 | 编写 kube-proxy/coredns 各版本的完整 YAML 清单 | 1.5 天 |
| ConfigMap 渲染适配 | 适配不同 K8s 版本的 kube-proxy/coredns 配置模板 | 1 天 |
| SSA Apply + Prune 逻辑 | 实现 YamlInstaller 的 SSA Apply 和 Prune 逻辑 | 1 天 |
| 健康检查开发 | 实现 kube-proxy/coredns 健康检查（Pod Ready + 功能验证） | 1 天 |
| 集成测试 | DNS 解析、网络连通性、资源裁剪测试 | 1.5 天 |
| **小计** | - | **6 天** |

---

## 4. 升级流程设计

### 4.1 升级前准备（PreCheck）

**检查项清单**：

| 检查项 | 检查内容 | 阻断条件 |
|--------|---------|---------|
| **集群健康** | 所有节点 Ready，所有 Pod Running | 有 NotReady 节点或 Failed Pod |
| **etcd 健康** | etcd 集群健康，无告警 | etcd 集群不健康 |
| **API 兼容性** | 扫描废弃 API 使用 | 存在未迁移的废弃 API |
| **资源充足** | CPU/内存/磁盘充足 | 资源不足 |
| **备份验证** | etcd 备份存在且可恢复 | 备份不存在或不可恢复 |
| **版本兼容性** | 验证升级路径合法 | 升级路径不存在或被阻断 |

**PreCheck 执行流程**：

```
1. 用户触发升级（修改 ClusterVersion.Spec.DesiredVersion）
2. BKECluster Controller 检测到版本变更
3. 加载目标 ReleaseImage
4. 执行 PreCheck DAG
   ├─ Batch 1: [cluster-health-check]
   ├─ Batch 2: [etcd-health-check]
   ├─ Batch 3: [api-deprecation-scan]
   ├─ Batch 4: [resource-check]
   ├─ Batch 5: [backup-verification]
   └─ Batch 6: [upgrade-path-validation]
5. 生成 PreCheck 报告
6. 如果有阻断项，停止升级并报告
7. 如果全部通过，继续升级流程
```

**工作量分解**：

| 任务 | 说明 | 工作量 |
|------|------|--------|
| PreCheck 框架开发 | 实现 PreCheck 执行引擎和报告生成 | 2 天 |
| 集群健康检查 | 实现节点和 Pod 健康检查 | 1 天 |
| etcd 健康检查 | 实现 etcd 集群健康检查 | 1 天 |
| API 废弃扫描 | 开发废弃 API 扫描工具 | 3 天 |
| 资源检查 | 实现 CPU/内存/磁盘检查 | 1 天 |
| 备份验证 | 实现 etcd 备份验证 | 1 天 |
| 版本兼容性验证 | 实现升级路径验证 | 1 天 |
| 集成测试 | PreCheck 流程端到端测试 | 2 天 |
| **小计** | - | **12 天** |

### 4.2 升级执行

**单跳升级执行流程（v1.34 → v1.35 示例）**：

```
1. 加载 ReleaseImage v1.35.0
2. 构建升级 DAG
3. 执行升级 DAG
   ├─ Batch 1: [pre-upgrade-resources]
   │   └─ 创建升级所需的 CRD/RBAC 资源
   │
   ├─ Batch 2: [bkeagent]
   │   └─ 升级所有节点的 bkeagent
   │
   ├─ Batch 3: [containerd]
   │   └─ 滚动升级所有节点的 containerd
   │   └─ 每个节点：停止 → 备份 → 升级 → 启动 → 验证
   │
   ├─ Batch 4: [etcd]
   │   └─ 滚动升级 etcd 节点
   │   └─ 每个节点：备份 → 停止 → 升级 → 启动 → 健康检查
   │
   ├─ Batch 5: [kubernetes-master]
   │   └─ 滚动升级 master 节点
   │   └─ 每个节点：drain → 停止 → 升级 → 启动 → 健康检查 → uncordon
   │
   ├─ Batch 6: [kubernetes-worker]
   │   └─ 滚动升级 worker 节点
   │   └─ 每个节点：drain → 停止 → 升级 → 启动 → 健康检查 → uncordon
   │
   ├─ Batch 7: [kube-proxy, coredns]
   │   └─ 更新 kube-proxy DaemonSet
   │   └─ 更新 coredns Deployment
   │
   └─ Batch 8: [post-upgrade-verification]
       └─ 执行 PostCheck（版本验证、集群健康）
4. 更新 ClusterVersion.Status.CurrentVersion = v1.35.0
5. 记录升级历史
```

**多跳升级编排**：

```
用户触发升级（DesiredVersion = v1.36.0）
  │
  ├─ Hop 1: v1.34 → v1.35
  │   ├─ 执行 PreCheck
  │   ├─ 执行升级 DAG
  │   ├─ 执行 PostCheck
  │   └─ 更新 CurrentVersion = v1.35.0
  │
  ├─ 自动等待（可配置间隔，默认 5 分钟）
  │
  └─ Hop 2: v1.35 → v1.36
      ├─ 执行 PreCheck
      ├─ 执行升级 DAG
      ├─ 执行 PostCheck
      └─ 更新 CurrentVersion = v1.36.0
```

### 4.3 升级后验证（PostCheck）

**检查项清单**：

| 检查项 | 检查内容 | 阻断条件 |
|--------|---------|---------|
| **版本验证** | 所有组件版本正确 | 版本不匹配 |
| **集群健康** | 所有节点 Ready，所有 Pod Running | 有 NotReady 节点或 Failed Pod |
| **etcd 健康** | etcd 集群健康 | etcd 集群不健康 |
| **API 可用** | 核心 API 可用 | API 不可用 |
| **DNS 正常** | DNS 解析正常 | DNS 解析失败 |
| **应用健康** | 关键应用正常运行 | 关键应用异常 |

**PostCheck 执行流程**：

```
1. 升级 DAG 执行完成
2. 执行 PostCheck DAG
   ├─ Batch 1: [component-version-check]
   ├─ Batch 2: [cluster-health-check]
   ├─ Batch 3: [etcd-health-check]
   ├─ Batch 4: [api-availability-check]
   ├─ Batch 5: [dns-resolution-check]
   └─ Batch 6: [application-health-check]
3. 生成 PostCheck 报告
4. 如果有失败项，触发告警
5. 更新 ClusterVersion.Status.Phase = Ready
```

**工作量分解**：

| 任务 | 说明 | 工作量 |
|------|------|--------|
| PostCheck 框架开发 | 实现 PostCheck 执行引擎和报告生成 | 2 天 |
| 版本验证 | 实现组件版本验证 | 1 天 |
| 集群健康检查 | 实现节点和 Pod 健康检查 | 1 天 |
| etcd 健康检查 | 实现 etcd 集群健康检查 | 1 天 |
| API 可用性检查 | 实现核心 API 可用性检查 | 1 天 |
| DNS 解析检查 | 实现 DNS 解析检查 | 1 天 |
| 应用健康检查 | 实现关键应用健康检查 | 1 天 |
| 集成测试 | PostCheck 流程端到端测试 | 2 天 |
| **小计** | - | **10 天** |

---

## 5. 回滚策略

### 5.1 回滚范围

**仅支持集群级回滚**：
- 不支持组件级回滚（如仅回滚 containerd）
- 不支持节点级回滚（如仅回滚单个节点）
- 回滚时整个集群回退到该跳的起始版本

### 5.2 回滚触发条件

| 场景 | 触发方式 | 回滚范围 |
|------|---------|---------|
| **升级过程中失败** | 自动触发（FailurePolicy=Rollback） | 回滚到该跳的起始版本 |
| **升级完成后发现问题** | 用户手动触发（修改 DesiredVersion） | 回滚到指定版本 |

### 5.3 回滚执行流程

**单跳回滚（v1.35 回滚到 v1.34）**：

```
1. 检测到升级失败或用户触发回滚
2. 加载源版本 ReleaseImage v1.34.0
3. 构建回滚 DAG（升级 DAG 逆序）
4. 执行回滚 DAG
   ├─ Batch 1: [kube-proxy, coredns]
   │   └─ 回滚 kube-proxy/coredns 到 v1.34 版本
   │
   ├─ Batch 2: [kubernetes-worker]
   │   └─ 滚动回滚 worker 节点 kubelet
   │
   ├─ Batch 3: [kubernetes-master]
   │   └─ 滚动回滚 master 节点控制面组件
   │
   ├─ Batch 4: [etcd]
   │   └─ 滚动回滚 etcd（从备份恢复数据）
   │
   ├─ Batch 5: [containerd]
   │   └─ 滚动回滚 containerd
   │
   └─ Batch 6: [bkeagent]
       └─ 回滚 bkeagent
5. 更新 ClusterVersion.Status.CurrentVersion = v1.34.0
6. 执行 PostCheck 验证回滚成功
```

**多跳回滚（v1.36 回滚到 v1.34）**：

```
用户触发回滚（DesiredVersion = v1.34.0）
  │
  ├─ Hop 2 回滚: v1.36 → v1.35
  │   ├─ 执行回滚 DAG
  │   └─ 更新 CurrentVersion = v1.35.0
  │
  └─ Hop 1 回滚: v1.35 → v1.34
      ├─ 执行回滚 DAG
      └─ 更新 CurrentVersion = v1.34.0
```

### 5.4 etcd 数据回滚

**关键问题**：etcd 数据格式可能跨版本不兼容

**回滚策略**：
1. **升级前备份**：每个节点升级前备份 etcd 数据
2. **回滚时恢复**：如果 etcd 数据格式不兼容，从备份恢复
3. **数据丢失风险**：升级后产生的数据在回滚时会丢失

**工作量分解**：

| 任务 | 说明 | 工作量 |
|------|------|--------|
| 回滚框架开发 | 实现回滚执行引擎 | 2 天 |
| 回滚脚本开发 | 编写各组件回滚脚本 | 3 天 |
| etcd 数据恢复 | 实现 etcd 数据备份恢复逻辑 | 2 天 |
| 多跳回滚编排 | 实现多跳回滚的自动编排 | 2 天 |
| 回滚测试 | 回滚场景端到端测试 | 3 天 |
| **小计** | - | **12 天** |

---

## 6. 工作量详细评估

### 6.1 开发工作量

| 模块 | 任务 | 工作量（人天） |
|------|------|---------------|
| **containerd 升级** | 升级脚本、配置适配、CRI 兼容性、滚动升级、集成测试 | 9 |
| **etcd 升级** | 升级脚本、数据兼容性、滚动升级、健康检查、集成测试 | 9 |
| **控制面升级** | 升级脚本、API 废弃扫描、滚动升级、健康检查、集成测试 | 11 |
| **kubelet 升级** | 升级脚本、drain 逻辑、滚动升级、倾斜策略、集成测试 | 8 |
| **kube-proxy/coredns** | ComponentVersion 清单、ConfigMap 适配、SSA Apply/Prune、健康检查、集成测试 | 6 |
| **PreCheck** | 框架开发、各项检查实现、集成测试 | 12 |
| **PostCheck** | 框架开发、各项检查实现、集成测试 | 10 |
| **回滚** | 框架开发、回滚脚本、etcd 恢复、多跳编排、测试 | 12 |
| **ReleaseImage 定义** | 定义 v1.35/v1.36 两个版本的 ReleaseImage | 1.5 |
| **UpgradePath 配置** | 配置升级路径和兼容性规则 | 1 |
| **多跳升级编排** | 实现多跳升级的自动编排和错误处理 | 5 |
| **小计** | - | **84.5 人天** |

### 6.2 测试工作量

| 测试类型 | 测试内容 | 工作量（人天） |
|---------|---------|---------------|
| **单元测试** | 各模块单元测试 | 5 |
| **集成测试** | 单跳升级集成测试（v1.34→v1.35, v1.35→v1.36） | 8 |
| **E2E 测试** | 完整升级流程端到端测试（v1.34→v1.36） | 8 |
| **回滚测试** | 各跳回滚场景测试 | 5 |
| **兼容性测试** | 业务应用兼容性测试 | 5 |
| **性能测试** | 升级过程性能影响测试 | 3 |
| **压力测试** | 大规模集群升级压力测试 | 3 |
| **小计** | - | **37 人天** |

### 6.3 文档工作量

| 文档类型 | 文档内容 | 工作量（人天） |
|---------|---------|---------------|
| **升级指南** | 用户升级操作指南 | 2 |
| **兼容性矩阵** | 版本兼容性矩阵文档 | 1 |
| **故障排查** | 升级故障排查指南 | 2 |
| **API 变更** | K8s 1.34→1.36 API 变更清单 | 2 |
| **Release Notes** | 各版本 Release Notes | 1 |
| **小计** | - | **8 人天** |

### 6.4 总工作量汇总

| 类别 | 工作量（人天） | 工作量（人周） |
|------|---------------|---------------|
| **开发** | 84.5 | 16.9 |
| **测试** | 37 | 7.4 |
| **文档** | 8 | 1.6 |
| **总计** | **129.5** | **25.9** |

**按人员配置估算**：
- 如果 2 人全职投入：约 13 周（3 个月）
- 如果 3 人全职投入：约 9 周（2 个月）
- 如果 4 人全职投入：约 6.5 周（1.5 个月）

---

## 7. 风险与缓解措施

### 7.1 技术风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **kubeadm 不支持逐版本升级** | 必须跳版本升级，风险高 | 低 | 提前验证 kubeadm 行为，准备跳版本方案 |
| **etcd 数据格式不兼容** | etcd 升级失败，集群不可用 | 中 | 升级前备份，验证 etcd 版本兼容性，准备数据迁移脚本 |
| **API 废弃导致资源失效** | 升级后部分资源无法创建 | 高 | PreCheck 扫描废弃 API，提供迁移工具 |
| **containerd CRI 不兼容** | kubelet 无法启动 | 中 | 验证 CRI 接口兼容性，准备回滚方案 |
| **kubelet 倾斜策略违反** | kubelet 无法注册到 apiserver | 中 | 确保先升级 master 再升级 worker |

### 7.2 运维风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **升级时间过长** | 业务中断时间长 | 高 | 优化升级脚本，支持并行升级（多节点同时） |
| **回滚失败** | 无法恢复到稳定状态 | 中 | 充分测试回滚流程，准备手动恢复方案 |
| **数据丢失** | 升级后产生的数据在回滚时丢失 | 高 | 升级前通知用户，避免在升级窗口期写入重要数据 |

### 7.3 业务风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **业务 Pod 重启** | 业务短暂中断 | 高 | 升级前通知用户，选择业务低峰期升级 |
| **DNS 短暂不可用** | 业务解析失败 | 中 | coredns 升级采用滚动更新，保持至少一个副本可用 |
| **网络中断** | kube-proxy 升级导致网络中断 | 中 | kube-proxy 升级采用滚动更新，保持至少一个副本可用 |

---

## 8. 与工行协作计划

### 8.1 协作事项

| 事项 | 责任方 | 时间 | 交付物 |
|------|--------|------|--------|
| **提供 K8s 版本变更清单** | openFuyao 社区 | 8 月底 | K8s 1.35/1.36 API 变更清单 |
| **提供兼容性矩阵** | openFuyao 社区 | 8 月底 | K8s/containerd/etcd 版本兼容性矩阵 |
| **验证工行环境 API 兼容性** | 工行 | 9 月初 | API 兼容性扫描报告 |
| **提供升级方案评审** | 双方 | 9 月中旬 | 升级方案评审纪要 |
| **联合测试升级流程** | 双方 | 9 月下旬 | 测试报告 |
| **确认回滚策略和 SLA** | 双方 | 9 月底 | 回滚策略文档 |
| **生产环境升级** | 双方 | 10 月 | 升级完成报告 |

### 8.2 里程碑计划

```
8 月底 ──── 9 月中旬 ──── 9 月底 ──── 10 月中旬 ──── 10 月底
   │            │            │              │              │
   ├─ 提供变更清单          │              │              │
   ├─ 提供兼容性矩阵        │              │              │
   │            │            │              │              │
   │         ├─ API 兼容性验证             │              │
   │         ├─ 升级方案评审               │              │
   │            │            │              │              │
   │            │         ├─ 联合测试       │              │
   │            │         ├─ 回滚策略确认   │              │
   │            │            │              │              │
   │            │            │           ├─ 测试环境升级   │
   │            │            │              │              │
   │            │            │              │           ├─ 生产环境升级
```

### 8.3 风险共担

| 风险 | openFuyao 社区责任 | 工行责任 |
|------|-------------------|---------|
| **升级失败** | 提供回滚方案和技术支持 | 配合执行回滚操作 |
| **数据丢失** | 提供备份恢复工具 | 升级前确认数据备份 |
| **业务中断** | 优化升级流程，减少中断时间 | 选择业务低峰期升级 |
| **兼容性问题** | 提供兼容性扫描工具 | 提前验证业务应用兼容性 |

---

## 附录

### A. 参考文档

1. [Kubernetes 升级指南](https://kubernetes.io/docs/tasks/administer-cluster/kubeadm/kubeadm-upgrade/)
2. [etcd 升级指南](https://etcd.io/docs/v3.5/upgrades/)
3. [containerd 发布说明](https://github.com/containerd/containerd/releases)
4. [KEP-5 声明式升级框架](../kep/kep5/kep5.md)
5. [KEP-6 三层状态机设计](../kep/kep6/kep6-state-machine-v5.md)

### B. 术语表

| 术语 | 定义 |
|------|------|
| **Hop** | 一次版本跳转，如 v1.34 → v1.35 |
| **PreCheck** | 升级前检查，验证升级条件 |
| **PostCheck** | 升级后检查，验证升级结果 |
| **倾斜策略** | kubelet 版本不能比 apiserver 新，最多旧 2 个版本 |
| **滚动升级** | 逐节点升级，保持集群可用 |
| **drain** | 驱逐节点上的 Pod，使节点不可调度 |
| **uncordon** | 使节点可调度，允许 Pod 调度到该节点 |
