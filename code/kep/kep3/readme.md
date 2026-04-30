# 旧框架整改
```go
CommonPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
        NewEnsureFinalizer,
        NewEnsurePaused,
        NewEnsureClusterManage,
        NewEnsureDeleteOrReset,
        NewEnsureDryRun,
    }
    
    DeployPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
        NewEnsureBKEAgent,
        NewEnsureNodesEnv,
        NewEnsureClusterAPIObj,
        NewEnsureCerts,
        NewEnsureLoadBalance,
        NewEnsureMasterInit,
        NewEnsureMasterJoin,
        NewEnsureWorkerJoin,
        NewEnsureAddonDeploy,
        NewEnsureNodesPostProcess,
        NewEnsureAgentSwitch,
    }
    
    PostDeployPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
        NewEnsureProviderSelfUpgrade,
        NewEnsureAgentUpgrade,
        NewEnsureContainerdUpgrade,
        NewEnsureEtcdUpgrade,
        NewEnsureWorkerUpgrade,
        NewEnsureMasterUpgrade,
        NewEnsureWorkerDelete,
        NewEnsureMasterDelete,
        NewEnsureComponentUpgrade,
        NewEnsureCluster,
    }
    
    DeletePhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
        NewEnsurePaused,
        NewEnsureDeleteOrReset,
    }
```
分析https://github.com/javadoors/cluster-api-provider-bke，各phase支持不同版本升级的方案，即各phase里安装的组件要能够卸载老版本的组件，同时安装新版本的组件（组件间版本可能变化）
1. 各phase里的组件信息要外围到版本安装包中，并提供对应的安装、卸载、升级、健康检查与兼容性检查能力
2. 组件信息要封装成builtin/k8s/shell可以执行的声明式安装，同时分析是否需要支持其它类型的执行方式
3. 给出各个phase整改的完整样例
4. 给出有状态phase的整改样例与方案（如etcd）
5. 给出整个工程整改的工作量
## 概要
将各 phase 的组件管理改造为 **外部化的版本安装包 + 声明式组件描述**，并通过统一执行层（**builtin / k8s / shell / container / remote**）执行安装、卸载、升级、健康检查与兼容性校验，可以实现按 phase 的细粒度版本升级与安全回滚。下面给出：组件描述规范、执行模型、每个 phase 的整改完整样例、针对有状态组件（etcd）的专门方案，以及工程整改工作量估算与迁移建议。
## 组件描述规范 Component Descriptor
**目标**：把组件信息从代码中剥离，放入可分发的版本包（phase package）。每个组件提供安装、卸载、升级、健康检查与兼容性声明。
### 关键字段（YAML Schema 摘要）
```yaml
apiVersion: bke.io/v1alpha1
kind: ComponentDescriptor
metadata:
  name: lxcfs
spec:
  version: "2.0.9"
  type: "package" # package | binary | manifest | helm | script | container
  artifacts:
    - url: "https://repo.example.com/lxcfs_2.0.9.deb"
      sha256: "..."
  install: |
    dpkg -i /tmp/lxcfs_2.0.9.deb
  uninstall: |
    apt-get remove -y lxcfs
  upgrade:
    strategy: "uninstall-install" # uninstall-install | inplace | rolling | kubeadm-sequence
    pre: "systemctl stop lxcfs || true"
    post: "systemctl start lxcfs"
  healthcheck:
    cmd: "systemctl is-active lxcfs"
    success: "active"
    timeoutSeconds: 30
  compatibility:
    kubelet: ">=1.25 <1.28"
    etcd: ">=3.5"
  hooks:
    preInstall: "scripts/pre_install.sh"
    postInstall: "scripts/post_install.sh"
```
### Phase Manifest（示例）
```yaml
phase: EnsureNodesEnv
version: "v1.2.0"
components:
  - ref: lxcfs@2.0.9
  - ref: nfs-utils@1.3.0
  - ref: etcdctl@3.5.9
```
### 执行模型 与 执行器类型
**目标**：统一执行接口，支持多种执行后端并保证幂等性、可观测性与回滚能力。
### 执行器类型
- **builtin**：Agent 内置操作（apt/yum、systemctl、文件操作）。适合节点本地修改。  
- **k8s**：通过 Kubernetes API 或 kubectl/helm 操作集群资源。适合 manifest/helm 类型组件。  
- **shell**：直接执行脚本片段或远程脚本。适合一次性复杂逻辑。  
- **container**：以镜像运行安装逻辑（可用 chroot 或挂载 /host），适合复杂依赖或隔离需求。  
- **remote**：通过 SSH 或远程执行（可选，用于无 Agent 场景）。
### 统一接口（伪 Go）
```go
type ComponentExecutor interface {
  Install(ctx context.Context, desc ComponentDescriptor) error
  Uninstall(ctx context.Context, desc ComponentDescriptor) error
  Upgrade(ctx context.Context, from, to ComponentDescriptor) error
  HealthCheck(ctx context.Context, desc ComponentDescriptor) (HealthStatus, error)
  CheckCompatibility(ctx context.Context, desc ComponentDescriptor, clusterState ClusterState) (bool, []string)
}
```
### 执行原则
- **幂等**：install/uninstall/upgrade 脚本必须可重复执行且安全。  
- **事务感**：升级前创建回滚点（版本记录、关键文件快照、systemd 单元备份）。  
- **分级健康检查**：进程级、功能级、业务级。升级必须通过必需级别。  
- **兼容性校验**：在执行前读取兼容性矩阵并校验全局一致性。  
- **并发与顺序**：跨节点可并发执行；同一节点内组件按 manifest 顺序或策略执行。
## 各 phase 的整改完整样例
下面以你列出的 phase 集合给出整改样例，包含 manifest、Agent 执行伪逻辑与回滚策略。
### CommonPhases 改造样例
**目标**：Finalizer、Paused、ClusterManage、DeleteOrReset、DryRun 保持控制流不变，但在 DeleteOrReset 中使用 descriptor 的 uninstall。

