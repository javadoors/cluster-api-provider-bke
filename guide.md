  
# 详细分析三个组件之间的调用关系
## cluster-api-provider-bke 与 openfuyao-system-controller、bke-manifests 的调用关系
### 一、整体架构关系
```
┌─────────────────────────────────────────────────────────────┐
│                   组件调用关系架构图                          │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌────────────────────────────────────────────────────┐    │
│  │              Management Cluster                     │    │
│  │            (管理集群/K3s集群)                       │    │
│  │                                                     │    │
│  │  ┌─────────────────────────────────────────────┐   │    │
│  │  │   cluster-system namespace                  │   │    │
│  │  │                                             │   │    │
│  │  │  ┌──────────────────────────────────────┐  │   │    │
│  │  │  │  bke-controller-manager Pod          │  │   │    │
│  │  │  │                                      │  │   │    │
│  │  │  │  ┌────────────────────────────────┐ │  │   │    │
│  │  │  │  │ manager container              │ │  │   │    │
│  │  │  │  │                                │ │  │   │    │
│  │  │  │  │ cluster-api-provider-bke       │ │  │   │    │
│  │  │  │  │ :{{.providerVersion}}          │ │  │   │    │
│  │  │  │  │                                │ │  │   │    │
│  │  │  │  │ - 监听 BKECluster CR          │ │  │   │    │
│  │  │  │  │ - 管理集群生命周期            │ │  │   │    │
│  │  │  │  │ - 读取 /manifests 文件        │ │  │   │    │
│  │  │  │  └────────────────────────────────┘ │  │   │    │
│  │  │  │                                      │  │   │    │
│  │  │  │  ┌────────────────────────────────┐ │  │   │    │
│  │  │  │  │ manifests container (sidecar)  │ │  │   │    │
│  │  │  │  │                                │ │  │   │    │
│  │  │  │  │ bke-manifests                  │ │  │   │    │
│  │  │  │  │ :{{.manifestsVersion}}         │ │  │   │    │
│  │  │  │  │                                │ │  │   │    │
│  │  │  │  │ - 提供 manifests 文件          │ │  │   │    │
│  │  │  │  │ - 挂载到 /manifests            │ │  │   │    │
│  │  │  │  │ - 共享存储卷                   │ │  │   │    │
│  │  │  │  └────────────────────────────────┘ │  │   │    │
│  │  │  │                                      │  │   │    │
│  │  │  │  共享 Volume: manifests             │  │   │    │
│  │  │  └──────────────────────────────────────┘  │   │    │
│  │  │                                             │   │    │
│  │  └─────────────────────────────────────────────┘   │    │
│  │                                                     │    │
│  │  ┌─────────────────────────────────────────────┐   │    │
│  │  │   openfuyao-system-controller namespace     │   │    │
│  │  │                                             │   │    │
│  │  │  ┌──────────────────────────────────────┐  │   │    │
│  │  │  │  openfuyao-system-controller Pod     │  │   │    │
│  │  │  │                                      │  │   │    │
│  │  │  │  - 平台系统控制器                    │  │   │    │
│  │  │  │  - 管理平台级资源                    │  │   │    │
│  │  │  │  - 与 cluster-api 协同工作           │  │   │    │
│  │  │  └──────────────────────────────────────┘  │   │    │
│  │  │                                             │   │    │
│  │  └─────────────────────────────────────────────┘   │    │
│  │                                                     │    │
│  └────────────────────────────────────────────────────┘    │
│                                                              │
│  ┌────────────────────────────────────────────────────┐    │
│  │              Workload Cluster                       │    │
│  │            (工作负载集群/目标集群)                  │    │
│  │                                                     │    │
│  │  由 cluster-api-provider-bke 创建和管理            │    │
│  │                                                     │    │
│  └────────────────────────────────────────────────────┘    │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```
### 二、组件详细说明
#### 2.1 cluster-api-provider-bke
**核心功能**：
- Cluster API Provider 的 BKE 实现
- 监听和管理 BKECluster CRD
- 负责集群生命周期管理（创建、更新、删除）
- 与 Kubernetes API Server 交互

