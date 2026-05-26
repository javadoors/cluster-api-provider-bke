# BKE 使用的 Cluster API 版本与最新版本的差距分析。

## BKE 使用的 Cluster API 版本与最新版本差距分析

### 版本对比总览
| 组件 | BKE 当前版本 | 社区最新版本 | 差距 |
|------|-------------|-------------|------|
| **sigs.k8s.io/cluster-api** | **v1.5.0** | **v1.12.0** | 落后 **7 个小版本** |
| sigs.k8s.io/controller-runtime | v0.22.4 (go.mod) / v0.15.3 (replace) | v0.20+ | replace 指向旧版 |
| Kubernetes 客户端库 (replace) | v0.28.0 | v1.34.x | 落后 **6 个小版本** |
| API 版本 | `cluster.x-k8s.io/v1beta1` | `cluster.x-k8s.io/v1beta1` | API 版本一致 |

### 关键版本差距详情

#### 1. Cluster API v1.5 → v1.12 之间缺失的核心特性
| 版本 | 发布时间 | 核心特性 | BKE 是否受益 |
|------|---------|---------|-------------|
| **v1.6** | 2023-11 | MachineHealthCheck 增强、ClusterClass 变更验证 | ✅ 可替代 BKE 自研的节点健康检查 |
| **v1.7** | 2024-04 | KubeadmControlPlane 升级策略增强（RolloutAfter、NodeDrainTimeout） | ✅ 可替代 BKE 自研的 Master 升级 Phase |
| **v1.8** | 2024-08 | ClusterClass 变量注入、Topology 级别默认值 | ✅ 可简化 BKECluster Spec 设计 |
| **v1.9** | 2024-12 | MachinePool GA、KubeadmControlPlane Remediatio | ✅ MachinePool 可替代 BKE 自研的 Worker 批量管理 |
| **v1.10** | 2025-04 | ClusterClass 增量补丁、MachineDeployment 策略扩展 | ✅ 可替代 BKE 自研的 Worker 升级 Phase |
| **v1.11** | 2025-08 | KubeadmControlPlane 证书自动轮换增强 | ✅ 可替代 BKE 自研的证书管理 Phase |
| **v1.12** | 2026-01 | **In-place Updates（原地更新）**、**Chained Upgrades（链式升级）** | ✅✅ **核心特性，直接解决 BKE 升级架构问题** |

#### 2. v1.12 两个核心特性对 BKE 的价值
**In-place Updates（原地更新）**：
- CAPI v1.12 引入了 Update Extension 机制，允许在不删除/重建 Machine 的情况下原地更新节点
- BKE 当前升级流程是：创建新 Machine → 等待就绪 → 删除旧 Machine（不可变基础设施模式）
- In-place Updates 可以直接在现有节点上执行升级操作（如修改凭证、更新配置），**无需 Pod 驱逐和重建**
- 这正是 BKE 当前 Phase 架构试图实现但做得不够好的能力

**Chained Upgrades（链式升级）**：
- CAPI v1.12 允许用户声明目标 K8s 版本后，自动编排中间升级步骤
- 例如从 v1.26 直接声明升级到 v1.30，CAPI 自动执行 v1.26→v1.27→v1.28→v1.29→v1.30
- BKE 当前升级路径硬编码在 [list.go](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/list.go) 中，无法跨版本跳级
- Chained Upgrades 直接解决了 BKE 的升级路径固定问题

#### 3. BKE 当前对 CAPI 的使用方式问题
从代码分析来看，BKE 对 CAPI 的使用存在**半用半不用**的问题：

| CAPI 标准能力 | BKE 当前做法 | 问题 |
|--------------|-------------|------|
| **KubeadmControlPlane** 管理 Master 生命周期 | BKE 创建了 KCP 对象，但升级时通过 `cluster.x-k8s.io/paused` 注解暂停 KCP，然后自研 Phase 手动编排 Master 升级 | 绕过 KCP 的声明式升级能力，自研命令式升级逻辑 |
| **MachineDeployment** 管理 Worker 生命周期 | BKE 创建了 MD 对象，但升级时暂停 MD，手动操作 replicas 和 delete annotation | 绕过 MD 的滚动更新能力，自研命令式缩容逻辑 |
| **ClusterClass** 模板化集群定义 | BKE 未使用 ClusterClass，每个集群独立定义 Cluster/KCP/MD | 无法统一管理集群模板，配置分散 |
| **MachineHealthCheck** 自动修复 | BKE 未使用 MHC，自研节点健康检查逻辑 | 重复造轮子，且自研逻辑不如社区成熟 |

#### 4. controller-runtime 版本矛盾
[go.mod:62](file:///d:/code/github/aaa/cluster-api-provider-bke/go.mod#L62) 声明 `sigs.k8s.io/controller-runtime v0.22.4`，但 [go.mod:97](file:///d:/code/github/aaa/cluster-api-provider-bke/go.mod#L97) 的 replace 将其替换为 `v0.15.3`：
```
sigs.k8s.io/controller-runtime => sigs.k8s.io/controller-runtime v0.15.3
```
- CAPI v1.5 依赖 controller-runtime v0.15.x
- CAPI v1.12 依赖 controller-runtime v0.20+
- 这个 replace 导致 BKE 实际运行的是 **v0.15.3**（2023 年版本），缺失 v0.16+ 的诸多改进（如改进的 Event 广播、更好的 Rate Limiter 等）

### 升级路径建议
```
BKE 当前: CAPI v1.5 + controller-runtime v0.15.3 (replace)
    ↓
第一步: CAPI v1.7 (获得 KCP 升级策略增强，可替代自研 Master 升级 Phase)
    ↓
第二步: CAPI v1.9 (获得 MachinePool GA，可替代自研 Worker 批量管理)
    ↓
第三步: CAPI v1.12 (获得 In-place Updates + Chained Upgrades，根本解决升级架构问题)
```

### 核心结论
BKE 使用的 CAPI **v1.5.0 落后社区最新 v1.12.0 整整 7 个小版本**，最关键的差距是：
1. **v1.12 的 In-place Updates**：直接解决 BKE 当前"升级必须删除重建 Machine"的问题，支持原地更新
2. **v1.12 的 Chained Upgrades**：直接解决 BKE 当前"升级路径硬编码、无法跨版本跳级"的问题
3. **v1.7+ 的 KCP 升级策略增强**：BKE 当前绕过 KCP 自研升级逻辑，而社区已经提供了成熟的声明式升级能力


BKE 当前最大的架构问题不是 CAPI 版本本身，而是**虽然引入了 CAPI，却绕过了 CAPI 的核心能力（KCP/MD 的声明式生命周期管理），自研了一套命令式 Phase 编排**。升级 CAPI 版本的同时，必须同步重构为**真正利用 CAPI 声明式能力**的架构。
        