**示例 DeleteOrReset 片段**
```yaml
phase: DeleteOrReset
components:
  - ref: kubelet@1.26.3
  - ref: containerd@1.6.20
```
**Agent 伪逻辑**
- 解析 manifest → 逆序调用 executor.Uninstall → 健康检查确认移除 → 更新 CRD 状态。
### DeployPhases 改造样例
**关键点**：BKEAgent、NodesEnv、ClusterAPIObj、Certs、LoadBalance、MasterInit/Join、WorkerJoin、AddonDeploy、NodesPostProcess、AgentSwitch。

**示例 EnsureNodesEnv Manifest**
```yaml
phase: EnsureNodesEnv
version: "v1.2.0"
components:
  - name: lxcfs
    version: "2.0.9"
    type: package
    artifacts: [...]
    install: ...
    uninstall: ...
    healthcheck: ...
  - name: nfs-utils
    version: "1.3.0"
    ...
```
**Agent 执行伪逻辑**
1. 下载 phase package。  
2. for comp in manifest.components:
   - `ok, reasons := executor.CheckCompatibility(comp, clusterState)`
   - if not ok → fail phase with reasons
   - `cur := queryInstalledVersion(comp.name)`
   - if cur == comp.version → continue
   - `backup := snapshotFiles(comp)`; record CRD rollback point
   - `err := executor.Upgrade(ctx, curDesc, comp)`
   - if err → `rollback(backup)` and mark phase failed
   - `executor.HealthCheck(comp)` → if fail → rollback

**MasterInit 特殊策略**
- Descriptor 指定 `upgrade.strategy: kubeadm-sequence`。  
- 执行顺序：升级 kubeadm → run `kubeadm upgrade apply` → 升级 kubelet → 重启 kubelet → 健康检查 API Server。
### PostDeployPhases 改造样例
**关键点**：ProviderSelfUpgrade、AgentUpgrade、ContainerdUpgrade、EtcdUpgrade、Worker/Master Upgrade、ComponentUpgrade。

**示例 ContainerdUpgrade**
```yaml
component:
  name: containerd
  version: "1.6.20"
  upgrade:
    strategy: rolling
    pre: "systemctl stop containerd || true"
    post: "systemctl start containerd"
  healthcheck:
    cmd: "ctr version"
```
**执行策略**
- 采用滚动升级：逐节点 drain → 升级 containerd → uncordon → 验证 Pod 调度。
### DeletePhases 改造样例
- 逆序卸载 manifest 中组件，执行依赖检查，清理残留配置，更新 CRD。
## 有状态 Phase 的整改样例 与 专门方案 Etcd
有状态组件（etcd）需要更严格的升级策略、备份与回滚流程。
### Etcd 升级关键原则
- **数据备份**：升级前必须做快照并验证快照可恢复。  
- **Leader 管理**：在升级过程中避免同时重启多数节点，保证 quorum。  
- **滚动升级**：单节点逐步升级并验证集群健康。  
- **兼容性校验**：etcd 版本与 API Server、kubelet 的兼容性必须通过矩阵校验。  
- **回滚策略**：若升级失败，使用快照恢复或回滚二进制版本并重启节点。
### Etcd Phase Manifest 示例
```yaml
phase: EnsureEtcdUpgrade
components:
  - name: etcd
    version: "3.5.9"
    type: binary
    artifacts:
      - url: "https://repo.example.com/etcd-v3.5.9-linux-amd64.tar.gz"
    upgrade:
      strategy: rolling
      pre: "etcdctl snapshot save /tmp/etcd-snap.db"
      post: "etcdctl endpoint status --write-out=table"
    healthcheck:
      cmd: "etcdctl endpoint health"
      success: "healthy"
    compatibility:
      kubeadm: ">=1.25"
```
### Etcd 升级流程（逐节点）
1. 在控制器或 Agent 上触发：`pre` hook 做 snapshot 并上传到安全存储。  
2. 选择一个 follower 节点：cordon → drain → stop etcd → replace binary → start etcd → wait health.  
3. 验证集群健康与数据一致性。  
4. 重复直到所有节点升级完成。  
5. 若任一步失败：停止升级，触发回滚流程（恢复快照或重装旧二进制并重启），并通知运维。
## 回滚、状态记录 与 兼容性矩阵
- **CRD 扩展**：在 Cluster 或 MachineStatus 中记录每个组件的 `currentVersion`, `targetVersion`, `lastOperation`, `healthStatus`, `rollbackPoint`.  
- **回滚点**：包含二进制版本、配置文件快照、systemd 单元备份、etcd snapshot URL。  
- **兼容性矩阵**：集中维护并可随版本包更新；控制器在下发 phase 前做全局校验，Agent 在本地做最终校验。  
- **日志与可观测**：每个组件操作产生日志、事件与指标（duration、success/fail、health checks）。
## 工程整改工作量 与 里程碑
下面给出分项工作与估算（单团队并行度中等，含开发、测试、文档）。估算以人周计，整体约 12–24 人周，分阶段并行可压缩。