**部署配置**：
```yaml
# 来自 cluster-api-bke.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: bke-controller-manager
  namespace: cluster-system
spec:
  template:
    spec:
      containers:
      - name: manager
        image: {{ if .repo }}{{ .repo }}{{ else }}cr.openfuyao.cn/openfuyao/{{ end }}cluster-api-provider-bke:{{.providerVersion}}
        imagePullPolicy: Always
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8081
            scheme: HTTP
        ports:
        - containerPort: 9443
          name: webhook-server
          protocol: TCP
        volumeMounts:
        - mountPath: /manifests
          name: manifests
        - mountPath: /tmp/k8s-webhook-server/serving-certs
          name: cert
          readOnly: true
```
#### 2.2 bke-manifests
**核心功能**：
- Sidecar 容器模式
- 提供 Kubernetes manifests 文件
- 通过共享卷与主容器通信
- 包含集群部署所需的 YAML 文件

**部署配置**：
```yaml
# 来自 cluster-api-bke.yaml
containers:
- name: manifests
  image: {{ if .repo }}{{ .repo }}{{ else }}cr.openfuyao.cn/openfuyao/{{ end }}bke-manifests:{{.manifestsVersion}}
  imagePullPolicy: Always
  volumeMounts:
    - mountPath: /manifests
      name: manifests
volumes:
  - name: manifests
    emptyDir: {}
```
#### 2.3 openfuyao-system-controller
**核心功能**：
- OpenFuyao 平台系统控制器
- 管理平台级资源和配置
- 提供 Web UI 和 API
- 与 cluster-api 协同工作

