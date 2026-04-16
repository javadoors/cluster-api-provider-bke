# Core-VersionConfig-v26.03.yaml
## `Core-VersionConfig-v26.03.yaml` 文件作用分析
### 一、文件本质
`Core-VersionConfig-v26.03.yaml` 是 openFuyao **v26.03 版本的版本配置清单**，本质上是一个 `BuildConfig` 结构的 YAML 实例，定义了该版本下所有组件的精确版本、镜像来源、二进制文件下载地址、Chart 包等信息。

与 [offline-artifacts.yaml](file:///d:/code/github/bkeadm/assets/offline-artifacts.yaml) 相比，它多了三个关键字段：

| 字段 | 作用 |
|------|------|
| `openFuyaoVersion: v26.03` | 标识该配置对应的 openFuyao 发行版本 |
| `kubernetesVersion` / `etcdVersion` / `containerdVersion` | 顶层版本锚定 |
| `usedPodInfo` | 每个镜像关联的 Pod 前缀和命名空间，用于升级时定位 Pod |
| `addons` | Addon 部署的版本和参数定义 |
| `openFuyaoCharts` | Chart 的版本和镜像 tag 版本映射 |
### 二、完整生命周期链路
```
┌──────────────────────────────────────────────────────────────────────┐
│  1. 发布阶段 (release-management 仓库)                                │
│                                                                      │
│  Core-VersionConfig-v26.03.yaml                                      │
│    ↓ 上传到华为云 OBS                                                 │
│  https://openfuyao.obs.cn-north-4.myhuaweicloud.com/                │
│    openFuyao/version-config/Core-VersionConfig-v26.03.yaml           │
└──────────────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────────────┐
│  2. 构建阶段 (bkeadm build)                                          │
│                                                                      │
│  offline-artifacts.yaml 的 patches 段引用:                            │
│    patches:                                                          │
│      - address: https://.../openFuyao/version-config/                │
│        files:                                                        │
│          - fileName: Core-VersionConfig-v26.03.yaml                  │
│                                                                      │
│  bke build -f offline-artifacts.yaml → 下载该文件                    │
│    → 打包到 /bke/volumes/patches/ 目录                               │
│    → 最终包含在 bke.tar.gz 离线包中                                   │
└──────────────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────────────┐
│  3. 初始化阶段 (bkeadm init)                                         │
│                                                                      │
│  解压 bke.tar.gz → patches 位于:                                     │
│    /bke/mount/source_registry/files/patches/                         │
│                                                                      │
│  【离线模式】offlineGenerateDeployCM()                                │
│    1. 遍历 patches 目录                                              │
│    2. 从文件名提取版本号: "v26.03"                                    │
│    3. 匹配 --oFVersion=v26.03 参数                                   │
│    4. 读取 YAML 内容                                                 │
│    5. 调用 SetPatchConfig() → 写入 ConfigMap:                        │
│       Namespace: openfuyao-patch                                     │
│       Name: cm.v26.03                                                │
│       Data: { "v26.03": "<YAML全文内容>" }                           │
│                                                                      │
│  【在线模式】onlineGenerateDeployCM()                                 │
│    1. 从 --versionUrl 下载 index.yaml                                │
│    2. 解析获取版本列表                                                │
│    3. 下载 Core-VersionConfig-v26.03.yaml                            │
│    4. 同样写入 ConfigMap                                              │
│                                                                      │
│  【同时】ProcessPatchFiles() 生成映射:                                │
│    BKECluster ConfigMap 中:                                          │
│      patch.v26.03 → cm.v26.03                                       │
└──────────────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────────────┐
│  4. Cluster API 部署阶段 (ensureClusterAPI)                          │
│                                                                      │
│  getClusterAPIVersion()                                              │
│    1. 从 ConfigMap "cm.v26.03" 读取数据                              │
│    2. 反序列化为 BuildConfig 结构                                     │
│    3. 在 repos 中查找:                                               │
│       - "bke-manifests" → tag 1.2.2 → manifestsVersion              │
│       - "cluster-api-provider-bke" → tag 1.2.2 → providerVersion    │
│    4. 使用这两个版本部署 Cluster API 组件                              │
└──────────────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────────────┐
│  5. Addon 部署阶段 (ensure_addon_deploy.go)                          │
│                                                                      │
│  BKECluster.Spec.ClusterConfig.Addons 中定义的 addon 列表:           │
│    - cluster-api: version=v1.4.3, providerVersion=1.2.2              │
│    - calico: version=v3.31.3                                         │
│    - coredns: version=v1.12.2-of.1                                   │
│    - openfuyao-system-controller: version=v1.1.2                     │
│    - bkeagent-deployer: version=v1.2.2                               │
│    ...                                                               │
│                                                                      │
│  这些 addon 版本信息来源于 VersionConfig 的 addons 段                 │
│  → 通过 BKECluster 配置传入                                          │
│  → 由 cluster-api-provider-bke 控制器部署到目标集群                    │
└──────────────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────────────┐
│  6. 升级阶段 (cluster-api-provider-bke)                              │
│                                                                      │
│  【Provider 自升级】ensure_provider_self_upgrade.go                   │
│    1. getPatchConfig() 从 ConfigMap "cm.v26.03" 读取                 │
│    2. GetPatchConfig() 反序列化为 PatchConfig 结构                    │
│    3. 在 repos 中查找 "cluster-api-provider-bke" 镜像                │
│    4. 获取 sourceRepo + name + tag → 拼接完整镜像地址                 │
│    5. Patch Deployment 更新镜像                                       │
│                                                                      │
│  【组件升级】ensure_component_upgrade.go                              │
│    1. getPatchConfig() 同上读取 VersionConfig                         │
│    2. 遍历 repos 中所有非 isKubernetes 的镜像                         │
│    3. 利用 usedPodInfo 定位目标集群中的 Pod                           │
│    4. 找到 Pod 的 Controller (Deployment/StatefulSet/DaemonSet)       │
│    5. 更新 Controller 的镜像 tag → 滚动升级                           │
│                                                                      │
│  【Agent 升级】ensure_agent_upgrade.go                                │
│    同样从 ConfigMap 读取 VersionConfig                                │
│    获取 bkeagent-launcher 的新版本镜像                                │
└──────────────────────────────────────────────────────────────────────┘
```
### 三、文件中各字段的具体作用
#### 1. 顶层版本字段
```yaml
openFuyaoVersion: v26.03          # 版本标识，用于 ConfigMap 的 key 和匹配
kubernetesVersion: v1.34.3-of.1   # K8s 版本锚定
containerdVersion: v2.1.1         # containerd 版本锚定
etcdVersion: v3.6.7-of.1          # etcd 版本锚定
```
- `openFuyaoVersion` 是 **ConfigMap 的 Data key**，也是 bkeadm `--oFVersion` 参数匹配的依据
- 其他版本字段供 bkeadm 和 controller 参考使用
#### 2. `repos` → 镜像版本定义（核心）
这是最核心的部分，定义了所有组件的精确镜像版本：

| 镜像分组 | 包含的组件 | 用途 |
|---------|----------|------|
| K3s 集群镜像 | registry, nginx, chartmuseum, nfs-server, k3s, pause | 引导节点本地服务 |
| K8s 核心组件 | kube-apiserver, kube-controller-manager, kube-scheduler, kube-proxy, etcd, coredns, pause | K8s 集群核心 |
| 网络组件 | calico(cni/node/kube-controllers/typha), keepalived, haproxy | 网络和负载均衡 |
| openFuyao 核心组件 | cluster-api-provider-bke, bke-manifests, bkeagent-deployer/launcher 等 | 平台核心 |
| 监控组件 | prometheus, alertmanager, node-exporter, kube-state-metrics 等 | 监控 |
| Harbor | harbor-core, harbor-portal, registry-photon 等 | 镜像仓库 |
| ingress-nginx | controller, kube-webhook-certgen | Ingress |

**关键字段 `usedPodInfo`**（与 offline-artifacts.yaml 的重要区别）：
```yaml
- name: kube-apiserver
  usedPodInfo:
    - podPrefix: kube-apiserver
      namespace: kube-system
  tag:
    - 1.34.3-of.1
```
`usedPodInfo` 的作用是**升级时定位 Pod**：
- `podPrefix`：Pod 名称前缀，用于在目标集群中查找对应的 Pod
- `namespace`：Pod 所在命名空间
- 找到 Pod 后，追溯其 Controller（Deployment/StatefulSet/DaemonSet），更新镜像 tag 触发滚动升级

**关键字段 `isKubernetes`**：
```yaml
isKubernetes: true   # 标记为 K8s 核心组件
```
- `isKubernetes: true` 的组件在 `ensure_component_upgrade` 阶段**跳过**（K8s 组件通过 kubeadm 升级流程处理）
- `isKubernetes: false` 的组件才通过 `processImageUpdates` 进行镜像 tag 更新
#### 3. `files` → 二进制文件下载
定义了 kubelet、kubectl、containerd、cni、helm、cfssl、runc、etcdctl 等二进制文件的下载地址和别名，用于 `bke build` 构建时下载打包。
#### 4. `patches` → 版本配置自身引用
```yaml
patches:
  - address: https://openfuyao.obs.cn-north-4.myhuaweicloud.com/openFuyao/version-config/
    files:
      - fileName: Core-VersionConfig-v26.03.yaml
```
**自引用**：该文件本身也被列入 patches 下载列表，确保构建时将自己打包进离线包。
#### 5. `charts` → Helm Chart 包
定义了所有 Helm Chart 的下载地址和版本化文件名（如 `oauth-webhook-1.0.2.tgz`），与 offline-artifacts.yaml 中使用 `latest` 不同，这里使用**精确版本号**。
#### 6. `addons` → Addon 部署定义
```yaml
addons:
  - name: cluster-api
    version: v1.4.3
    param:
      providerVersion: 1.2.2
      manifestsVersion: 1.2.2
  - name: calico
    version: v3.31.3
  - name: coredns
    version: v1.12.2-of.1
```
定义了集群创建时需要部署的 Addon 及其版本，这些信息最终写入 BKECluster 的 `Spec.ClusterConfig.Addons` 中，由 `ensure_addon_deploy` 阶段部署。
#### 7. `openFuyaoCharts` → Chart 版本映射
```yaml
openFuyaoCharts:
  - name: oauth-webhook
    chartVersion: 1.0.2
    tagVersion: 1.0.2
```
定义了 Chart 包版本和镜像 tag 版本的映射关系，用于 Addon 部署时确定 Chart 和镜像的对应版本。
### 四、与 offline-artifacts.yaml 的关键差异
| 维度 | offline-artifacts.yaml | Core-VersionConfig-v26.03.yaml |
|------|----------------------|-------------------------------|
| **定位** | 构建输入配置 | 版本配置清单 |
| **镜像 tag** | `latest` | 精确版本号（如 `1.2.2`） |
| **usedPodInfo** | ❌ 无 | ✅ 有（升级定位 Pod） |
| **isKubernetes** | ❌ 无 | ✅ 有（区分升级策略） |
| **openFuyaoVersion** | ❌ 无 | ✅ `v26.03` |
| **addons 段** | ❌ 无 | ✅ 有 |
| **openFuyaoCharts 段** | ❌ 无 | ✅ 有 |
| **使用场景** | `bke build` 构建 | 初始化 + 部署 + 升级全流程 |
### 五、总结
`Core-VersionConfig-v26.03.yaml` 是 openFuyao 版本管理的**核心枢纽文件**，贯穿了从构建到部署到升级的完整生命周期：
1. **构建时**：作为 patches 被 `bke build` 下载打包进离线包
2. **初始化时**：被 bkeadm 读取并写入 K8s ConfigMap（`openfuyao-patch/cm.v26.03`）
3. **部署时**：controller 从 ConfigMap 读取，获取 Cluster API 版本、Addon 版本等信息
4. **升级时**：controller 从 ConfigMap 读取，利用 `usedPodInfo` 定位 Pod 并更新镜像 tag 实现滚动升级
        