| 工作项 | 说明 | 估算人周 |
|--------|------|---------|
| Schema 与 包格式设计 | 定义 ComponentDescriptor、PhaseManifest、兼容性矩阵 | 1–2 |
| Agent 执行框架改造 | 实现 ComponentExecutor 接口、job 工厂扩展、container job 支持 | 3–5 |
| builtin/k8s/shell 适配 | 将现有 builtin job 改为 descriptor 驱动，增加 health/compat checks | 3–4 |
| Phase 改造与集成 | 改造 DeployPhases、PostDeployPhases、CommonPhases、DeletePhases | 3–5 |
| 有状态组件支持 | etcd 升级策略、备份/恢复、滚动升级实现与测试 | 2–4 |
| 回滚与状态管理 | CRD 扩展、回滚实现、状态上报 | 1–2 |
| 测试与验证 | 单元测试、集成测试、升级回滚演练、兼容性矩阵测试 | 2–4 |
| 文档与迁移指南 | 使用手册、descriptor 编写指南、运维流程 | 1–2 |

**总体估算**：约 **12–24 人周**（2–4 个月单人推进，或 4–8 周并行小团队）。
## 风险、优先级与落地建议
- **高风险点**：etcd 与 control-plane 升级失败、回滚复杂度、特权操作安全边界。  
- **优先级建议**：  
  1. 先实现 descriptor schema 与 Agent 执行框架（最小可行产品）。  
  2. 在 **EnsureNodesEnv** 做试点，验证安装/升级/回滚流程。  
  3. 推广到 ControlPlane（kubeadm/kubelet）并严格测试兼容性矩阵。  
  4. 最后覆盖有状态组件（etcd）并做灾难恢复演练。  
- **测试矩阵**：必须建立自动化升级回滚矩阵，覆盖常见版本组合与失败场景。  
- **安全**：对 container job 与特权操作做严格审计与最小权限控制。
### 结论
把各 phase 的组件管理改造成 **外部化的版本安装包 + 声明式 ComponentDescriptor**，并通过统一执行层执行安装/卸载/升级/健康/兼容性检查，是可行且可维护的长期方案。短期建议先做 schema 与 Agent 执行框架，并在 EnsureNodesEnv 试点，逐步扩展到 control-plane 与有状态组件（etcd）。我可以把上面的 schema、示例 manifest、Agent 执行伪代码和一个迁移路线图整理成一个可交付的设计文档，或者把某个 phase（例如 EnsureNodesEnv 或 EnsureEtcdUpgrade）做成详细实现样例代码，你想先要哪一个交付物。

# etcd改造
## 可行性评估
将 **etcd** 的升级从当前代码库改造为基于 **Phase Manifest + ComponentDescriptor** 的逐节点滚动升级是可行的。关键可行点：  
- etcd 升级本质上是**有状态服务的滚动替换**，只要保证 **快照备份、单节点升级、健康校验、quorum 保持**，即可实现无数据丢失的升级。  
- 需要在 Agent 与控制器之间增加 **描述文件分发、状态上报、回滚点记录** 与 **兼容性校验**。  
- 最大风险来自兼容性矩阵不全、快照恢复流程未演练、以及并发重启导致 quorum 丢失。通过严格的前置检查与自动化回滚可以把风险降到可接受范围。
## 前置条件 与 风险控制
**前置条件**（必须满足）  
- 每个节点可执行 `etcdctl` 并访问 etcd 集群证书与端点。  
- 集群有稳定的备份存储（S3/NFS/本地路径），并能上传/下载快照。  
- Agent 能以足够权限执行二进制替换、systemd 操作、文件备份与恢复。  
- 兼容性矩阵可用，控制器能校验目标 etcd 版本与 Kubernetes 版本兼容性。

