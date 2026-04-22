# EnsureComponentUpgrade 业务流程
## EnsureComponentUpgrade 业务流程梳理
### 一、整体流程图
```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    EnsureComponentUpgrade Phase 执行流程                     │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  1. NeedExecute() - 判断是否需要执行                                        │
│     ├── 检查是否为补丁版本（patch version）                                 │
│     │   └── isPatchVersion(): v.X.Y.Z 且 Z > 0，无预发布标识                │
│     ├── 初次安装场景：status.OpenFuyaoVersion == "" 且是补丁版本            │
│     └── 非初次安装：status.OpenFuyaoVersion != spec.OpenFuyaoVersion        │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  2. Execute() - 执行升级                                                    │
│     ├── getRemoteClient() → 获取目标集群的 Kubernetes Client                │
│     ├── loadLocalKubeConfig() → 加载本地 kubeconfig                         │
│     └── rolloutOpenfuyaoComponent() → 执行组件滚动升级                      │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  3. rolloutOpenfuyaoComponent() - 核心升级逻辑                              │
│     ├── getPatchConfig() → 从 ConfigMap 获取补丁配置                        │
│     │   ├── 读取本地 bke-config ConfigMap（key: patch.{version}）           │
│     │   └── 读取 openfuyao-patch/{version} ConfigMap 获取 PatchConfig       │
│     └── processImageUpdates() → 处理镜像更新                                │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  4. processImageUpdates() - 遍历处理所有镜像仓库                            │
│     └── for each repo in patchCfg.Repos:                                    │
│         ├── 跳过 Kubernetes 组件（由其他 Phase 处理）                       │
│         └── processRepoImages() → 处理仓库中的所有子镜像                    │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  5. processSubImage() - 处理单个子镜像                                      │
│     └── for each image in subImage.Images:                                  │
│         └── updateSingleImage() → 更新单个镜像                              │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  6. updateSingleImage() - 更新单个镜像的所有 Pod                            │
│     └── for each podInfo in image.UsedPodInfo:                              │
│         └── updatePodImageTag() → 更新 Pod 镜像 Tag                         │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  7. updatePodImageTag() - 更新 Pod 控制器的镜像                             │
│     ├── findMatchingPods() → 按前缀查找匹配的 Pod                           │
│     ├── getPodController() → 获取 Pod 的控制器（Deployment/STS/DS/RS）      │
│     └── upgradeXxxImage() → 更新控制器中的镜像 Tag                          │
│         ├── upgradeDeploymentImage()                                        │
│         ├── upgradeStatefulSetImage()                                       │
│         ├── upgradeDaemonSetImage()                                         │
│         └── upgradeReplicaSetImage()                                        │
└─────────────────────────────────────────────────────────────────────────────┘
```
### 二、核心数据结构
```go
// 补丁配置（从 ConfigMap 解析）
type PatchConfig struct {
    Registry          Registry `json:"registry"`           // 镜像仓库地址
    OpenFuyaoVersion  string   `json:"openfuyaoVersion"`   // openFuyao 版本
    ContainerdVersion string   `json:"containerdVersion"`  // containerd 版本
    KubernetesVersion string   `json:"kubernetesVersion"`  // Kubernetes 版本
    Repos             []Repo   `json:"repos"`              // 镜像仓库列表
    Files             []File   `json:"files"`              // 文件列表
}

// 镜像仓库
type Repo struct {
    Architecture []string   `json:"architecture"`   // 支持的架构
    IsKubernetes bool       `json:"isKubernetes"`   // 是否为 Kubernetes 组件
    SubImages    []SubImage `json:"subImages"`      // 子镜像列表
}

// 子镜像（同一仓库下的多个镜像）
type SubImage struct {
    SourceRepo string  `json:"sourceRepo"`  // 源仓库
    TargetRepo string  `json:"targetRepo"`  // 目标仓库
    Images     []Image `json:"images"`      // 镜像列表
}

// 镜像定义
type Image struct {
    Name        string    `json:"name"`        // 镜像名称（不含 tag）
    UsedPodInfo []PodInfo `json:"usedPodInfo"` // 使用该镜像的 Pod 信息
    Tag         []string  `json:"tag"`         // 镜像 tag（补丁升级只有一个 tag）
}

// Pod 信息
type PodInfo struct {
    PodPrefix string `json:"podPrefix"` // Pod 名称前缀
    NameSpace string `json:"namespace"` // Pod 命名空间
}

// 镜像更新信息
type ImageUpdate struct {
    ImageName string // 镜像名称（不带 tag）
    PodPrefix string // Pod 名称前缀
    NameSpace string // Pod 归属命名空间
    NewTag    string // 新的镜像 tag
}
```
### 三、关键函数详解
#### 3.1 NeedExecute() - 执行条件判断
```go
func (e *EnsureComponentUpgrade) NeedExecute(old, new *BKECluster) bool {
    // 1. 默认条件检查
    if !e.BasePhase.DefaultNeedExecute(old, new) {
        return false
    }
    
    // 2. 判断是否需要组件升级
    return e.isComponentNeedUpgrade(old, new)
}

func (e *EnsureComponentUpgrade) isComponentNeedUpgrade(old, new *BKECluster) bool {
    // 场景 1：初次安装补丁版本
    // status.OpenFuyaoVersion == "" 且安装的是补丁版本（v.X.Y.Z, Z > 0）
    if new.Status.OpenFuyaoVersion == "" {
        return e.isPatchVersion(new.Spec.ClusterConfig.Cluster.OpenFuyaoVersion)
    }
    
    // 场景 2：非初次安装，版本变更
    // 检查是否有节点需要升级组件
    nodes := phaseutil.GetNeedUpgradeComponentNodesWithBKENodes(new, bkeNodes)
    return nodes != nil && nodes.Length() > 0
}

func (e *EnsureComponentUpgrade) isPatchVersion(version string) bool {
    // 判断是否为补丁版本：v.X.Y.Z 格式，Z > 0，无预发布标识
    // 例如：v2.6.1 → true
    //      v2.6.0 → false
    //      v2.6.1-rc1 → false
    v, err := semver.NewVersion(strings.TrimPrefix(version, "v"))
    return err == nil && v.Patch > 0 && v.PreRelease == ""
}
```
#### 3.2 getPatchConfig() - 获取补丁配置
```go
func (e *EnsureComponentUpgrade) getPatchConfig() (*PatchConfig, error) {
    openFuyaoVersion := bkeCluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion
    
    // 1. 从本地 bke-config ConfigMap 获取补丁配置 key
    // key 格式：patch.{version}，例如 patch.v2.6.1
    localConfigMap.Data["patch.v2.6.1"] → "cm.v2.6.1"
    
    // 2. 从 openfuyao-patch 命名空间获取补丁配置
    // ConfigMap 名称：cm.{version}，例如 cm.v2.6.1
    // namespace: openfuyao-patch
    patchConfigMap.Data["v2.6.1"] → YAML 格式的 PatchConfig
    
    // 3. 解析 YAML 为 PatchConfig 结构
    return GetPatchConfig(patchConfigMap.Data[openFuyaoVersion])
}
```
#### 3.3 processImageUpdates() - 镜像更新处理
```go
func (e *EnsureComponentUpgrade) processImageUpdates(patchCfg *PatchConfig) error {
    for _, repo := range patchCfg.Repos {
        // 跳过 Kubernetes 组件（由 EnsureMasterUpgrade/EnsureWorkerUpgrade 处理）
        if repo.IsKubernetes {
            continue
        }
        
        // 处理仓库中的所有子镜像
        for _, subImage := range repo.SubImages {
            for _, image := range subImage.Images {
                // 更新单个镜像
                if err := e.updateSingleImage(image); err != nil {
                    return err
                }
            }
        }
    }
    return nil
}
```
#### 3.4 updateSingleImage() - 更新单个镜像
```go
func (e *EnsureComponentUpgrade) updateSingleImage(image Image) error {
    tag := image.Tag[0]  // 补丁升级只有一个 tag
    
    for _, podInfo := range image.UsedPodInfo {
        update := &ImageUpdate{
            ImageName: image.Name,    // 例如：openfuyao-controller
            PodPrefix: podInfo.PodPrefix,  // 例如：openfuyao-controller-manager
            NameSpace: podInfo.NameSpace,  // 例如：openfuyao-system
            NewTag:    tag,           // 例如：v2.6.1
        }
        
        if err := e.updatePodImageTag(update); err != nil {
            return err
        }
    }
    return nil
}
```
#### 3.5 updatePodImageTag() - 更新 Pod 控制器镜像
```go
func (e *EnsureComponentUpgrade) updatePodImageTag(update *ImageUpdate) error {
    // 1. 查找匹配的 Pod（按名称前缀）
    pods, _ := e.findMatchingPods(update.NameSpace, update.PodPrefix)
    
    if len(pods) == 0 {
        return nil  // 未找到 Pod，跳过
    }
    
    // 2. 获取 Pod 的控制器
    controller, controllerType, _ := e.getPodController(pods[0])
    
    // 3. 根据控制器类型更新镜像
    switch controllerType {
    case "Deployment":
        return e.upgradeDeploymentImage(controller, update)
    case "StatefulSet":
        return e.upgradeStatefulSetImage(controller, update)
    case "DaemonSet":
        return e.upgradeDaemonSetImage(controller, update)
    case "ReplicaSet":
        return e.upgradeReplicaSetImage(controller, update)
    }
}
```
#### 3.6 getPodController() - 获取 Pod 控制器
```go
func (e *EnsureComponentUpgrade) getPodController(pod corev1.Pod) (metav1.Object, string, error) {
    for _, ownerRef := range pod.OwnerReferences {
        switch ownerRef.Kind {
        case "ReplicaSet":
            // ReplicaSet 可能被 Deployment 管理，需要继续向上查找
            rs, _ := clientSet.AppsV1().ReplicaSets(namespace).Get(ownerRef.Name)
            for _, rsOwnerRef := range rs.OwnerReferences {
                if rsOwnerRef.Kind == "Deployment" {
                    deployment, _ := clientSet.AppsV1().Deployments(namespace).Get(rsOwnerRef.Name)
                    return deployment, "Deployment", nil
                }
            }
            return rs, "ReplicaSet", nil
            
        case "StatefulSet":
            sts, _ := clientSet.AppsV1().StatefulSets(namespace).Get(ownerRef.Name)
            return sts, "StatefulSet", nil
            
        case "DaemonSet":
            ds, _ := clientSet.AppsV1().DaemonSets(namespace).Get(ownerRef.Name)
            return ds, "DaemonSet", nil
        }
    }
    return &pod, "Pod", nil  // 无控制器，直接操作 Pod
}
```
#### 3.7 upgradeDeploymentImage() - 更新 Deployment 镜像
```go
func (e *EnsureComponentUpgrade) upgradeDeploymentImage(deployment *appsv1.Deployment, update *ImageUpdate) error {
    return retry.RetryOnConflict(retry.DefaultRetry, func() error {
        // 1. 重新获取 Deployment（避免冲突）
        deploymentCfg, _ := e.remoteClient.AppsV1().Deployments(namespace).Get(deployment.Name)
        
        needUpdated := false
        for i, container := range deploymentCfg.Spec.Template.Spec.Containers {
            // 2. 匹配镜像名称（不含 tag）
            if e.isMatchingImage(container.Image, update.ImageName) {
                newImage := e.buildNewImage(container.Image, update.NewTag)
                if container.Image != newImage {
                    deploymentCfg.Spec.Template.Spec.Containers[i].Image = newImage
                    needUpdated = true
                }
            }
        }
        
        // 3. 更新 Deployment
        if needUpdated {
            _, err = e.remoteClient.AppsV1().Deployments(namespace).Update(deploymentCfg)
            return err
        }
        return nil
    })
}
```
### 四、ConfigMap 配置示例
#### 4.1 本地 bke-config ConfigMap
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: bke-config
  namespace: cluster-system
