
# 2026.12

## 升级框架完善

- [框架支持二进制组件](../kep/kep6/声明式集群版本升级方案-支持二进制与Helm组件.md)
  - [二进制改造](../kep/kep6/kep13-binary-component-migration-design.md)
- [支持三层状态机（集群、节点、组件）](../kep/kep6/kep6-state-machine-v5.md)
- [支持可观测性](../kep/kep6/kep6-state-machine-v5.md)
- [支持静态Pod组件](../kep/kep6/kep9-staticpod-upgrade-framework.md)
- [支持预检](../kep/kep5/kep5-2-precheck-postcheck-design-v2.md)

## [安装流程与升级框架统一](../kep/kep6/kep10-install-components-declarative-design.md)

## [支持回滚](../kep/kep6/kep11-cluster-rollback-design.md)

## [备份与恢复](../kep/kep6/kep12-backup-restore-design.md)

## [k8s大版本升级(1.34->1.36)](../icbc/k8s-upgrade-1.34-to-1.36.md)

## 组件Operator化

## [ICBC](../icbc/kep-docs-discussion.md)

---

## 工作量评估

> 评估依据：各 KEP 文档中的工作量评估章节。人天与人月换算按 20 人天/人月。

### 总览

| 工作项 | 评估来源 | 工作量 | 备注 |
|--------|---------|--------|------|
| **升级框架完善** | — | **~12.5 人月** | 含下方 5 个子项 |
| 框架支持二进制组件 | KEP-16 + KEP-13 + KEP-15 + KEP-17 | ~8.5 人月 | 去重叠后合计 |
| 支持三层状态机 | KEP-6 v5（无评估） | ~3.0 人月 | 按 L1/L2/L3 状态机 + CAPI 集成估算 |
| 支持可观测性 | KEP-6 v5 §6（无评估） | ~1.0 人月 | 状态查询 + Event + Prometheus 指标 |
| 支持静态 Pod 组件 | KEP-9 | 60 人天 (~3.0 人月) | 含 etcd/apiserver/cm/scheduler 等 6 个组件 |
| 支持预检 | KEP-5-2 | 27 人天 (~1.4 人月) | CheckPolicy CRD + 4 预检 + 4 后检 |
| **安装流程与升级框架统一** | KEP-10 | 124 人天 (~6.2 人月) | 4 阶段：结构扩展→渐进迁移→全 DAG→遗留移除 |
| **支持回滚** | KEP-11 | 13.7 人月 | PhaseFlow 回滚(P0) + ClusterVersion 回滚(P1) |
| **备份与恢复** | KEP-12 | 10.5 人月 | M1 基础备份恢复(P0) + M2 应用备份灾备(P1) |
| **k8s 大版本升级 (1.34→1.36)** | k8s-upgrade-1.34-to-1.36 | 91 人天 (~4.6 人月) | 依赖升级框架已就绪，含跨版本验证 |
| **组件 Operator 化** | kep-docs-discussion.md | ~1.5 人月 | 16 个组件重构（10 Binary + 6 StaticPod），4-6 周 |
| **合计** | — | **~50 人月** | 去除重叠后的乐观估算 |

---

### 1. 升级框架完善

#### 1.1 框架支持二进制组件

| 子项 | KEP | 工作量 | 说明 |
|------|-----|--------|------|
| BinaryInstaller 完整设计 | [KEP-16](../kep/kep6/kep16-binary-component-design.md) | 96 人天 (~4.8 人月) | 含 BinaryInstaller/ConfigRenderer/TemplateRenderer + containerd/docker/bkeagent 重构 |
| 二进制改造迁移 | [KEP-13](../kep/kep6/kep13-binary-component-migration-design.md) | 42 + 19 = 61 人天 (~3.1 人月) | Phase 1: 基础迁移 42 人天；Phase 2: kubelet 迁移 19 人天 |
| Composite 组件类型 | [KEP-15](../kep/kep6/kep15-composite-component-design.md) | 19 人天 (~1.0 人月) | K8s 核心组件组合管理 + kubernetesVersion + deferredSubComponents |
| Selector 组件类型 | [KEP-17](../kep/kep6/kep17-selector-component-design.md) | 14 人天 (~0.7 人月) | condition 互斥选择 + 依赖展开 |

> **重叠说明**：KEP-16 已包含 selector 类型实现（2 人天）和 containerd/docker/bkeagent 重构，与 KEP-13/KEP-17 存在重叠。去重后合计约 **~8.5 人月**。

**KEP-16 详细分解**：

