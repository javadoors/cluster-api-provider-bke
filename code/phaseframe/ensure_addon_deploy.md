# ensure_addon_deploy.go业务流程梳理。
## EnsureAddonDeploy 业务流程梳理
### 一、整体定位
[ensure_addon_deploy.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_addon_deploy.go) 负责在**集群控制平面就绪后**，向目标集群**部署/更新/升级/删除 Addon（扩展组件）**。它通过比较 Spec 与 Status 中的 Addon 列表差异，计算出每个 Addon 的操作类型，然后逐一执行，并支持 Addon 的前置/后置自定义操作。
### 二、核心流程图
```
NeedExecute 判断
    │
    ├─ 控制平面未初始化 → 不执行
    ├─ 节点未完成 Boot → 不执行
    ├─ Spec.Addons 与 Status.AddonStatus 无差异 → 不执行
    └─ 有差异 → 执行
         │
         ▼
    Execute
         │
         ├─ 创建目标集群远程客户端（RemoteKubeClient）
         │
         ▼
    reconcileAddon
         │
         ├─ 1. validateAndPrepare（验证与准备）
         │     ├─ CompareBKEConfigAddon（对比差异）
         │     └─ 生成 AddonTransfer 列表
         │
         ├─ 2. 遍历 AddonTransfer 列表
         │     │
         │     ├─ [暂停检查] BKECluster.Paused → 停止部署
         │     │
         │     ├─ 2a. addonBeforeCreateCustomOperate（前置自定义操作）
         │     │     ├─ etcdbackup → 创建备份目录 + etcd 证书 Secret
         │     │     ├─ beyondELB → 创建 VIP + 标签节点
         │     │     ├─ cluster-api → 创建 kubeconfig/bkeconfig/patchconfig
         │     │     ├─ openFuyaoSystemController → 标签控制平面 + 分发 patch CM
         │     │     └─ gpu-manager → 重建 kube-scheduler Static Pod
         │     │
         │     ├─ 2b. processAddon（处理单个 Addon）
         │     │     ├─ TargetClusterClient.InstallAddon（安装到目标集群）
         │     │     ├─ Block=true + 失败 → 返回错误（阻塞）
         │     │     └─ Block=false + 失败 → 记录警告（继续）
         │     │
         │     └─ 2c. updateAddonStatus（更新 Addon 状态）
         │           ├─ Create → 追加到 Status.AddonStatus + 后置操作
         │           ├─ Update/Upgrade → 更新 Status.AddonStatus
         │           ├─ Remove → 从 Status.AddonStatus 删除
         │           └─ 记录 AddonRecorder
         │
         ├─ 3. 汇总结果（成功/失败数）
         │
         └─ 4. PostHook: saveAddonManifestsPostHook
               └─ 将 Addon 的 K8s 资源清单保存到 Master 节点
```
### 三、详细流程分析
#### 3.1 NeedExecute — 执行条件判断
```go
func (e *EnsureAddonDeploy) NeedExecute(old, new *BKECluster) bool
```
**三层检查**：