data:
  patch.v2.6.1: "cm.v2.6.1"    # 补丁版本 → 补丁 ConfigMap 名称
  patch.v2.6.2: "cm.v2.6.2"
```
#### 4.2 补丁配置 ConfigMap
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm.v2.6.1
  namespace: openfuyao-patch
data:
  v2.6.1: |
    registry:
      imageAddress: repo.openfuyao.cn
      architecture:
        - amd64
        - arm64
    openfuyaoVersion: v2.6.1
    containerdVersion: v1.7.2
    kubernetesVersion: v1.29.0
    repos:
      - architecture:
          - amd64
          - arm64
        isKubernetes: false
        subImages:
          - sourceRepo: openfuyao
            targetRepo: openfuyao
            images:
              - name: openfuyao-controller
                usedPodInfo:
                  - podPrefix: openfuyao-controller-manager
                    namespace: openfuyao-system
                tag:
                  - v2.6.1
              - name: openfuyao-apiserver
                usedPodInfo:
                  - podPrefix: openfuyao-apiserver
                    namespace: openfuyao-system
                tag:
                  - v2.6.1
      - architecture:
          - amd64
        isKubernetes: true    # Kubernetes 组件，跳过（由其他 Phase 处理）
        subImages: []
```
### 五、执行场景总结
| 场景 | 条件 | 行为 |
|------|------|------|
| **初次安装补丁版本** | `status.OpenFuyaoVersion == ""` 且 `isPatchVersion(spec.OpenFuyaoVersion)` | 执行组件升级 |
| **版本升级** | `status.OpenFuyaoVersion != spec.OpenFuyaoVersion` | 执行组件升级 |
| **非补丁版本安装** | `status.OpenFuyaoVersion == ""` 且 `!isPatchVersion(spec.OpenFuyaoVersion)` | 跳过 |
| **版本未变更** | `status.OpenFuyaoVersion == spec.OpenFuyaoVersion` | 跳过 |
### 六、与 ComponentVersion YAML 的映射
根据之前的提案，`EnsureComponentUpgrade` 对应 **openFuyao ComponentVersion**：
```yaml
apiVersion: nodecomponent.io/v1alpha1
kind: ComponentVersion
metadata:
  name: openfuyao-v2.6.1
spec:
  componentName: openFuyao
  version: v2.6.1
  scope: Cluster
  dependencies: [kubernetes]

  upgradeAction:
    steps:
      - name: patch-controller-image
        type: Kubectl
        kubectl:
          operation: Patch
          resource: "deployment/openfuyao-controller-manager"
          namespace: openfuyao-system
          fieldPatch: |
            {"spec":{"template":{"spec":{"containers":[{"name":"manager","image":"{{.ImageRepo}}/openfuyao-controller:{{.Version}}"}]}}}}
      - name: patch-apiserver-image
        type: Kubectl
        kubectl:
          operation: Patch
          resource: "deployment/openfuyao-apiserver"
          namespace: openfuyao-system
          fieldPatch: |
            {"spec":{"template":{"spec":{"containers":[{"name":"apiserver","image":"{{.ImageRepo}}/openfuyao-apiserver:{{.Version}}"}]}}}}
    postCheck:
      name: verify-deployment-ready
      type: Kubectl
      kubectl:
        operation: Wait
        resource: "deployment/openfuyao-controller-manager"
        namespace: openfuyao-system
        condition: "Available"
        timeout: 180s
```
**关键差异**：
- 现有实现通过 **ConfigMap 动态配置** 镜像更新列表
- YAML 声明式方案需要 **预定义所有组件的更新规则**
- 现有实现支持 **运行时动态发现 Pod 控制器类型**
- YAML 方案需要 **明确指定每个组件的更新方式**
        