| 模块 | 人天 |
|------|------|
| BinaryInstaller 核心 | 5 |
| YamlComponentExecutor 核心 | 4 |
| TemplateRenderer + ConfigRenderer | 6 |
| ApplyStrategy + Prune | 4 |
| 健康检查（Binary + YAML） | 3 |
| ComponentVersion CRD 扩展 | 3 |
| VersionContext + ExecutionContext | 4 |
| containerd/docker/bkeagent 重构 | 13 |
| EnsureNodesEnv 重构 | 3 |
| Executor 集成（Binary + YAML） | 5 |
| DAG 调度器适配 | 3 |
| Feature Gate + 兼容层 | 4 |
| 错误处理与恢复 | 3 |
| 测试（单元 + 集成 + E2E） | 27 |
| 迁移验证 + 文档 + 代码审查 | 11 |
| **合计** | **96** |

#### 1.2 支持三层状态机

| KEP | 评估状态 |
|-----|---------|
| [KEP-6 v5](../kep/kep6/kep6-state-machine-v5.md) | 无工作量评估章节 |

**估算依据**（按设计范围推算）：

| 模块 | 估算（人月） |
|------|------------|
| L1 集群层状态机（BKECluster Controller + DAG 构建/执行 + node-group 驱动） | 1.2 |
| L2 节点层状态机（BKEMachine Controller + 节点状态转换） | 0.8 |
| L3 组件层状态机（组件状态追踪 + 幂等 + 状态聚合） | 0.5 |
| CAPI 集成（BKEMachine CRD 扩展 + 标准 Conditions + Watch 协调） | 0.5 |
| **合计** | **~3.0** |

#### 1.3 支持可观测性

| KEP | 评估状态 |
|-----|---------|
| [KEP-6 v5 §6](../kep/kep6/kep6-state-machine-v5.md) | 无独立工作量评估 |

**估算依据**（KEP-6 v5 §6 设计范围）：

| 模块 | 估算（人月） |
|------|------------|
| 状态可观测（BKECluster/BKEMachine Status + Conditions 暴露） | 0.3 |
| Event 可观测（StateTransition/Operation/Component 事件） | 0.3 |
| Metric 可观测（Prometheus gauges: cluster/node/component phase） | 0.4 |
| **合计** | **~1.0** |

#### 1.4 支持静态 Pod 组件

| KEP | 工作量 |
|-----|--------|
| [KEP-9](../kep/kep6/kep9-staticpod-upgrade-framework.md) | 60 人天 (~3.0 人月) |

| 类别 | 人天 |
|------|------|
| 开发 | 38 |
| 测试 | 22 |

**开发分解**：StaticPodSpec 类型定义(2) + StaticPodInstaller 安装/升级/回滚(12) + 健康检查(2) + DAG 集成(3) + 5 个组件 ComponentVersion(etcd/apiserver/cm/scheduler/haproxy/keepalived)(15) + Feature Gate(1) + DAG 回滚集成(3)

#### 1.5 支持预检

| KEP | 工作量 |
|-----|--------|
| [KEP-5-2](../kep/kep5/kep5-2-precheck-postcheck-design-v2.md) | 27 人天 (~1.4 人月) |

| 模块 | 人天 |
|------|------|
| CheckPolicy CRD + Webhook | 2 |
| CheckPolicyResolver | 2 |
| 检查框架（CheckRegistry + CheckRunner） | 3 |
| 预检项开发（4 项） | 6 |
| 后检项开发（4 项） | 5 |
| 集成开发 | 3 |
| 可观测性 | 2 |
| 测试 | 4 |

---

### 2. 安装流程与升级框架统一

| KEP | 工作量 |
|-----|--------|
| [KEP-10](../kep/kep6/kep10-install-components-declarative-design.md) | 124 人天 (~6.2 人月) |

| 阶段 | 开发 | 测试 | 小计 |
|------|------|------|------|
| Phase 1: 结构扩展 | 30 | 8 | 38 |
| Phase 2: 渐进迁移 | 15 | 5 | 20 |
| Phase 3: 全 DAG | 18 | 7 | 25 |
| Phase 4: 遗留移除 | 25 | 9 | 34 |
| 文档 | — | — | 7 |
| **合计** | **88** | **29** | **124** |

> 跨 4 个 openFuyao 版本：v2.7.0(38) → v2.8.0(20) → v2.9.0(25) → v3.0.0(34)

---

### 3. 支持回滚

| KEP | 工作量 |
|-----|--------|
| [KEP-11](../kep/kep6/kep11-cluster-rollback-design.md) | 13.7 人月 |