**风险控制措施**  
- **强制快照**：升级前必须成功生成并验证快照。  
- **单节点滚动**：一次只升级一个 etcd 成员，确保 quorum 始终满足。  
- **健康门控**：每步升级后执行健康检查（`etcdctl endpoint health`、leader 检查、成员列表一致性）。  
- **回滚点**：记录旧二进制版本、配置、systemd 单元与快照 URL。  
- **超时与中断策略**：若某节点在指定超时内未恢复，停止升级并触发回滚或人工介入。
## 逐节点升级的具体实现步骤
下面给出**可直接执行的逐节点升级步骤**，包含命令、校验点与失败处理。假设使用 systemd 管理 etcd，`etcdctl` 在控制节点可用，证书路径为 `/etc/kubernetes/pki/etcd`，快照存储为 `/var/backups/etcd`（可替换为 S3）。
### 1. 全局前置检查（在控制器或 Operator 上执行）
```bash
# 检查兼容性矩阵（伪命令，实际由控制器实现）
controller.checkCompatibility(target_etcd_version, cluster_k8s_version)

# 检查集群健康
ETCD_ENDPOINTS=$(kubectl -n kube-system get endpoints etcd -o jsonpath='{.subsets[*].addresses[*].ip}')
for ep in $ETCD_ENDPOINTS; do
  ETCDCTL_API=3 etcdctl --endpoints=$ep:2379 --cacert=/etc/kubernetes/pki/etcd/ca.crt \
    --cert=/etc/kubernetes/pki/etcd/peer.crt --key=/etc/kubernetes/pki/etcd/peer.key endpoint health || exit 1
done
```
### 2. 备份所有节点 etcd 数据（必须成功）
```bash
SNAPSHOT=/var/backups/etcd-snap-$(date +%s).db
ETCDCTL_API=3 etcdctl snapshot save $SNAPSHOT \
  --cacert=/etc/kubernetes/pki/etcd/ca.crt --cert=/etc/kubernetes/pki/etcd/peer.crt \
  --key=/etc/kubernetes/pki/etcd/peer.key
# 验证快照
ETCDCTL_API=3 etcdctl snapshot status $SNAPSHOT
# 上传到中央存储（示例）
cp $SNAPSHOT /var/backups/etcd/ || exit 1
```
记录快照 URL 到 CRD 的 rollbackPoint 字段。
### 3. 选择升级顺序
- 优先升级 follower 节点，最后升级 leader。  
- 获取成员列表与 leader：
```bash
ETCDCTL_API=3 etcdctl --endpoints=$ETCD_ENDPOINTS member list
ETCDCTL_API=3 etcdctl --endpoints=$ETCD_ENDPOINTS endpoint status --write-out=table
# 找到 leader id/ip
```
### 4. 单节点升级流程（对每个 member，按顺序执行）
假设节点 IP 为 `NODE_IP`，旧版本 `OLD_VER`，目标版本 `NEW_VER`，二进制包 URL 已下发到节点 `/tmp/etcd-v${NEW_VER}.tar.gz`。
##### 4.1 在控制器标记节点为升级中并记录回滚点
写入 CRD：`etcd/member-NODE_IP: upgrading -> from OLD_VER to NEW_VER, snapshot: <url>`
#### 4.2 Cordon 与 Drain（如果该节点运行 kubelet）
```bash
kubectl cordon <node-name>
kubectl drain <node-name> --ignore-daemonsets --delete-local-data
```
#### 4.3 备份本地 etcd 配置与二进制
```bash
ssh root@NODE_IP "mkdir -p /var/backups/etcd/node-backup-$(date +%s) && \
  cp /usr/local/bin/etcd /usr/local/bin/etcd.old && \
  cp -r /etc/etcd /var/backups/etcd/node-backup-$(date +%s)/"
```
#### 4.4 停止 etcd 服务并确认停止
```bash
ssh root@NODE_IP "systemctl stop etcd && systemctl status etcd --no-pager"
```
#### 4.5 替换二进制并校验
```bash
# 在节点上
ssh root@NODE_IP "tar xzf /tmp/etcd-v${NEW_VER}.tar.gz -C /tmp && \
  cp /tmp/etcd-v${NEW_VER}/etcd /usr/local/bin/etcd && \
  cp /tmp/etcd-v${NEW_VER}/etcdctl /usr/local/bin/etcdctl && \
  chmod +x /usr/local/bin/etcd*"
# 校验版本
ssh root@NODE_IP "etcd --version && etcdctl version"
```
#### 4.6 启动 etcd 并等待健康
```bash
ssh root@NODE_IP "systemctl daemon-reload && systemctl start etcd"
# 等待健康，超时处理
for i in {1..30}; do
  ssh root@NODE_IP "ETCDCTL_API=3 etcdctl --endpoints=127.0.0.1:2379 \
    --cacert=/etc/kubernetes/pki/etcd/ca.crt --cert=/etc/kubernetes/pki/etcd/server.crt \
    --key=/etc/kubernetes/pki/etcd/server.key endpoint health" && break
  sleep 10
done
# 若超时未健康，触发回滚
```
#### 4.7 集群级健康校验
在控制器上再次检查整个集群：
```bash
ETCDCTL_API=3 etcdctl --endpoints=$ETCD_ENDPOINTS endpoint status --write-out=table
ETCDCTL_API=3 etcdctl --endpoints=$ETCD_ENDPOINTS endpoint health
# 检查成员数量与 quorum
```
#### 4.8 解除 Drain 与 Cordon
```bash
kubectl uncordon <node-name>
```
#### 4.9 标记节点升级成功并记录版本
更新 CRD：`etcd/member-NODE_IP: succeeded -> version NEW_VER`
#### 4.10 若失败则回滚（见回滚策略）
### 5. 升级 leader 节点
- 在升级 leader 前，**优先触发 leader 转移**到其他节点（`etcdctl` 不直接提供强制转移命令，但可以通过停止 leader 让集群选举新 leader，或使用 `etcdctl move-leader` if supported）。  
- 然后按单节点流程升级 leader。
## 回滚与恢复策略
**回滚触发条件**：单节点升级后集群健康检查失败、quorum 丢失、或关键业务检查失败。  

**回滚步骤**（自动化脚本）  
1. 标记升级失败并停止后续节点升级。  
2. 对失败节点执行：停止当前 etcd，恢复旧二进制与配置（从 `/usr/local/bin/etcd.old` 或备份目录），启动服务。  
3. 若集群数据异常或 quorum 丢失，使用最近成功快照恢复整个集群：  
   - 在控制器上选择恢复节点，使用 `etcdctl snapshot restore` 恢复数据到指定节点，然后逐节点替换数据或重建集群成员。  
4. 更新 CRD 状态为 `rolled-back` 并附上失败原因与快照 URL。  

