# KEP-7: LTS 版本升级方案 - 从 25.12(LTS) 到 26.12(LTS)

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-7 |
| **标题** | LTS 版本升级方案 - 从 25.12(LTS) 到 26.12(LTS) |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-09-09 |
| **依赖** | KEP-6 声明式升级框架、KEP-10 ReleaseImage 安装组件声明式定义 |

---

## 1. 摘要

本提案设计从 25.12(LTS) 升级到 26.12(LTS) 的完整方案。由于 25.12 和 26.03 版本不支持声明式升级，需要通过预处理和 ReleaseImage 构建来实现多 Hop 升级。升级路径为：25.12(LTS) -> 26.03 -> 26.06 -> 26.09 -> 26.12(LTS)，其中 26.06 及之后版本支持声明式升级。

---

## 2. 动机

### 2.1 版本兼容性问题

| 版本 | 升级方式 | 兼容性 |
|------|---------|--------|
| 25.12(LTS) | PhaseFlow | 管理集群存在不兼容变更 |
| 26.03 | PhaseFlow | 管理集群存在不兼容变更 |
| 26.06 | 声明式升级 | 支持多 Hop 升级 |
| 26.09 | 声明式升级 | 支持多 Hop 升级 |
| 26.12(LTS) | 声明式升级 | 支持多 Hop 升级 |

### 2.2 升级约束

1. **25.12(LTS)->26.03**: 管理集群存在不兼容变更，需要预处理
2. **26.03->26.06**: 管理集群存在不兼容变更，需要预处理
3. **26.06 及之后**: 支持声明式升级方案
4. **ReleaseImage 构建**: 25.12 和 26.03 需要基于声明式升级方案构建 ReleaseImage
5. **管理集群升级**: 25.12(LTS) 管理集群需要先升级以支持 26.06 的声明式升级方案

---

## 3. 设计方案

### 3.1 整体升级路径

```
25.12(LTS) --[预处理]--> 26.03 --[预处理]--> 26.06 --> 26.09 --> 26.12(LTS)
   |                      |                      |          |          |
   |                      |                      |          |          |
PhaseFlow            PhaseFlow              声明式升级    声明式升级   声明式升级
(不兼容变更)         (不兼容变更)         (多Hop支持)   (多Hop支持)  (多Hop支持)
```

### 3.2 升级阶段划分

#### 阶段 1: 25.12(LTS) -> 26.03 (预处理)

**目标**: 解决管理集群不兼容变更

**预处理内容**:
1. **CRD 迁移**: 处理 CRD 版本升级
2. **API 兼容性**: 处理 API 字段变更
3. **数据迁移**: 处理数据存储格式变更
4. **配置迁移**: 处理配置格式变更

**升级方式**: PhaseFlow (传统方式)

#### 阶段 2: 26.03 -> 26.06 (预处理)

**目标**: 解决管理集群不兼容变更，为声明式升级做准备

**预处理内容**:
1. **声明式升级框架引入**: 引入 DAG 调度器
2. **ReleaseImage 构建**: 构建支持声明式升级的 ReleaseImage
3. **ExecutorRegistry 扩展**: 扩展执行器注册表

**升级方式**: PhaseFlow (传统方式)

#### 阶段 3: 26.06 -> 26.09 (声明式升级)

**目标**: 使用声明式升级方案

**升级方式**: 声明式升级 (DAG)

**特点**:
- 支持多 Hop 升级
- 支持断点续传
- 支持并行执行

#### 阶段 4: 26.09 -> 26.12(LTS) (声明式升级)

**目标**: 使用声明式升级方案升级到 LTS 版本

**升级方式**: 声明式升级 (DAG)

### 3.3 ReleaseImage 构建方案

#### 3.3.1 25.12(LTS) ReleaseImage 构建