| 检查层 | 条件 | 说明 |
|--------|------|------|
| 第1层 | `AllowDeployAddonWithBKENodes` | 控制平面已初始化 + 至少一个 Master 节点已完成 Boot |
| 第2层 | `new.Spec.ClusterConfig.Addons != nil` | Spec 中配置了 Addon |
| 第3层 | `CompareBKEConfigAddon` 有差异 | Spec 与 Status 中的 Addon 列表不同 |
#### 3.2 Execute — 创建远程客户端
```go
func (e *EnsureAddonDeploy) Execute() (ctrl.Result, error)
```
1. 通过 `kube.NewRemoteClientByBKECluster` 创建到**目标集群**的远程客户端
2. 获取 `kubernetes.Clientset` 和 `dynamic.Interface` 用于操作目标集群资源
3. 调用 `reconcileAddon`
#### 3.3 validateAndPrepare — 验证与准备
```go
func (e *EnsureAddonDeploy) validateAndPrepare(params) ValidateAndPrepareResult
```
调用 [CompareBKEConfigAddon](file:///d:/code/github/cluster-api-provider-bke/common/cluster/addon/compare.go#L32) 对比 `Status.AddonStatus`（旧）与 `Spec.ClusterConfig.Addons`（新），生成 `AddonTransfer` 列表：

| 操作类型 | 触发条件 | 说明 |
|----------|----------|------|
| `CreateAddon` | 新 Addon 不在旧列表中 | 新增安装 |
| `UpdateAddon` | Addon 存在但参数不同（非版本） | 更新配置 |
| `UpgradeAddon` | Addon 版本变化 | 版本升级 |
| `RemoveAddon` | 旧 Addon 不在新列表中 | 删除卸载 |
#### 3.4 addonBeforeCreateCustomOperate — 前置自定义操作
```go
func (e *EnsureAddonDeploy) addonBeforeCreateCustomOperate(addon *Product) error
```
**仅在 `CreateAddon` 操作时执行**，针对特定 Addon 进行前置准备：

| Addon 名称 | 前置操作 | 详细说明 |
|------------|----------|----------|
| **etcdbackup** | `createEtcdBackupDir` + `createEtcdCertSecret` | ① 在 etcd 节点上创建备份目录（通过 Agent Shell 命令 `mkdir -p`）<br>② 在目标集群 `kube-system` 命名空间创建 `etcd-backup-secrets`（包含 etcd CA/Client 证书） |
| **beyondELB** | `createBeyondELBVIP` + `labelNodesForELB` | ① 在 Ingress 节点上通过 HA 命令创建 VIP（Keepalived Static Pod）<br>② 为 ELB 节点打上 `beyondELB` 标签 |
| **cluster-api** | 多步操作 | ① 创建 `local-kubeconfig` Secret（目标集群管理员 kubeconfig）<br>② 创建 `least-privilege-kubeconfig` Secret（最小权限 kubeconfig）<br>③ 标记 BKEAgent 切换待定<br>④ 迁移 `bke-config` ConfigMap 到目标集群<br>⑤ 迁移 `patch-config` ConfigMap 到目标集群<br>⑥ 同步 Chart Addon 的 values.yaml CM 和 Chart 仓库认证 Secret 到目标集群 |
| **openFuyaoSystemController** | `addControlPlaneLabels` + `distributePatchCM` | ① 为 Master 节点打 `control-plane` 标签<br>② 分发 patch ConfigMap 到目标集群 |
| **gpu-manager** | `reCreateKubeSchedulerStaticPodYaml` | 在 Master 节点上重建 kube-scheduler Static Pod YAML（启用 GPU 调度扩展） |
#### 3.5 processAddon — 处理单个 Addon
```go
func (e *EnsureAddonDeploy) processAddon(params) ProcessAddonResult
```
**核心安装逻辑**：
1. 获取集群节点列表
2. 创建 `AddonRecorder` 记录 Addon 操作
3. 调用 `TargetClusterClient.InstallAddon` 将 Addon 安装到目标集群
4. 获取最新的 BKECluster（安装过程中用户可能修改了配置）
5. **失败处理**：
   - `Block=true`：返回错误，**阻塞后续 Addon 部署**
   - `Block=false`：记录警告，**继续部署下一个 Addon**
#### 3.6 updateAddonStatus — 更新 Addon 状态
```go
func (e *EnsureAddonDeploy) updateAddonStatus(params) error
```
根据操作类型更新 `BKECluster.Status.AddonStatus`：

| 操作 | 状态更新 | 额外操作 |
|------|----------|----------|
| `CreateAddon` | 追加到 AddonStatus | 执行 `addonAfterCreateCustomOperate` |
| `UpdateAddon` | 替换 AddonStatus 中对应项 | — |
| `UpgradeAddon` | 替换 AddonStatus 中对应项 | — |
| `RemoveAddon` | 从 AddonStatus 中删除 | 移除 Condition |

**后置自定义操作**（`addonAfterCreateCustomOperate`）：

| Addon 名称 | 后置操作 |
|------------|----------|
| **openFuyaoSystemController** | 生成默认用户名/密码，输出登录信息到日志 |

#### 3.7 saveAddonManifestsPostHook — 后置钩子
```go
func (e *EnsureAddonDeploy) saveAddonManifestsPostHook()
```
**在 Phase 执行完成后**（无论成功或失败），将所有已部署 Addon 的 K8s 资源清单保存到 Master 节点：
1. 遍历 `addonRecorders`，每个 Recorder 记录了一个 Addon 的所有 K8s 资源对象
2. 为每个 Addon 生成 Agent 命令：
   - `mkdir -p /etc/kubernetes/addon-manifests/<addon-name>-<version>`
   - 对每个对象执行 `kubectl get <kind> [-n <ns>] <name> -oyaml > <file>`
3. 命令下发到所有 Master 节点
4. **不等待命令完成**（TTL 10 分钟后自动删除命令）
### 四、Addon 类型与安装方式
根据代码中的引用，BKE 支持两种 Addon 类型：

| 类型 | 标识 | 安装方式 |
|------|------|----------|
| **Chart Addon** | `chart` | 通过 Helm Chart 安装到目标集群 |
| **YAML Addon** | `yaml` | 直接应用 YAML 清单到目标集群 |

Chart Addon 还需要额外的准备工作：
- 将 `ValuesConfigMapRef` 引用的 ConfigMap 同步到目标集群
- 将 Chart 仓库的认证 Secret（`AuthSecretRef`/`TlsSecretRef`）同步到目标集群
### 五、关键设计要点
#### 5.1 声明式 Addon 管理
通过 `CompareBKEConfigAddon` 对比 Spec（期望状态）与 Status（当前状态），自动计算出需要的操作（Create/Update/Upgrade/Remove），实现了**声明式的 Addon 生命周期管理**。
#### 5.2 Block 机制
每个 Addon 有 `Block` 属性：
- `Block=true`：关键 Addon（如 coredns、kube-proxy），失败阻塞后续部署
- `Block=false`：非关键 Addon（如监控、日志），失败不影响后续部署
#### 5.3 前置/后置自定义操作
针对特定 Addon 的特殊需求，设计了**前置操作**（`addonBeforeCreateCustomOperate`）和**后置操作**（`addonAfterCreateCustomOperate`）的扩展点：
```
Create Addon 流程：
  addonBeforeCreateCustomOperate → InstallAddon → updateAddonStatus → addonAfterCreateCustomOperate
```
#### 5.4 Addon 清单备份
`saveAddonManifestsPostHook` 将 Addon 的 K8s 资源清单保存到 Master 节点的 `/etc/kubernetes/addon-manifests/` 目录，用于：
- 灾难恢复时快速重建 Addon
- 排查 Addon 部署问题
- 审计 Addon 资源变更
#### 5.5 暂停支持
遍历 Addon 列表时检查 `BKECluster.Spec.Pause` 和 `PausedAnnotation`，支持用户暂停 Addon 部署流程。
#### 5.6 实时状态刷新
每个 Addon 处理完成后，通过 `GetNewestBKECluster` 获取最新的 BKECluster，确保后续操作基于最新状态，避免覆盖用户的并发修改。
### 六、与其他 Phase 的对比
| 维度 | EnsureMasterInit/Join | EnsureWorkerJoin | EnsureAddonDeploy |
|------|----------------------|------------------|-------------------|
| **操作对象** | KubeadmControlPlane | MachineDeployment | 目标集群 Addon |
| **执行位置** | 管理集群 CAPI 对象 | 管理集群 CAPI 对象 | 目标集群 K8s 资源 |
| **客户端** | 管理集群 Client | 管理集群 Client | 目标集群 RemoteClient |
| **失败策略** | 全部回滚 | 部分成功继续 | Block 决定 |
| **前置条件** | 节点就绪 | 控制平面初始化 + 节点就绪 | 控制平面初始化 + Master Boot |
| **自定义操作** | 无 | 无 | 前置/后置扩展点 |
        
# Addon 安装的组件及安装命令
## Addon 安装的组件及安装命令
### 一、默认安装的 Addon 组件
根据 [export.go](file:///d:/code/github/cluster-api-provider-bke/common/cluster/initialize/export.go) 中的 `defaultAddons()` 函数，BKE 默认安装以下 Addon：

| 序号 | Addon 名称 | 默认版本 | 类型 | 说明 |
|------|-----------|---------|------|------|
| 1 | **kubeproxy** | 1.25.6 | YAML | Kubernetes 网络代理 |
| 2 | **calico** | v3.4.1 | YAML | CNI 网络插件 |
| 3 | **coredns** | v1.8.0 | YAML | 集群 DNS 服务 |
| 4 | **nfs-csi** | v4.1.0 | YAML | NFS CSI 存储驱动 |
| 5 | **bocoperator** | latest | YAML | BOC 运维管理平台 |
| 6 | **cluster-api** | v1.3.2 | YAML | CAPI 管理组件 |
### 二、bke-manifests 仓库中所有可用的 Addon 组件
根据 [bke-manifests/kubernetes](file:///d:/code/github/bke-manifests/kubernetes/) 目录，所有可用的 YAML 类型 Addon 如下：

| Addon 名称 | 可用版本 | 功能说明 |
|-----------|---------|---------|
| **kubeproxy** | v1.21.1, v1.21.14, v1.23.17, v1.25.6, v1.27.2, v1.28.8-of.1, v1.29.1, v1.33.1, v1.33.1-of.1, v1.33.1-of.2, v1.34.3-of.1 | Kube-Proxy 网络代理 |
| **calico** | v3.25.0, v3.27.3, v3.31.3 | Calico CNI 网络插件 |
| **coredns** | v1.8.0, v1.8.6, v1.9.3, v1.10.1, v1.12.2-of.1 | 集群 DNS |
| **nfs-csi** | v4.1.0 | NFS CSI 存储 |
| **bocoperator** | (不在 manifests 仓库) | BOC 运维管理平台 |
| **cluster-api** | v1.1.4, v1.3.2, v1.4.3 | CAPI 管理组件 |
| **nodelocaldns** | v1.26.4 | 节点本地 DNS 缓存 |
| **etcdbackup** | 3.4.3 | etcd 定时备份 |
| **beyondELB** | v2.1.5 | 负载均衡器 |
| **cert-manager** | v1.11.0 | 证书管理 |
| **gpu-manager** | 1.1.4 | GPU 管理器 |
| **kube-gpu** | 1.0.0, 1.1.0, 1.2.0 | GPU 调度 |
| **prometheus** | v2.11.0, v2.32.1 | 监控告警 |
| **efk** | 7.13.1 | 日志收集 |
| **logrotate** | 1.2 | 日志轮转 |
| **kubectl** | v1.21, v1.23, v1.25 | kubectl 终端 |
| **kubehelm** | 1.6.12, 1.6.13 | Helm 控制器 |
| **kubevirt** | v1.0.0 | 虚拟化管理 |
| **jenkins** | 2.278, 2.375.3-lts | CI/CD |
| **minio** | 2023-04-20 | 对象存储 |
| **mysql** | 5.7, katib-8.0.29 | 数据库 |
| **postgres** | 16 | 数据库 |
| **redis** | 6.2.12 | 缓存 |
| **argo-workflow** | v3.4.3 | 工作流引擎 |
| **katib** | v0.15.0 | 超参调优 |
| **kserve** | v0.11.2 | 模型服务 |
| **volcano** | release-1.7-bcc | 批调度 |
| **vpa** | 0.10.0 | 垂直自动伸缩 |
| **victoriametrics-controller** | 0.20.0, latest | 监控 |
| **rdma-dev-plugin** | v1.5.0 | RDMA 设备插件 |
| **numa-affinity-package** | v0.0.1, v0.0.2 | NUMA 亲和 |
| **priorityclass** | latest | 优先级类 |
| **openfuyao-system-controller** | latest, v1.0.1, v25.03, dev-aiaio | 开放扶摇系统 |
| **bkeagent** | latest | BKE Agent |
| **bkeagent-deployer** | latest, v1.0.1, v1.0.4 | BKE Agent 部署器 |
| **bkeagent-update** | latest | BKE Agent 更新 |
| **clusterextra** | latest + 各脚本 | 集群额外脚本 |
| **fabric** | (不在 manifests 仓库) | 网络插件(另一种 CNI) |
### 三、两种安装方式及安装命令
#### 方式一：YAML 类型 Addon（默认类型）
**安装流程**：
1. **读取 YAML 文件**：从 `/manifests/kubernetes/<addon_name>/<version>/` 目录读取所有 `.yaml` 文件
2. **模板渲染**：使用 Go `text/template` 引擎渲染 YAML，注入参数（镜像仓库、网络配置、节点信息等）
3. **按序应用**：按文件名排序（安装时正序，卸载时逆序），逐个 Apply 到目标集群
4. **等待就绪**：如果 `Block=true`，等待资源就绪

**安装命令（等效）**：
```bash
# 等效于 kubectl apply -f，但使用 Go 的 dynamic client 实现 Server-Side Apply
kubectl apply -f <rendered_yaml_file> --server-side --force-conflicts --field-manager=bke
```
**实际实现**（[yaml.go](file:///d:/code/github/cluster-api-provider-bke/pkg/kube/yaml.go)）：
- 使用 `dynamic.ResourceInterface.Apply()` → Server-Side Apply
- 更新操作：`dr.Patch(types.ApplyPatchType)` → Strategic Merge Patch
- 删除操作：`dr.Delete()` → 直接删除
- 升级操作：`dr.Apply()` → Server-Side Apply
#### 方式二：Chart 类型 Addon
**安装流程**：
1. **获取 Chart 包**：从 Chart 仓库（支持 OCI 和 HTTP）拉取 Chart 包
2. **获取 Values**：从 ConfigMap 读取 `values.yaml` 配置
3. **Helm 安装**：使用 Helm SDK 执行安装/升级/卸载

**安装命令（等效）**：
```bash
# 安装
helm install <releaseName> <chartName> --version <version> --namespace <ns> --wait --wait-for-jobs
# 升级
helm upgrade <releaseName> <chartName> --version <version> --namespace <ns> --wait --wait-for-jobs
# 卸载
helm uninstall <releaseName> --namespace <ns> --wait
```
**实际实现**（[chart.go](file:///d:/code/github/cluster-api-provider-bke/pkg/kube/chart.go)）：
- 使用 `helm.sh/helm/v3/pkg/action` SDK
- `action.NewInstall` → 安装
- `action.NewUpgrade` → 升级
- `action.NewUninstall` → 卸载
- 支持从 OCI Registry 和 HTTP Chart 仓库拉取
### 四、特殊 Addon 的前置/后置操作
某些 Addon 在安装前/后需要额外的操作（[ensure_addon_deploy.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_addon_deploy.go)）：

| Addon | 前置操作 (addonBeforeCreateCustomOperate) | 后置操作 (addonAfterCreateCustomOperate) |
|-------|------------------------------------------|------------------------------------------|
| **etcdbackup** | ① 在 etcd 节点创建备份目录 ② 在目标集群创建 etcd 证书 Secret | 无 |
| **beyondELB** | ① 创建 VIP ② 为 LB 节点打标签 | 无 |
| **cluster-api** | ① 创建 local kubeconfig Secret ② 创建最小权限 kubeconfig Secret ③ 标记 BKEAgent 切换 pending ④ 创建 bkeconfig ConfigMap ⑤ 创建 patchconfig ConfigMap ⑥ 将 Chart 引用写入 BKECluster | 无 |
| **openfuyao-system-controller** | ① 为 Master 节点添加 control-plane 标签 ② 下发 patch ConfigMap | ① 创建默认用户名密码 ② 输出登录信息 |
| **gpu-manager** | 重建 kube-scheduler Static Pod YAML | 无 |
### 五、特殊参数增强
在 [addon.go](file:///d:/code/github/cluster-api-provider-bke/pkg/kube/addon.go) 的 `enhanceCommonParamForSpecialAddons` 中，以下 Addon 会被注入额外参数：

| Addon | 额外注入的参数 |
|-------|--------------|
| **bocoperator** | master SSH 信息、pipeline 服务器信息、portalClusterToken、数据库配置、master 服务器列表 |
| **cluster-api** (manage=true) | clusterToken（门户集群 Token）、nodes（管理节点模板数据） |
| **fabric** | 解析 fabric 专用参数覆盖 addon.Param |
| **nodelocaldns** | domain（DNS 域名）、DNSserver/clusterDNS（根据 proxyMode 区分 iptables/ipvs 模式） |
### 六、通用参数注入
所有 Addon 都会通过 `getCommonParamFromBKECluster` 注入以下通用参数：

| 参数类别 | 参数名 | 说明 |
|---------|-------|------|
| 镜像仓库 | imageRepo, imageRepoDomain, imageRepoPort, imageRepoIp, imageRepoPrefix | 镜像仓库地址 |
| HTTP 仓库 | httpRepo, httpRepoDomain, httpRepoPort, httpRepoIp, httpRepoPrefix | RPM/DEB 包仓库 |
| NTP | ntpServer | NTP 服务器 |
| Agent | agentHealthPort | Agent 健康端口 |
| NFS | nfsServer, nfsRootDir, nfsVersion | NFS 存储 |
| 网络 | podSubnet, serviceSubnet, dnsDomain, apiServerSrcHost, apiServerSrcPort | 集群网络 |
| etcd | etcdEndpoints, etcdIps | etcd 端点 |
| 节点 | masterReplicas, workerReplicas, replicas | 节点副本数 |
| DNS | dnsIP | 集群 DNS IP |
| 存储 | kubeletDataRoot, dockerDataRoot | 数据目录 |
| 版本 | k8sVersion, version | K8s 和 BKE 版本 |
| 命名空间 | namespace | 集群命名空间 |
| 网络 | clusterNetworkMode | 网络模式(calico/fabric) |