**示例回滚命令**
```bash
# 在失败节点
ssh root@NODE_IP "systemctl stop etcd && cp /usr/local/bin/etcd.old /usr/local/bin/etcd && systemctl start etcd"
# 若需要 snapshot 恢复（复杂场景）
ETCDCTL_API=3 etcdctl snapshot restore /var/backups/etcd/etcd-snap.db \
  --data-dir /var/lib/etcd-from-snap --name <node-name> --initial-cluster <...> --initial-cluster-token <token> --initial-advertise-peer-urls http://<node-ip>:2380
```
## 自动化实现建议与伪代码
在 Agent/Controller 中实现一个 **EtcdUpgradeController**，伪流程如下：
```go
func UpgradeEtcdCluster(manifest PhaseManifest) error {
  // 1. Pre-checks
  if !checkCompatibility(manifest) { return error }
  snapshot := takeAndUploadSnapshot()
  recordRollbackPoint(snapshot)

  members := getEtcdMembersOrderedByRole() // followers first, leader last
  for _, m := range members {
    markMemberUpgrading(m)
    if err := cordonAndDrainNode(m.Node); err != nil { triggerRollback(); return err }
    if err := backupNodeState(m.Node); err != nil { triggerRollback(); return err }
    if err := replaceEtcdBinary(m.Node, manifest.Component.Artifact); err != nil { triggerRollback(); return err }
    if err := startEtcdAndWaitHealthy(m.Node); err != nil { triggerRollback(); return err }
    if err := clusterHealthCheck(); err != nil { triggerRollback(); return err }
    uncordonNode(m.Node)
    markMemberSucceeded(m)
  }
  return nil
}
```
实现细节：并发控制、超时、重试次数、日志与事件上报、CRD 状态更新、告警集成。
## 验证测试矩阵 与 演练
必须建立自动化测试矩阵，至少包含：  
- 多种 etcd 版本组合（3.4→3.5、3.5→3.5.x）与 Kubernetes 版本组合。  
- 升级成功路径、单节点失败并回滚、leader 升级失败并恢复、快照恢复演练。  
- 灾难恢复演练：模拟多数节点失败后从快照恢复。  

每次变更都在 CI 环境中运行这些演练，确保升级脚本与回滚脚本可靠。
## 工程可行性结论 与 工作量估算
**可行性结论**：可行。通过分阶段实现（schema、Agent 执行器、etcd 专项流程、测试），可以把 etcd 升级改造为安全的逐节点滚动升级流程，支持回滚与兼容性校验。

**建议分阶段交付与估算（人周）**  
1. **设计与 schema**（ComponentDescriptor、PhaseManifest、CRD 扩展） — 1–2 人周  
2. **Agent 执行器扩展**（builtin/container job、exec/ssh hooks、状态上报） — 3–4 人周  
3. **Etcd 专项实现**（备份、逐节点升级逻辑、leader 处理、回滚） — 3–5 人周  
4. **测试与演练**（自动化矩阵、恢复演练、CI 集成） — 2–4 人周  
5. **文档与运维流程**（操作手册、故障处理） — 1 人周

**总计**：约 **10–16 人周**（可并行缩短）。若与其它 phase 一起改造，整体工程量会更大，但 etcd 专项可作为优先交付模块。
## 最后建议
- 先做 **最小可行实现**：实现 descriptor、Agent 执行器与 EnsureEtcdUpgrade 的逐节点脚本，在测试环境反复演练快照与回滚。  
- 强制把 **快照与恢复** 做为升级前的必需步骤。  
- 在生产推广前完成自动化测试矩阵与演练报告。  

# etcd升级
### 可行性评估
将 etcd 升级实现为 **可自动化的逐节点滚动升级** 是可行的。关键保障点是 **强制快照备份、单节点滚动、健康门控、回滚点记录** 与 **兼容性校验**。下面交付两部分可直接落地的产物：  
- **可执行 Bash 自动化脚本模板**（可在控制器节点或运维主机运行，通过 SSH 操作每个 etcd 节点）  
- **Controller 与 Agent 的实现样例代码**（Go，包含核心流程、状态更新与回滚触发点）
### Bash 自动化脚本模板 概览
**用途**：在控制器/运维主机上运行，按 manifest 指定的节点顺序逐节点升级 etcd。脚本假定 `etcdctl` 可在控制器上使用并能访问集群端点，节点可通过 SSH 无密码登录，目标二进制已下发到每个节点的 `/tmp/etcd-v${NEW_VER}.tar.gz`。

**主要特性**  
- 全局兼容性检查和集群快照备份  
- 按 follower-first、leader-last 顺序逐节点升级  
- 每节点：cordon/drain → 备份本地二进制与配置 → 停止服务 → 替换二进制 → 启动并等待健康 → 集群级健康校验 → uncordon  
- 自动回滚：单节点失败触发回滚；严重失败触发快照恢复流程（人工确认或自动）  
- 可配置超时、重试次数与快照存储路径