```yaml
# ReleaseImage for 25.12(LTS)
apiVersion: config.openfuyao.cn/v1alpha1
kind: ReleaseImage
metadata:
  name: openfuyao-v25.12
spec:
  version: "25.12"
  
  install:
    components:
      # 基础组件 (PhaseFlow 方式)
      - name: bkeagent
        version: v25.12.0
      - name: nodes-env
        version: v1.0.0
      - name: cluster-api-obj
        version: v1.0.0
      - name: certs
        version: v1.0.0
      - name: load-balance
        version: v1.0.0
      - name: kubernetes-master
        version: v1.25.0
      - name: kubernetes-worker
        version: v1.25.0
      - name: kube-proxy
        version: v1.25.0
      - name: coredns
        version: v1.9.0
      - name: nodes-postprocess
        version: v1.0.0
      - name: agent-switch
        version: v1.0.0
  
  upgrade:
    components:
      # 升级组件 (PhaseFlow 方式)
      - name: pre-upgrade-resources
        version: v1.0.0
        inline:
          handler: EnsurePreUpgradeResources
          version: v1.0.0
      - name: bkeagent
        version: v25.12.0
        inline:
          handler: EnsureAgentUpgrade
          version: v1.0.0
      - name: kubernetes-master
        version: v1.25.0
        inline:
          handler: EnsureMasterUpgrade
          version: v1.0.0
      - name: kubernetes-worker
        version: v1.25.0
        inline:
          handler: EnsureWorkerUpgrade
          version: v1.0.0
```

#### 3.3.2 26.03 ReleaseImage 构建

```yaml
# ReleaseImage for 26.03
apiVersion: config.openfuyao.cn/v1alpha1
kind: ReleaseImage
metadata:
  name: openfuyao-v26.03
spec:
  version: "26.03"
  
  install:
    components:
      # 基础组件 (PhaseFlow 方式)
      - name: bkeagent
        version: v26.03.0
      - name: nodes-env
        version: v1.0.0
      - name: cluster-api-obj
        version: v1.0.0
      - name: certs
        version: v1.0.0
      - name: load-balance
        version: v1.0.0
      - name: kubernetes-master
        version: v1.26.0
      - name: kubernetes-worker
        version: v1.26.0
      - name: kube-proxy
        version: v1.26.0
      - name: coredns
        version: v1.10.0
      - name: nodes-postprocess
        version: v1.0.0
      - name: agent-switch
        version: v1.0.0
  
  upgrade:
    components:
      # 升级组件 (PhaseFlow 方式)
      - name: pre-upgrade-resources
        version: v1.0.0
        inline:
          handler: EnsurePreUpgradeResources
          version: v1.0.0
      - name: bkeagent
        version: v26.03.0
        inline:
          handler: EnsureAgentUpgrade
          version: v1.0.0
      - name: kubernetes-master
        version: v1.26.0
        inline:
          handler: EnsureMasterUpgrade
          version: v1.0.0
      - name: kubernetes-worker
        version: v1.26.0
        inline:
          handler: EnsureWorkerUpgrade
          version: v1.0.0
```

#### 3.3.3 26.06 及之后版本 ReleaseImage (声明式升级)

```yaml
# ReleaseImage for 26.06
apiVersion: config.openfuyao.cn/v1alpha1
kind: ReleaseImage
metadata:
  name: openfuyao-v26.06
spec:
  version: "26.06"
  
  install:
    components:
      # 基础组件 (声明式升级方式)
      - name: bkeagent
        version: v26.06.0
        inline:
          handler: EnsureBKEAgent
          version: v1.0.0
      - name: nodes-env
        version: v1.0.0
        inline:
          handler: EnsureNodesEnv
          version: v1.0.0
      - name: cluster-api-obj
        version: v1.0.0
        inline:
          handler: EnsureClusterAPIObj
          version: v1.0.0
      - name: certs
        version: v1.0.0
        inline:
          handler: EnsureCerts
          version: v1.0.0
      - name: load-balance
        version: v1.0.0
        inline:
          handler: EnsureLoadBalance
          version: v1.0.0
      - name: kubernetes-master
        version: v1.26.0
        inline:
          handler: EnsureMasterInit
          version: v1.0.0
      - name: kubernetes-worker
        version: v1.26.0
        inline:
          handler: EnsureWorkerJoin
          version: v1.0.0
      - name: kube-proxy
        version: v1.26.0
      - name: coredns
        version: v1.11.0
      - name: nodes-postprocess
        version: v1.0.0
        inline:
          handler: EnsureNodesPostProcess
          version: v1.0.0
      - name: agent-switch
        version: v1.0.0
        inline:
          handler: EnsureAgentSwitch
          version: v1.0.0
  
  upgrade:
    components:
      # 升级组件 (声明式升级方式)
      - name: pre-upgrade-resources
        version: v1.0.0
        inline:
          handler: EnsurePreUpgradeResources
          version: v1.0.0
      - name: bkeagent
        version: v26.06.0
        inline:
          handler: EnsureAgentUpgrade
          version: v1.0.0
      - name: kubernetes-master
        version: v1.26.0
        inline:
          handler: EnsureMasterUpgrade
          version: v1.0.0
      - name: kubernetes-worker
        version: v1.26.0
        inline:
          handler: EnsureWorkerUpgrade
          version: v1.0.0
```