**部署配置**：
```go
// 来自 config.go
func (op *Options) createSystemControllerAddon() confv1beta1.Product {
    return confv1beta1.Product{
        Name:    "openfuyao-system-controller",
        Version: "latest",
        Param: map[string]string{
            "helmRepo":   "https://helm.openfuyao.cn/_core",
            "tagVersion": "latest",
        },
    }
}
```
### 三、调用关系详解
```
┌─────────────────────────────────────────────────────────────┐
│                   组件调用关系流程图                          │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  1. 部署阶段                                                │
│     ┌──────────────┐                                        │
│     │ bkeadm       │                                        │
│     │              │                                        │
│     │ bke init     │                                        │
│     └──────┬───────┘                                        │
│            │                                                 │
│            ▼                                                 │
│     ┌──────────────────────────────────────────────┐       │
│     │ DeployClusterAPI()                           │       │
│     │                                              │       │
│     │ 1. 安装 cert-manager                         │       │
│     │ 2. 安装 cluster-api (CAPI)                   │       │
│     │ 3. 安装 cluster-api-bke (BKE Provider)      │       │
│     └──────┬───────────────────────────────────────┘       │
│            │                                                 │
│            ▼                                                 │
│     ┌──────────────────────────────────────────────┐       │
│     │ 创建 bke-controller-manager Pod              │       │
│     │                                              │       │
│     │ ┌────────────────────────────────────────┐  │       │
│     │ │ manager container                      │  │       │
│     │ │ - cluster-api-provider-bke             │  │       │
│     │ └────────────────────────────────────────┘  │       │
│     │ ┌────────────────────────────────────────┐  │       │
│     │ │ manifests container (sidecar)          │  │       │
│     │ │ - bke-manifests                        │  │       │
│     │ │ - 挂载到 /manifests                    │  │       │
│     │ └────────────────────────────────────────┘  │       │
│     │ 共享 Volume: manifests                      │       │
│     └──────────────────────────────────────────────┘       │
│                                                              │
│  2. 运行阶段                                                │
│     ┌──────────────────────────────────────────────┐       │
│     │ bke-controller-manager Pod                   │       │
│     │                                              │       │
│     │ ┌────────────────────────────────────────┐  │       │
│     │ │ manager container                      │  │       │
│     │ │                                        │  │       │
│     │ │ 1. 监听 BKECluster CR                 │  │       │
│     │ │ 2. 读取 /manifests/*.yaml             │  │       │
│     │ │ 3. 创建/更新/删除集群                 │  │       │
│     │ │                                        │  │       │
│     │ │         ▲                              │  │       │
│     │ │         │ 读取 manifests              │  │       │
│     │ │         │                              │  │       │
│     │ └─────────┼──────────────────────────────┘  │       │
│     │           │                                  │       │
│     │ ┌─────────┼──────────────────────────────┐  │       │
│     │ │ manifests container                    │  │       │
│     │ │                                        │  │       │
│     │ │ /manifests/                            │  │       │
│     │ │ ├── kubeadm-init.yaml                 │  │       │
│     │ │ ├── kubeadm-join.yaml                 │  │       │
│     │ │ ├── kubelet-config.yaml               │  │       │
│     │ │ └── ...                                │  │       │
│     │ │                                        │  │       │
│     │ └────────────────────────────────────────┘  │       │
│     └──────────────────────────────────────────────┘       │
│                                                              │
│  3. 平台集成阶段                                            │
│     ┌──────────────────────────────────────────────┐       │
│     │ openfuyao-system-controller                  │       │
│     │                                              │       │
│     │ 1. 通过 Helm 部署                            │       │
│     │ 2. 监听集群状态                              │       │
│     │ 3. 提供 Web UI                               │       │
│     │ 4. 调用 cluster-api API                     │       │
│     │                                              │       │
│     └──────────────────────────────────────────────┘       │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```
### 四、数据流和交互
```
┌─────────────────────────────────────────────────────────────┐
│                   数据流和交互详解                            │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌────────────────────────────────────────────────────┐    │
│  │ 1. 用户创建集群请求                                 │    │
│  │    用户 → openfuyao-system-controller              │    │
│  └────────────────────────────────────────────────────┘    │
│            │                                                 │
│            ▼                                                 │
│  ┌────────────────────────────────────────────────────┐    │
│  │ 2. 创建 BKECluster CR                              │    │
│  │    openfuyao-system-controller → K8s API Server    │    │
│  └────────────────────────────────────────────────────┘    │
│            │                                                 │
│            ▼                                                 │
│  ┌────────────────────────────────────────────────────┐    │
│  │ 3. 监听 BKECluster CR                              │    │
│  │    K8s API Server → cluster-api-provider-bke      │    │
│  └────────────────────────────────────────────────────┘    │
│            │                                                 │
│            ▼                                                 │
│  ┌────────────────────────────────────────────────────┐    │
│  │ 4. 读取 manifests 文件                             │    │
│  │    cluster-api-provider-bke → /manifests/*.yaml   │    │
│  │    (从 bke-manifests sidecar 共享卷读取)          │    │
│  └────────────────────────────────────────────────────┘    │
│            │                                                 │
│            ▼                                                 │
│  ┌────────────────────────────────────────────────────┐    │
│  │ 5. 创建目标集群                                    │    │
│  │    cluster-api-provider-bke → 目标节点            │    │
│  │    - 使用 manifests 中的配置                       │    │
│  │    - 初始化控制平面                                │    │
│  │    - 加入工作节点                                  │    │
│  └────────────────────────────────────────────────────┘    │
│            │                                                 │
│            ▼                                                 │
│  ┌────────────────────────────────────────────────────┐    │
│  │ 6. 更新集群状态                                    │    │
│  │    cluster-api-provider-bke → BKECluster.status   │    │
│  └────────────────────────────────────────────────────┘    │
│            │                                                 │
│            ▼                                                 │
│  ┌────────────────────────────────────────────────────┐    │
│  │ 7. 显示集群状态                                    │    │
│  │    BKECluster.status → openfuyao-system-controller│    │
│  │    → Web UI                                        │    │
│  └────────────────────────────────────────────────────┘    │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```
### 五、关键代码实现
#### 5.1 部署 Cluster API
```go
// 来自 clusterapi.go
func DeployClusterAPI(repo, manifestsVersion, providerVersion string) error {
    // 1. 确保 K8s 客户端
    if err := ensureK8sClient(); err != nil {
        return err
    }

    // 2. 写入模板文件
    tmplDir := filepath.Join(global.Workspace, "tmpl")
    if err := writeClusterAPITemplates(tmplDir); err != nil {
        return err
    }

    // 3. 安装 cert-manager
    log.BKEFormat(log.INFO, "Install Certificate Management...")
    certManagerFile := filepath.Join(tmplDir, "cert-manager.yaml")
    if err := global.K8s.InstallYaml(certManagerFile, map[string]string{"repo": repo}, ""); err != nil {
        return err
    }

    // 4. 安装 Cluster API
    log.BKEFormat(log.INFO, "Install the Cluster API...")
    clusterAPIFile := filepath.Join(tmplDir, "cluster-api.yaml")
    if err := installClusterAPIWithRetry(clusterAPIFile, repo); err != nil {
        return err
    }

    // 5. 安装 BKE Provider (包含 bke-manifests sidecar)
    clusterAPIBKEFile := filepath.Join(tmplDir, "cluster-api-bke.yaml")
    params := map[string]string{
        "repo":             repo,
        "manifestsVersion": manifestsVersion,
        "providerVersion":  providerVersion,
    }
    if err := global.K8s.InstallYaml(clusterAPIBKEFile, params, ""); err != nil {
        return err
    }

    // 6. 等待 Pod 运行
    return waitForClusterAPIPodsRunning()
}
```
#### 5.2 获取版本信息
```go
// 来自 initialize.go
func (op *Options) getClusterAPIVersion(ofVersion, clusterAPIVersion string) (string, string) {
    defaultVersion := "latest"
    if ofVersion != "" {
        defaultVersion = ofVersion
    }
    if clusterAPIVersion != "" {
        defaultVersion = clusterAPIVersion
    }

    // 从配置中获取版本
    cfg := configinit.GetDefaultBKEConfig()
    manifestsVersion := findImageTag(cfg, "bke-manifests", defaultVersion)
    providerVersion := findImageTag(cfg, "cluster-api-provider-bke", defaultVersion)

    return manifestsVersion, providerVersion
}
```
#### 5.3 配置 Addons
```go
// 来自 config.go
func (op *Options) createClusterAPIAddon(sandbox, offline string) confv1beta1.Product {
    return confv1beta1.Product{
        Name:    "cluster-api",
        Version: "v1.4.3",
        Block:   true,  // 阻塞式安装，必须成功
        Param: map[string]string{
            "manage":            "true",
            "offline":           offline,
            "sandbox":           sandbox,
            "replicas":          "1",
            "containerdVersion": "v2.1.1",
            "openFuyaoVersion":  "latest",
            "manifestsVersion":  "latest",  // bke-manifests 版本
            "providerVersion":   "latest",  // cluster-api-provider-bke 版本
        },
    }
}

func (op *Options) createSystemControllerAddon() confv1beta1.Product {
    return confv1beta1.Product{
        Name:    "openfuyao-system-controller",
        Version: "latest",
        Param: map[string]string{
            "helmRepo":   "https://helm.openfuyao.cn/_core",
            "tagVersion": "latest",
        },
    }
}
```
### 六、Sidecar 模式详解
```
┌─────────────────────────────────────────────────────────────┐
│                   Sidecar 模式工作原理                        │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  Pod: bke-controller-manager                                │
│  ┌────────────────────────────────────────────────────┐    │
│  │                                                     │    │
│  │  ┌──────────────────────────────────────────────┐ │    │
│  │  │ manager container                            │ │    │
│  │  │                                              │ │    │
│  │  │ cluster-api-provider-bke:latest             │ │    │
│  │  │                                              │ │    │
│  │  │ 功能:                                        │ │    │
│  │  │ - 运行控制器主逻辑                           │ │    │
│  │  │ - 监听 CRD 变化                             │ │    │
│  │  │ - 调用 Kubernetes API                       │ │    │
│  │  │ - 读取 /manifests 目录                      │ │    │
│  │  │                                              │ │    │
│  │  │ 挂载点:                                      │ │    │
│  │  │ /manifests → 共享卷                         │ │    │
│  │  │                                              │ │    │
│  │  └──────────────────────────────────────────────┘ │    │
│  │                                                     │    │
│  │  ┌──────────────────────────────────────────────┐ │    │
│  │  │ manifests container (sidecar)                │ │    │
│  │  │                                              │ │    │
│  │  │ bke-manifests:latest                        │ │    │
│  │  │                                              │ │    │
│  │  │ 功能:                                        │ │    │
│  │  │ - 提供 manifests 文件                        │ │    │
│  │  │ - 初始化 /manifests 目录                    │ │    │
│  │  │ - 只读方式共享文件                           │ │    │
│  │  │                                              │ │    │
│  │  │ 挂载点:                                      │ │    │
│  │  │ /manifests → 共享卷                         │ │    │
│  │  │                                              │ │    │
│  │  │ 文件结构:                                    │ │    │
│  │  │ /manifests/                                 │ │    │
│  │  │ ├── kubeadm/                               │ │    │
│  │  │ │   ├── init.yaml                         │ │    │
│  │  │ │   └── join.yaml                         │ │    │
│  │  │ ├── kubelet/                              │ │    │
│  │  │ │   └── config.yaml                       │ │    │
│  │  │ └── addons/                               │ │    │
│  │  │     ├── calico.yaml                       │ │    │
│  │  │     └── coredns.yaml                      │ │    │
│  │  │                                              │ │    │
│  │  └──────────────────────────────────────────────┘ │    │
│  │                                                     │    │
│  │  共享卷:                                            │    │
│  │  ┌──────────────────────────────────────────────┐ │    │
│  │  │ Volume: manifests                            │ │    │
│  │  │ Type: emptyDir                               │ │    │
│  │  │                                              │ │    │
│  │  │ 生命周期: 与 Pod 相同                        │ │    │
│  │  │ 访问模式: ReadWriteMany                      │ │    │
│  │  └──────────────────────────────────────────────┘ │    │
│  │                                                     │    │
│  └────────────────────────────────────────────────────┘    │
│                                                              │
│  工作流程:                                                   │
│  1. Pod 启动                                                │
│     - manifests container 启动并初始化 /manifests 目录      │
│     - manager container 启动并等待就绪                      │
│                                                              │
│  2. 运行时                                                   │
│     - manager container 读取 /manifests 中的文件           │
│     - 使用这些文件创建/管理集群                              │
│                                                              │
│  3. 更新                                                     │
│     - 更新 bke-manifests 镜像版本                           │
│     - 重启 Pod 以加载新的 manifests                         │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```
### 七、总结
**三个组件的关系**：
1. **cluster-api-provider-bke**：
   - 主控制器，负责集群生命周期管理
   - 监听 BKECluster CRD
   - 通过 Kubernetes API 管理资源