| 模块 | 估算（人月） | 优先级 |
|------|------------|--------|
| PhaseFlow 操作回滚 | 4.3 | P0 |
| ClusterVersion 版本回滚 | 8.6 | P1 |
| 安装失败处理 | 0.8 | P0 |
| **合计** | **13.7** | |

> 里程碑：M1 (2027Q1, 5.1 人月) → M2 (2027Q2, 2.3 人月) → M3 (2027Q3, 6.3 人月)

---

### 4. 备份与恢复

| KEP | 工作量 |
|-----|--------|
| [KEP-12](../kep/kep6/kep12-backup-restore-design.md) | 10.5 人月 |

| 里程碑 | 估算（人月） | 优先级 |
|--------|------------|--------|
| M1: 基础备份恢复（etcd + 配置） | 6.0 | P0 |
| M2: 应用备份 + 灾备（Velero + PVC 快照） | 4.5 | P1 |
| **合计** | **10.5** | |

---

### 5. k8s 大版本升级 (1.34→1.36)

| 文档 | 工作量 |
|------|--------|
| [k8s-upgrade-1.34-to-1.36](../icbc/k8s-upgrade-1.34-to-1.36.md) | 91 人天 (~4.6 人月) |

| 类别 | 人天 |
|------|------|
| 开发 | 41 |
| 测试 | 42 |
| 文档 | 8 |

> **前提**：声明式升级框架（DAG 调度、SSH 推送等）已就绪。本项工作主要是版本适配、kubeadm 跨版本验证、测试验证。
>
> 人员配置：2 人全职约 2 个月；3 人全职约 1.5 个月；4 人全职约 1 个月。

---

### 6. 组件 Operator 化

| 文档 | 工作量 |
|------|--------|
| [kep-docs-discussion.md §3.3.4](../icbc/kep-docs-discussion.md) | ~1.5 人月 (4-6 周) |

| 组件类型 | 数量 | 复杂度 | 估算 |
|---------|------|--------|------|
| Binary 类型 | 10 | 中（installScript + configTemplates） | 2-3 周 |
| StaticPod 类型 | 6 | 高（manifestTemplate + 健康检查） | 2-3 周 |
| **合计** | **16** | — | **4-6 周** |

> 组件优先级：P0（bkeagent/containerd/kubelet/kubectl/runc/etcd/apiserver/cm/scheduler）→ P1（haproxy/keepalived）→ P2（helm/etcdctl/calicoctl/lxcfs/nfs-utils）

---

### 7. ICBC

| 文档 | 工作量 |
|------|--------|
| [kep-docs-discussion.md](../icbc/kep-docs-discussion.md) | 无独立评估 |

**讨论范围**（5 个议题）：

1. 二进制组件迁移方案适配 ICBC 场景
2. 三层状态机与 ICBC 现有监控系统集成
3. 可观测性数据接入 ICBC 监控系统
4. 备份回滚策略与 ICBC SLA 对齐
5. ICBC 专属预检需求

> ICBC 协作里程碑：8 月底（API 变更清单 + 兼容性矩阵）→ 9 月中（方案评审）→ 9 月底（联合测试）→ 10 月（生产环境升级）

---

### 重叠与依赖说明

| 重叠项 | 说明 |
|--------|------|
| KEP-16 ↔ KEP-13 | KEP-16 含 containerd/docker/bkeagent 重构（13 人天），KEP-13 含相同组件迁移，已去重 |
| KEP-16 ↔ KEP-17 | KEP-16 含 selector 类型实现（2 人天），KEP-17 为详细设计（14 人天），已去重 |
| KEP-9 ↔ 组件 Operator 化 | KEP-9 含 6 个 StaticPod 组件 ComponentVersion，与组件 Operator 化的 StaticPod 部分重叠 |
| k8s 大版本升级 ↔ 升级框架 | k8s 升级依赖升级框架已就绪，若框架未完成则需额外适配工作 |
| KEP-10 ↔ KEP-16 | 安装流程统一(KEP-10)与二进制组件框架(KEP-16)共享 DAG 调度器，部分开发工作重叠 |

### 人力配置建议

| 团队规模 | 预计周期 | 适用场景 |
|---------|---------|---------|
| 3 人 | ~17 个月 | 全量并行，含所有工作项 |
| 5 人 | ~10 个月 | 全量并行，加速关键路径 |
| 8 人 | ~6 个月 | 全量并行，优先 P0 项 |

> **关键路径**：升级框架完善（KEP-16 → KEP-13 → KEP-9）→ 安装流程统一（KEP-10）→ k8s 大版本升级 → 回滚（KEP-11）+ 备份恢复（KEP-12）