### 3.4 多 Hop 升级流程

#### 3.4.1 升级路径定义

```yaml
# UpgradePath for LTS upgrade
apiVersion: config.openfuyao.cn/v1alpha1
kind: UpgradePath
metadata:
  name: lts-upgrade-path
spec:
  versions:
    - version: "25.12"
      lts: true
    - version: "26.03"
    - version: "26.06"
    - version: "26.09"
    - version: "26.12"
      lts: true
  
  paths:
    - from: "25.12"
      to: "26.03"
      preprocessing: true
      preprocessingSteps:
        - name: CRD 迁移
        - name: API 兼容性处理
        - name: 数据迁移
        - name: 配置迁移
    - from: "26.03"
      to: "26.06"
      preprocessing: true
      preprocessingSteps:
        - name: 声明式升级框架引入
        - name: ReleaseImage 构建
        - name: ExecutorRegistry 扩展
    - from: "26.06"
      to: "26.09"
      preprocessing: false
    - from: "26.09"
      to: "26.12"
      preprocessing: false
```

#### 3.4.2 升级流程

```
Step 1: 25.12(LTS) -> 26.03 (预处理)
  1. 执行预处理步骤:
     - CRD 迁移
     - API 兼容性处理
     - 数据迁移
     - 配置迁移
  2. 使用 PhaseFlow 升级到 26.03
  3. 验证升级结果

Step 2: 26.03 -> 26.06 (预处理)
  1. 执行预处理步骤:
     - 声明式升级框架引入
     - ReleaseImage 构建
     - ExecutorRegistry 扩展
  2. 使用 PhaseFlow 升级到 26.06
  3. 验证升级结果

Step 3: 26.06 -> 26.09 (声明式升级)
  1. 使用声明式升级方案
  2. 支持多 Hop 升级
  3. 支持断点续传
  4. 验证升级结果

Step 4: 26.09 -> 26.12(LTS) (声明式升级)
  1. 使用声明式升级方案
  2. 支持多 Hop 升级
  3. 支持断点续传
  4. 验证升级结果
```

### 3.5 管理集群升级策略

#### 3.5.1 问题描述

25.12(LTS) 管理集群需要先升级以支持 26.06 的声明式升级方案。这意味着：
1. 管理集群需要先升级到 26.03
2. 然后升级到 26.06（支持声明式升级）
3. 最后才能使用声明式升级方案升级工作负载集群

#### 3.5.2 升级顺序

```
管理集群升级:
  25.12(LTS) --[预处理]--> 26.03 --[预处理]--> 26.06
  
工作负载集群升级:
  25.12(LTS) --[预处理]--> 26.03 --[预处理]--> 26.06 --> 26.09 --> 26.12(LTS)
```

#### 3.5.3 升级策略

1. **阶段 1**: 升级管理集群到 26.06
   - 25.12(LTS) -> 26.03 (预处理)
   - 26.03 -> 26.06 (预处理)

2. **阶段 2**: 升级工作负载集群
   - 25.12(LTS) -> 26.03 (预处理)
   - 26.03 -> 26.06 (预处理)
   - 26.06 -> 26.09 (声明式升级)
   - 26.09 -> 26.12(LTS) (声明式升级)

### 3.6 预处理步骤详解

#### 3.6.1 25.12(LTS) -> 26.03 预处理

**CRD 迁移**:
```bash
# 备份现有 CRD
kubectl get crd -o yaml > crd-backup-25.12.yaml

# 升级 CRD
kubectl apply -f crd-update-26.03.yaml

# 验证 CRD 升级
kubectl get crd -o yaml > crd-verify-26.03.yaml
```