```bash
#!/usr/bin/env bash
set -euo pipefail

# ====== Configuration ======
# Controller environment must have etcdctl configured to talk to cluster
ETCDCTL_BIN=${ETCDCTL_BIN:-/usr/local/bin/etcdctl}
CA_CERT=${CA_CERT:-/etc/kubernetes/pki/etcd/ca.crt}
CERT=${CERT:-/etc/kubernetes/pki/etcd/peer.crt}
KEY=${KEY:-/etc/kubernetes/pki/etcd/peer.key}
SNAPSHOT_DIR=${SNAPSHOT_DIR:-/var/backups/etcd}
REMOTE_TMP=${REMOTE_TMP:-/tmp}
SSH_USER=${SSH_USER:-root}
SSH_OPTS=${SSH_OPTS:-"-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"}
DRY_RUN=${DRY_RUN:-false}
RETRY_COUNT=${RETRY_COUNT:-3}
HEALTH_WAIT_SECONDS=${HEALTH_WAIT_SECONDS:-300}
NODE_DRAIN_CMD=${NODE_DRAIN_CMD:-"kubectl drain %s --ignore-daemonsets --delete-local-data --force"}
NODE_UNCORDON_CMD=${NODE_UNCORDON_CMD:-"kubectl uncordon %s"}

# ====== Helper functions ======
log() { echo "$(date -Is) [INFO] $*"; }
err() { echo "$(date -Is) [ERROR] $*" >&2; }
run_ssh() { ssh ${SSH_OPTS} ${SSH_USER}@"$1" "$2"; }

# ====== Pre-checks ======
precheck() {
  log "Running pre-checks"
  if ! command -v "${ETCDCTL_BIN}" >/dev/null 2>&1; then
    err "etcdctl not found at ${ETCDCTL_BIN}"
    return 1
  fi
  # cluster health check
  if ! ${ETCDCTL_BIN} --cacert="${CA_CERT}" --cert="${CERT}" --key="${KEY}" endpoint health >/dev/null 2>&1; then
    err "Cluster health check failed"
    return 1
  fi
  mkdir -p "${SNAPSHOT_DIR}"
  return 0
}

# ====== Snapshot and upload ======
take_snapshot() {
  local snapfile="${SNAPSHOT_DIR}/etcd-snap-$(date +%s).db"
  log "Taking snapshot to ${snapfile}"
  ${ETCDCTL_BIN} snapshot save "${snapfile}" --cacert="${CA_CERT}" --cert="${CERT}" --key="${KEY}"
  ${ETCDCTL_BIN} snapshot status "${snapfile}"
  echo "${snapfile}"
}

# ====== Get member list and leader ======
get_members() {
  ${ETCDCTL_BIN} --cacert="${CA_CERT}" --cert="${CERT}" --key="${KEY}" member list -w json
}

get_endpoints() {
  ${ETCDCTL_BIN} --cacert="${CA_CERT}" --cert="${CERT}" --key="${KEY}" endpoint status -w json
}

# ====== Node upgrade flow ======
upgrade_node() {
  local node_ip="$1"
  local node_name="$2"
  local new_tar="$3"  # path on node, e.g. /tmp/etcd-v3.5.9.tar.gz
  local backup_dir="/var/backups/etcd/node-backup-$(date +%s)"
  log "Upgrading node ${node_name} (${node_ip}) to ${new_tar}"

  # 1. cordon and drain
  log "Cordoning and draining node ${node_name}"
  if [ "${DRY_RUN}" = "false" ]; then
    printf "${NODE_DRAIN_CMD}" "${node_name}" | bash -e
  fi

  # 2. backup local binary and config
  log "Backing up local etcd binary and config on ${node_ip}"
  run_ssh "${node_ip}" "mkdir -p ${backup_dir} && cp -a /usr/local/bin/etcd /usr/local/bin/etcd.old || true && cp -a /etc/etcd ${backup_dir}/ || true"

  # 3. stop etcd
  log "Stopping etcd on ${node_ip}"
  run_ssh "${node_ip}" "systemctl stop etcd || true; sleep 2"

  # 4. replace binary
  log "Replacing etcd binary on ${node_ip}"
  run_ssh "${node_ip}" "tar xzf ${new_tar} -C ${REMOTE_TMP} && cp ${REMOTE_TMP}/etcd*/etcd /usr/local/bin/ && cp ${REMOTE_TMP}/etcd*/etcdctl /usr/local/bin/ && chmod +x /usr/local/bin/etcd*"

  # 5. start etcd
  log "Starting etcd on ${node_ip}"
  run_ssh "${node_ip}" "systemctl daemon-reload || true; systemctl start etcd"

  # 6. wait for local health
  log "Waiting for local etcd health on ${node_ip}"
  local elapsed=0
  while [ "${elapsed}" -lt "${HEALTH_WAIT_SECONDS}" ]; do
    if run_ssh "${node_ip}" "ETCDCTL_API=3 /usr/local/bin/etcdctl --endpoints=127.0.0.1:2379 --cacert=${CA_CERT} --cert=${CERT} --key=${KEY} endpoint health" >/dev/null 2>&1; then
      log "Local etcd healthy on ${node_ip}"
      break
    fi
    sleep 5
    elapsed=$((elapsed+5))
  done
  if [ "${elapsed}" -ge "${HEALTH_WAIT_SECONDS}" ]; then
    err "Local etcd did not become healthy on ${node_ip} within ${HEALTH_WAIT_SECONDS}s"
    return 1
  fi

  # 7. cluster-level health check
  log "Checking cluster health after upgrading ${node_ip}"
  if ! ${ETCDCTL_BIN} --cacert="${CA_CERT}" --cert="${CERT}" --key="${KEY}" endpoint health >/dev/null 2>&1; then
    err "Cluster health check failed after upgrading ${node_ip}"
    return 1
  fi

  # 8. uncordon
  log "Uncordoning node ${node_name}"
  if [ "${DRY_RUN}" = "false" ]; then
    printf "${NODE_UNCORDON_CMD}" "${node_name}" | bash -e
  fi

  log "Node ${node_name} upgraded successfully"
  return 0
}

# ====== Rollback node ======
rollback_node() {
  local node_ip="$1"
  log "Rolling back node ${node_ip}"
  run_ssh "${node_ip}" "systemctl stop etcd || true; cp /usr/local/bin/etcd.old /usr/local/bin/etcd || true; systemctl start etcd || true"
  # optional: restore config from backup if needed
}

# ====== Main orchestration ======
main() {
  if ! precheck; then
    err "Precheck failed"
    exit 1
  fi

  local snapshot
  snapshot=$(take_snapshot)
  log "Snapshot created at ${snapshot}"

  # Example: members list must be provided or discovered
  # members format: "ip1:name1 ip2:name2 ip3:name3"
  # For production, parse etcd member list and map to node names
  MEMBERS=${MEMBERS:-"10.0.0.11:master-1 10.0.0.12:master-2 10.0.0.13:master-3"}
  NEW_TAR_PATH=${NEW_TAR_PATH:-/tmp/etcd-v3.5.9.tar.gz}

  # Determine leader and order: followers first, leader last
  # For simplicity assume provided order is safe; production should query member list and leader
  for pair in ${MEMBERS}; do
    node_ip="${pair%%:*}"
    node_name="${pair##*:}"
    log "Processing ${node_name} at ${node_ip}"
    attempt=0
    while [ $attempt -lt "${RETRY_COUNT}" ]; do
      if upgrade_node "${node_ip}" "${node_name}" "${NEW_TAR_PATH}"; then
        break
      else
        err "Upgrade attempt $((attempt+1)) failed for ${node_name}"
        attempt=$((attempt+1))
        sleep 5
      fi
    done
    if [ $attempt -ge "${RETRY_COUNT}" ]; then
      err "Exceeded retries for ${node_name}, initiating rollback"
      rollback_node "${node_ip}"
      err "Stopping upgrade process due to failure"
      exit 2
    fi
  done

  log "All nodes processed. Final cluster health check"
  if ! ${ETCDCTL_BIN} --cacert="${CA_CERT}" --cert="${CERT}" --key="${KEY}" endpoint health; then
    err "Cluster health check failed after full upgrade"
    exit 3
  fi

  log "Etcd upgrade completed successfully"
}

main "$@"
```
**使用说明**  
- 在运行前设置环境变量：`ETCDCTL_BIN`, `CA_CERT`, `CERT`, `KEY`, `SNAPSHOT_DIR`, `MEMBERS`, `NEW_TAR_PATH` 等。  
- 生产环境应把 `MEMBERS` 自动从 `etcdctl member list` 解析并按 follower-first 排序。  
- 强烈建议先在测试集群演练并将脚本纳入 CI。
### Controller 实现样例 概览
**目标**：在 Operator/Controller 层实现 `EtcdUpgradeController`，负责：下发 phase manifest、触发快照、按顺序调用节点升级、记录 CRD 状态、触发回滚。下面给出关键方法的 Go 伪实现（可直接在项目中改造）。
#### 关键结构体与接口
```go
package controllers

import (
  "context"
  "time"
  "fmt"
  "os/exec"
  corev1 "k8s.io/api/core/v1"
  metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
  "k8s.io/client-go/kubernetes"
  "sigs.k8s.io/controller-runtime/pkg/client"
)

type EtcdUpgradeController struct {
  kubeClient kubernetes.Interface
  ctrlClient client.Client
  etcdctlPath string
  caCert string
  cert string
  key string
  snapshotStore string
  sshUser string
  sshOpts []string
  logger Logger
}
```
#### 核心方法伪代码
```go
func (c *EtcdUpgradeController) StartUpgrade(ctx context.Context, manifest PhaseManifest) error {
  // 1. Compatibility check
  if ok, reasons := c.checkCompatibility(manifest); !ok {
    return fmt.Errorf("compatibility check failed: %v", reasons)
  }

  // 2. Take snapshot and record rollback point
  snapPath, err := c.takeSnapshot(ctx)
  if err != nil {
    return fmt.Errorf("snapshot failed: %w", err)
  }
  // persist snapshot info to CRD
  if err := c.recordRollbackPoint(ctx, manifest, snapPath); err != nil {
    return err
  }

  // 3. Get etcd members and order them (followers first)
  members, err := c.getEtcdMembers(ctx)
  if err != nil { return err }
  ordered := orderMembersFollowersFirst(members)

  // 4. Iterate members
  for _, m := range ordered {
    if err := c.markMemberUpgrading(ctx, m); err != nil { return err }

    // cordon and drain node
    if err := c.cordonAndDrain(ctx, m.NodeName); err != nil {
      c.triggerRollback(ctx, manifest, m, err)
      return err
    }

    // backup node state
    if err := c.backupNodeState(ctx, m); err != nil {
      c.triggerRollback(ctx, manifest, m, err)
      return err
    }

    // call agent to perform replace binary and restart
    if err := c.callAgentUpgrade(ctx, m, manifest.Component.ArtifactURL); err != nil {
      c.triggerRollback(ctx, manifest, m, err)
      return err
    }

    // wait for local health
    if err := c.waitLocalHealth(ctx, m, 5*time.Minute); err != nil {
      c.triggerRollback(ctx, manifest, m, err)
      return err
    }

    // cluster health check
    if err := c.clusterHealthCheck(ctx); err != nil {
      c.triggerRollback(ctx, manifest, m, err)
      return err
    }

    // uncordon
    if err := c.uncordon(ctx, m.NodeName); err != nil {
      c.logger.Warn("uncordon failed", "node", m.NodeName, "err", err)
    }

    if err := c.markMemberSucceeded(ctx, m); err != nil { return err }
  }

  // final cluster health verification
  if err := c.clusterHealthCheck(ctx); err != nil {
    return fmt.Errorf("final cluster health check failed: %w", err)
  }

  return nil
}
```
#### 辅助方法要点
- **takeSnapshot**：在 controller 端执行 `etcdctl snapshot save`，并上传到 snapshotStore（S3/NFS）。返回 snapshot URL。  
- **getEtcdMembers**：调用 `etcdctl member list -w json` 解析成员 IP 与 nodeName 映射（可通过 Node 标签或 CRD 关联）。  
- **callAgentUpgrade**：通过 Agent 的 API（或 SSH fallback）触发节点上的升级操作。Agent 接口示例：`POST /v1/etcd/upgrade`，body 包含 artifact URL、version、rollbackPoint。Agent 在本地执行替换并返回结果。  
- **waitLocalHealth**：轮询 Agent 提供的本地健康接口或直接通过 `etcdctl --endpoints=127.0.0.1:2379 endpoint health`（通过 SSH 或 Agent RPC）验证。  
- **triggerRollback**：记录失败原因到 CRD，调用回滚流程（优先尝试二进制回滚，必要时触发 snapshot restore 并通知运维）。
### Agent 实现样例 概览
**目标**：Agent 在节点上提供本地执行能力，接收 Controller 下发的升级任务并执行替换、健康检查与回滚。Agent 需要有足够权限执行 systemd 操作与文件替换。
#### Agent HTTP Handler 伪代码
```go
package agent

import (
  "net/http"
  "os/exec"
  "io"
  "os"
  "fmt"
)

type EtcdUpgradeRequest struct {
  ArtifactURL string `json:"artifactUrl"`
  Version string `json:"version"`
  RollbackPoint string `json:"rollbackPoint"`
}

func (a *AgentServer) HandleEtcdUpgrade(w http.ResponseWriter, r *http.Request) {
  var req EtcdUpgradeRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    http.Error(w, "bad request", http.StatusBadRequest)
    return
  }

  // 1. download artifact
  tmp := "/tmp/etcd-upgrade.tar.gz"
  if err := downloadFile(req.ArtifactURL, tmp); err != nil {
    http.Error(w, "download failed", http.StatusInternalServerError)
    return
  }

  // 2. backup binary
  exec.Command("cp", "/usr/local/bin/etcd", "/usr/local/bin/etcd.old").Run()
  exec.Command("systemctl", "stop", "etcd").Run()

  // 3. extract and replace
  exec.Command("tar", "xzf", tmp, "-C", "/tmp").Run()
  exec.Command("cp", "/tmp/etcd*/etcd", "/usr/local/bin/").Run()
  exec.Command("cp", "/tmp/etcd*/etcdctl", "/usr/local/bin/").Run()
  exec.Command("chmod", "+x", "/usr/local/bin/etcd").Run()

  // 4. start and health check
  exec.Command("systemctl", "daemon-reload").Run()
  exec.Command("systemctl", "start", "etcd").Run()

  // wait for local health
  // call etcdctl locally to check health
  // respond success or failure
  w.WriteHeader(http.StatusOK)
  io.WriteString(w, `{"status":"ok"}`)
}
```
#### Agent 本地回滚接口
- 提供 `POST /v1/etcd/rollback`，执行 `cp /usr/local/bin/etcd.old /usr/local/bin/etcd && systemctl restart etcd` 并返回结果。
### 测试与演练建议
- **单节点演练**：在单节点 etcd 集群上反复演练升级与回滚脚本。  
- **多节点演练**：在 3 节点测试集群上演练 follower-first、leader-last 升级路径。  
- **故障注入**：模拟节点升级失败、网络分区、快照损坏，验证回滚与恢复流程。  
- **CI 集成**：把脚本与 Controller/Agent 单元集成测试纳入 CI，自动运行关键演练。
### 安全与运维注意事项
- **证书与密钥管理**：`etcdctl` 使用的证书必须安全存储，Controller 与 Agent 访问权限受限。  
- **SSH 与 Agent 权限**：优先使用 Agent RPC，SSH 作为回退。Agent 运行需最小权限但能管理 systemd 与文件。  
- **审计与日志**：每次升级、回滚、快照都写事件到 Kubernetes Event 与 CRD 状态，便于审计。  
- **人工中断点**：在生产首次升级时建议在 leader 升级前设置人工审批点。
### 交付物与下一步
我可以把上面的 Bash 模板扩展为：  
- **完整可执行脚本**（参数化、日志、上传快照到 S3、自动解析 member 列表）  
- **Controller 真实代码片段**（含 client-go 调用、CRD 更新、并发控制）  
- **Agent 完整微服务示例**（HTTP API、任务队列、日志与回滚实现）