2. **bke-manifests**：
   - Sidecar 容器，提供 manifests 文件
   - 通过共享卷与主容器通信
   - 包含集群部署所需的配置文件
3. **openfuyao-system-controller**：
   - 平台级控制器，提供 Web UI 和 API
   - 通过 Helm 部署，独立运行
   - 与 cluster-api 协同工作，管理平台资源

**调用关系**：
- openfuyao-system-controller → 创建 BKECluster CR → cluster-api-provider-bke 监听并处理
- cluster-api-provider-bke ← 读取 manifests 文件 ← bke-manifests (sidecar)
- 三个组件协同工作，实现完整的集群管理平台

# 部署架构
## 部署架构分析
### 结论：**不是**全部在同一个Pod中
这三个组件分为**两个独立的部署单元**：
```
┌─────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                        │
│                                                              │
│  ┌───────────────────────────────────────────────────────┐  │
│  │         Pod 1: bke-controller-manager                 │  │
│  │  ┌─────────────────────────────────────────────────┐  │  │
│  │  │  InitContainer: manifests                       │  │  │
│  │  │  - Image: bke-manifests:latest                  │  │  │
│  │  │  - Command: cp -rf . /manifests                 │  │  │
│  │  └─────────────────────────────────────────────────┘  │  │
│  │                      ↓ (共享Volume)                    │  │
│  │  ┌─────────────────────────────────────────────────┐  │  │
│  │  │  Container: manager                             │  │  │
│  │  │  - Image: cluster-api-provider-bke:latest       │  │  │
│  │  │  - Mount: /manifests                            │  │  │
│  │  └─────────────────────────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                              │
│  ┌───────────────────────────────────────────────────────┐  │
│  │         Pod 2: openfuyao-system-controller           │  │
│  │  ┌─────────────────────────────────────────────────┐  │  │
│  │  │  Container: system-controller                   │  │  │
│  │  │  - 通过Helm Chart部署                           │  │  │
│  │  │  - 独立的Deployment                             │  │  │
│  │  └─────────────────────────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```
### 详细说明
#### Pod 1: bke-controller-manager
来自 [cluster-api-bke.yaml:2998-3112](file:///d:/code/github/bkeadm/pkg/initialize/clusterapi/cluster-api-bke.yaml#L2998-L3112)：
```yaml
kind: Deployment
metadata:
  name: bke-controller-manager
  namespace: cluster-system
spec:
  template:
    spec:
      # InitContainer: bke-manifests
      initContainers:
        - name: manifests
          image: bke-manifests:{{.manifestsVersion}}
          command:
            - /bin/sh
            - -c
            - cp -rf . /manifests
          volumeMounts:
            - mountPath: /manifests
              name: manifests
      
      # Main Container: cluster-api-provider-bke
      containers:
        - name: manager
          image: cluster-api-provider-bke:{{.providerVersion}}
          volumeMounts:
            - mountPath: /manifests
              name: manifests
      
      # 共享Volume
      volumes:
        - name: manifests
          hostPath:
            path: /etc/openFuyao/addons/manifests
```
#### Pod 2: openfuyao-system-controller
来自 [config.go:430-440](file:///d:/code/github/bkeadm/pkg/config/config.go#L430-L440)：

```go
func (op *Options) createSystemControllerAddon() confv1beta1.Product {
    return confv1beta1.Product{
        Name:    "openfuyao-system-controller",
        Version: "latest",
        Param: map[string]string{
            "helmRepo":   "https://helm.openfuyao.cn/_core",
            "tagVersion": "latest",
        },
    }
}
```
这是一个独立的Addon，通过Helm Chart部署。
### 为什么这样设计？
#### 1. cluster-api-provider-bke + bke-manifests 在同一Pod
**设计模式：Sidecar模式（使用InitContainer）**
- **bke-manifests**：作为InitContainer，负责将manifests文件复制到共享卷
- **cluster-api-provider-bke**：主容器，读取manifests文件并执行集群管理逻辑
- **优势**：
  - 紧耦合：manifests是provider运行所需的数据
  - 原子性：两者一起部署、一起升级
  - 共享存储：通过emptyDir或hostPath共享文件
#### 2. openfuyao-system-controller 独立部署
**设计模式：独立服务**
- **职责不同**：system-controller负责系统级控制逻辑，与Cluster API Provider职责分离
- **独立生命周期**：可以独立升级、扩缩容
- **松耦合**：通过Kubernetes API与其他组件交互
### 部署顺序
根据 [config.go:405-408](file:///d:/code/github/bkeadm/pkg/config/config.go#L405-L408)：
```go
func (op *Options) applyFuyaoCommonConfig(cfg *confv1beta1.BKEConfig, sandbox, offline string) {
    clusterAPIAddon := op.createClusterAPIAddon(sandbox, offline)
    systemControllerAddon := op.createSystemControllerAddon()
    
    cfg.Addons = append(cfg.Addons, clusterAPIAddon, systemControllerAddon)
}
```
部署顺序：
1. **cluster-api** (包含cluster-api-provider-bke + bke-manifests)
2. **openfuyao-system-controller**
### 总结
| 组件 | 部署方式 | Pod名称 | 是否在同一Pod |
|------|---------|---------|--------------|
| cluster-api-provider-bke | Deployment中的主容器 | bke-controller-manager | ✅ 是 |
| bke-manifests | Deployment中的InitContainer | bke-controller-manager | ✅ 是 |
| openfuyao-system-controller | 独立的Helm Chart部署 | 独立Pod | ❌ 否 |

**核心结论**：
- cluster-api-provider-bke 和 bke-manifests 在**同一个Pod**中（Sidecar模式）
- openfuyao-system-controller 在**另一个独立的Pod**中
        