**API 兼容性处理**:
```bash
# 检查 API 兼容性
kubectl api-resources --api-group=config.openfuyao.cn

# 应用 API 迁移脚本
kubectl apply -f api-migration-26.03.yaml
```

**数据迁移**:
```bash
# 备份数据
kubectl get bkecluster -o yaml > bkecluster-backup-25.12.yaml

# 应用数据迁移脚本
kubectl apply -f data-migration-26.03.yaml

# 验证数据迁移
kubectl get bkecluster -o yaml > bkecluster-verify-26.03.yaml
```

**配置迁移**:
```bash
# 备份配置
kubectl get configmap -n bke-system -o yaml > config-backup-25.12.yaml

# 应用配置迁移脚本
kubectl apply -f config-migration-26.03.yaml

# 验证配置迁移
kubectl get configmap -n bke-system -o yaml > config-verify-26.03.yaml
```

#### 3.6.2 26.03 -> 26.06 预处理

**声明式升级框架引入**:
```bash
# 部署 DAG 调度器
kubectl apply -f dag-scheduler-26.06.yaml

# 部署执行器注册表
kubectl apply -f executor-registry-26.06.yaml

# 验证框架部署
kubectl get deployment -n bke-system | grep dag-scheduler
```

**ReleaseImage 构建**:
```bash
# 构建 26.06 ReleaseImage
opm render openfuyao-v26.06 > release-image-26.06.yaml

# 验证 ReleaseImage
kubectl apply -f release-image-26.06.yaml
kubectl get releaseimage openfuyao-v26.06
```

**ExecutorRegistry 扩展**:
```bash
# 扩展执行器注册表
kubectl apply -f executor-extension-26.06.yaml

# 验证执行器注册
kubectl get executorregistry -n bke-system
```

---

## 4. 升级执行计划

### 4.1 升级时间表

| 阶段 | 版本 | 升级方式 | 预处理 | 预计时间 |
|------|------|---------|--------|---------|
| 1 | 25.12(LTS) -> 26.03 | PhaseFlow | 是 | 2 周 |
| 2 | 26.03 -> 26.06 | PhaseFlow | 是 | 2 周 |
| 3 | 26.06 -> 26.09 | 声明式升级 | 否 | 1 周 |
| 4 | 26.09 -> 26.12(LTS) | 声明式升级 | 否 | 1 周 |

### 4.2 升级检查清单

#### 4.2.1 阶段 1: 25.12(LTS) -> 26.03

- [ ] 备份管理集群数据
- [ ] 执行 CRD 迁移
- [ ] 执行 API 兼容性处理
- [ ] 执行数据迁移
- [ ] 执行配置迁移
- [ ] 验证管理集群升级
- [ ] 备份工作负载集群数据
- [ ] 执行工作负载集群升级
- [ ] 验证工作负载集群升级

#### 4.2.2 阶段 2: 26.03 -> 26.06

- [ ] 部署声明式升级框架
- [ ] 构建 26.06 ReleaseImage
- [ ] 扩展 ExecutorRegistry
- [ ] 验证框架部署
- [ ] 升级管理集群到 26.06
- [ ] 验证管理集群升级
- [ ] 升级工作负载集群到 26.06
- [ ] 验证工作负载集群升级

#### 4.2.3 阶段 3: 26.06 -> 26.09

- [ ] 使用声明式升级方案升级管理集群
- [ ] 验证管理集群升级
- [ ] 使用声明式升级方案升级工作负载集群
- [ ] 验证工作负载集群升级

#### 4.2.4 阶段 4: 26.09 -> 26.12(LTS)

- [ ] 使用声明式升级方案升级管理集群
- [ ] 验证管理集群升级
- [ ] 使用声明式升级方案升级工作负载集群
- [ ] 验证工作负载集群升级

---

## 5. 风险与缓解

### 5.1 风险评估

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **预处理失败** | 升级失败 | 中 | 详细测试预处理步骤，准备回滚方案 |
| **数据丢失** | 业务中断 | 低 | 完整备份，测试恢复流程 |
| **兼容性问题** | 升级失败 | 中 | 充分测试，准备兼容性补丁 |
| **性能问题** | 性能下降 | 低 | 性能测试，准备优化方案 |
| **时间延误** | 项目延期 | 中 | 预留缓冲时间，准备应急预案 |

### 5.2 缓解措施

1. **备份策略**: 在每个阶段前完整备份集群数据
2. **测试策略**: 在测试环境充分验证每个升级步骤
3. **回滚策略**: 准备每个阶段的回滚方案
4. **监控策略**: 升级过程中持续监控集群状态
5. **应急预案**: 准备应急预案，处理意外情况

---

## 6. 测试策略

### 6.1 测试环境

| 环境 | 用途 | 配置 |
|------|------|------|
| 开发环境 | 开发测试 | 单节点集群 |
| 测试环境 | 集成测试 | 3 节点集群 |
| 预生产环境 | 预生产测试 | 生产环境配置 |
| 生产环境 | 生产环境 | 生产环境配置 |

### 6.2 测试用例

#### 6.2.1 预处理测试

| 测试用例 | 描述 | 预期结果 |
|---------|------|---------|
| CRD 迁移测试 | 测试 CRD 迁移脚本 | CRD 成功迁移 |
| API 兼容性测试 | 测试 API 兼容性处理 | API 兼容性问题解决 |
| 数据迁移测试 | 测试数据迁移脚本 | 数据成功迁移 |
| 配置迁移测试 | 测试配置迁移脚本 | 配置成功迁移 |

#### 6.2.2 升级测试

| 测试用例 | 描述 | 预期结果 |
|---------|------|---------|
| 25.12 -> 26.03 升级 | 测试 PhaseFlow 升级 | 升级成功 |
| 26.03 -> 26.06 升级 | 测试 PhaseFlow 升级 | 升级成功 |
| 26.06 -> 26.09 升级 | 测试声明式升级 | 升级成功 |
| 26.09 -> 26.12 升级 | 测试声明式升级 | 升级成功 |

#### 6.2.3 回滚测试

| 测试用例 | 描述 | 预期结果 |
|---------|------|---------|
| 25.12 -> 26.03 回滚 | 测试回滚流程 | 回滚成功 |
| 26.03 -> 26.06 回滚 | 测试回滚流程 | 回滚成功 |
| 26.06 -> 26.09 回滚 | 测试回滚流程 | 回滚成功 |
| 26.09 -> 26.12 回滚 | 测试回滚流程 | 回滚成功 |

---

## 7. 工作量估算

### 7.1 工作量分解

| 任务 | 工作量 | 说明 |
|------|--------|------|
| **预处理开发** | 4 周 | 开发预处理脚本和工具 |
| **ReleaseImage 构建** | 2 周 | 构建 25.12 和 26.03 ReleaseImage |
| **测试** | 4 周 | 测试环境和预生产环境测试 |
| **升级执行** | 4 周 | 生产环境升级执行 |
| **回滚准备** | 2 周 | 准备回滚方案和工具 |
| **文档** | 2 周 | 编写升级文档和操作手册 |
| **总计** | 18 周 | 约 4.5 个月 |

### 7.2 人力资源

| 角色 | 人数 | 职责 |
|------|------|------|
| 架构师 | 1 | 架构设计和方案评审 |
| 开发工程师 | 2 | 预处理开发和测试 |
| 测试工程师 | 2 | 测试用例设计和执行 |
| 运维工程师 | 2 | 升级执行和监控 |
| 项目经理 | 1 | 项目管理和协调 |

---

## 8. 附录

### 8.1 参考文档

- KEP-6: 声明式升级框架
- KEP-10: ReleaseImage 安装组件声明式定义
- OpenShift CVO 架构分析
- BKE CVO 架构分析

### 8.2 术语表

| 术语 | 定义 |
|------|------|
| **LTS** | Long Term Support，长期支持版本 |
| **PhaseFlow** | 传统升级方式，基于硬编码的 Phase 列表 |
| **声明式升级** | 基于 DAG 的升级方式，支持多 Hop 和断点续传 |
| **预处理** | 升级前的准备工作，包括 CRD 迁移、API 兼容性处理等 |
| **ReleaseImage** | 发布镜像，包含组件版本和升级信息 |
| **多 Hop 升级** | 通过多个中间版本进行升级 |
| **断点续传** | 升级中断后可以从断点继续升级 |

---

**文档版本**: v1.0
**维护者**: openFuyao Team
