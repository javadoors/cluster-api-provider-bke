# EnsureProviderSelfUpgrade 业务流程梳理
## 一、Phase 定位
`EnsureProviderSelfUpgrade` 是 BKE 集群升级流程中的一个 Phase，负责 **bke-controller-manager 自身（即 Provider）的镜像升级**。这是一个"自举升级"场景——Provider 在运行过程中修改自己所在 Deployment 的镜像，触发自身重启。
## 二、核心常量
| 常量 | 值 | 含义 |
|------|-----|------|
| `providerNamespace` | `cluster-system` | Provider Deployment 所在命名空间 |
| `providerDeploymentName` | `bke-controller-manager` | Provider Deployment 名称 |
| `providerContainerName` | `manager` | 目标容器名称 |
| `providerImageName` | `cluster-api-provider-bke` | Provider 镜像名关键字 |
| `deploymentReadyTimeout` | `5m` | 等待新 Pod 就绪的超时时间 |
| `gracefulShutdownDuration` | `2s` | 升级成功后的优雅等待时间 |
## 三、完整业务流程
```
PhaseFlow 调度 EnsureProviderSelfUpgrade
│
│  ┌──────────────────────────────────────────────────────────────┐
│  │  Phase 1: NeedExecute(old, new) — 判断是否需要执行           │
│  └──────────────────────────────────────────────────────────────┘
│
├── 1.1 通用检查 (DefaultNeedExecute)
│   ├── BKECluster 正在删除？ → 跳过
│   ├── BKECluster 已暂停？ → 跳过
│   ├── BKECluster DryRun？ → 跳过
│   ├── BKECluster 健康状态 Failed？ → 跳过
│   └── 非 BKECluster 类型且非完全控制？ → 跳过
│
├── 1.2 版本变更检查 (isProviderNeedUpgrade)
│   │
│   ├── 场景 A: 首次安装 (Status.OpenFuyaoVersion == "")
│   │   ├── Spec 版本是非 Patch 版本 (如 v1.0.0)?
│   │   │   └── 返回 false — 首次安装非 Patch 版本不需要自升级
│   │   └── Spec 版本是 Patch 版本 (如 v1.0.1)?
│   │       └── 继续后续检查
│   │
│   ├── 场景 B: 非首次安装 (Status.OpenFuyaoVersion != "")
│   │   ├── Spec 版本 == Status 版本？
│   │   │   └── 返回 false — 版本未变化，无需升级
│   │   └── Spec 版本 != Status 版本？
│   │       └── 继续后续检查
│   │
│   ├── 1.3 获取当前 Deployment 镜像
│   │   ├── 读取 Deployment: cluster-system/bke-controller-manager
│   │   ├── 找到 container: "manager"
│   │   └── 获取当前镜像: currentImage
│   │       例: "registry.example.com/cluster-api-provider-bke:v1.0.0"
│   │
│   ├── 1.4 解析目标镜像 (getProviderTargetImage)
│   │   ├── 读取本地 ConfigMap: cluster-system/bke-config
│   │   ├── 检查 key "patch.<openFuyaoVersion>" 是否存在
│   │   │   └── 不存在 → 非 Patch 版本，返回空，跳过自升级
│   │   ├── 读取 Patch ConfigMap: openfuyao-patch/cm.<openFuyaoVersion>
│   │   ├── 解析 YAML 为 PatchConfig 结构
│   │   └── 在 PatchConfig.Repos 中查找 Provider 镜像
│   │       └── (详见 1.5)
│   │
│   ├── 1.5 查找 Provider 镜像 (findProviderImageInPatchConfig)
│   │   ├── 遍历 PatchConfig.Repos[]
│   │   │   └── 遍历 Repo.SubImages[]
│   │   │       └── 遍历 SubImage.Images[]
│   │   │           └── isProviderImage(image)?
│   │   │               ├── 匹配方式1: image.Name 包含 "cluster-api-provider-bke"
│   │   │               └── 匹配方式2: image.UsedPodInfo 中存在
│   │   │                   PodPrefix=="bke-controller-manager"
│   │   │                   && NameSpace=="cluster-system"
│   │   │
│   │   └── 找到后拼接完整镜像:
│   │       fullImage = "<sourceRepo>/<image.Name>:<image.Tag[0]>"
│   │       例: "registry.example.com/cluster-api-provider-bke:v1.0.1"
│   │
│   └── 1.6 比较镜像
│       ├── currentImage == targetImage → 返回 false (已一致)
│       └── currentImage != targetImage → 返回 true ✅ 需要自升级
│
│  ┌──────────────────────────────────────────────────────────────┐
│  │  Phase 2: Execute() — 执行自升级                             │
│  └──────────────────────────────────────────────────────────────┘
│
├── 2.1 解析目标镜像
│   └── 同 1.4-1.5，获取 targetImage
│
├── 2.2 Patch Deployment 镜像 (PatchDeploymentImage)
│   ├── 获取 Deployment: cluster-system/bke-controller-manager
│   ├── 找到 container: "manager"
│   ├── 更新 container.Image = targetImage
│   ├── 添加 Annotation 触发滚动更新:
│   │   annotations["bke.openfuyao.cn/restartedAt"] = <当前时间 RFC3339>
│   └── 执行 cli.Update() 提交变更
│       │
│       └── Kubernetes 滚动更新机制:
│           ├── 创建新 Pod (使用新镜像)
│           ├── 新 Pod Ready 后终止旧 Pod
│           └── 旧 Pod (即当前运行的 Provider 进程) 被终止
│
├── 2.3 等待新 Pod 就绪 (WaitDeploymentReady)
│   ├── 轮询间隔: 2s
│   ├── 超时: 5min
│   ├── 每次轮询检查:
│   │   ├── Deployment.UpdatedReplicas == Replicas?
│   │   ├── Deployment.AvailableReplicas == Replicas?
│   │   └── 是否存在使用 targetImage 的 Ready Pod?
│   │
│   └── 特殊处理: Context Canceled
│       ├── 如果等待过程中 context 被取消
│       │   (因为当前 Provider 进程被终止)
│       ├── 用 context.Background() 重新检查镜像
│       ├── 如果镜像已更新为目标镜像
│       │   └── 视为升级成功，返回 Requeue
│       └── 否则返回错误
│
└── 2.4 返回结果
    └── ctrl.Result{Requeue: true}
        → 升级成功后重新入队，由新版本的 Provider 继续处理后续 Phase
│
│  ┌──────────────────────────────────────────────────────────────┐
│  │  Phase 3: PostHook(err) — 后置处理                           │
│  └──────────────────────────────────────────────────────────────┘
│
├── 3.1 执行默认后置钩子 (DefaultPostHook)
│   ├── 记录 Phase 耗时指标
│   ├── 设置 Phase 状态: Succeeded / Failed
│   └── 上报状态到 BKECluster.Status.PhaseStatus
│
└── 3.2 自升级特有逻辑
    └── 如果 err == nil (升级成功)
        ├── 记录日志: "self-upgrade successful"
        └── time.Sleep(2s) — 优雅等待
            │
            └── 目的: 给新 Pod 启动时间，确保新版本
                Provider 已接管调谐循环后再退出
```
## 四、关键数据流：PatchConfig 查找链
这是理解目标镜像如何获取的核心路径：
```
BKECluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion = "v1.0.1"
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│ Step 1: 读取本地 ConfigMap                                  │
│                                                               │
│   ConfigMap: cluster-system/bke-config                       │
│   ┌───────────────────────────────────────────────────────┐ │
│   │ data:                                                 │ │
│   │   "patch.v1.0.1": "cm.v1.0.1"  ← 指向 Patch CM 名   │ │
│   │   "patch.v1.0.0": ""            ← 基础版本，无 Patch  │ │
│   └───────────────────────────────────────────────────────┘ │
│                                                               │
│   检查 key "patch.v1.0.1" 是否存在                           │
│   ├── 不存在 → 非 Patch 版本，跳过自升级                      │
│   └── 存在 → 获取 Patch CM 名称: "cm.v1.0.1"                │
└─────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│ Step 2: 读取 Patch ConfigMap                                │
│                                                               │
│   ConfigMap: openfuyao-patch/cm.v1.0.1                       │
│   ┌───────────────────────────────────────────────────────┐ │
│   │ data:                                                 │ │
│   │   "v1.0.1": |                                         │ │
│   │     registry:                                         │ │
│   │       imageAddress: registry.example.com              │ │
│   │     openfuyaoVersion: v1.0.1                          │ │
│   │     repos:                                            │ │
│   │       - subImages:                                    │ │
│   │         - sourceRepo: registry.example.com            │ │
│   │           images:                                     │ │
│   │           - name: /cluster-api-provider-bke           │ │
│   │             tag: ["v1.0.1"]                           │ │
│   │             usedPodInfo:                              │ │
│   │             - podPrefix: bke-controller-manager       │ │
│   │               namespace: cluster-system               │ │
│   │           - name: /bke-agent                          │ │
│   │             tag: ["v1.0.1"]                           │ │
│   └───────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│ Step 3: 在 PatchConfig 中查找 Provider 镜像                  │
│                                                               │
│   遍历: PatchConfig.Repos[] → Repo.SubImages[] → Image[]    │
│                                                               │
│   匹配规则 (isProviderImage):                                 │
│   ├── 规则1: image.Name 包含 "cluster-api-provider-bke"      │
│   └── 规则2: image.UsedPodInfo 中有                          │
│       PodPrefix=="bke-controller-manager"                    │
│       && NameSpace=="cluster-system"                         │
│                                                               │
│   找到后拼接:                                                 │
│   fullImage = "registry.example.com/cluster-api-provider-bke:v1.0.1" │
└─────────────────────────────────────────────────────────────┘
```
## 五、自升级的特殊性：Context Canceled 处理
这是本 Phase 最关键的设计点。因为 Provider 在升级自己，所以：
```
时间线:
  T0: Provider (v1.0.0) 执行 PatchDeploymentImage
      └── Deployment 镜像更新为 v1.0.1
      └── Kubernetes 开始滚动更新

  T1: Provider (v1.0.0) 执行 WaitDeploymentReady
      └── 轮询等待新 Pod Ready...

  T2: 新 Pod (v1.0.1) Ready
      └── Kubernetes 终止旧 Pod (v1.0.0)

  T3: 旧 Pod 被终止
      └── context 被取消 (context canceled)
      └── WaitDeploymentReady 收到 ctx.Done()

  处理策略:
      ├── 用 context.Background() 重新获取 Deployment 镜像
      ├── 如果当前镜像 == targetImage → 视为成功，返回 Requeue
      └── 否则 → 返回错误
```
## 六、PostHook 中的优雅等待
```go
func (p *EnsureProviderSelfUpgrade) PostHook(err error) error {
    if hookErr := p.DefaultPostHook(err); hookErr != nil {
        return hookErr
    }
    if err == nil {
        time.Sleep(gracefulShutdownDuration) // 2s
    }
    return nil
}
```
**目的**：升级成功后，当前进程（旧版本 Provider）会 sleep 2 秒，给新版本 Provider 足够时间启动并接管调谐循环，避免出现调谐空窗期。
## 七、流程总结图
```
                    NeedExecute
                        │
            ┌───────────┼───────────┐
            ▼           ▼           ▼
       通用检查     版本变更?    镜像不同?
       (失败→跳过)  (否→跳过)   (否→跳过)
            │           │           │
            └───────────┼───────────┘
                        │ 是
                        ▼
                    Execute
                        │
            ┌───────────┼───────────┐
            ▼           ▼           ▼
      解析目标镜像  Patch Deployment  等待 Ready
      (PatchConfig)  (更新镜像+注解)  (轮询5min)
                        │               │
                        │    ┌──────────┤
                        │    ▼          ▼
                        │  正常就绪  Context Canceled
                        │              │
                        │    ┌─────────┤
                        │    ▼         ▼
                        │  镜像已更新  镜像未更新
                        │  →Requeue   →Error
                        ▼
                    PostHook
                        │
            ┌───────────┼───────────┐
            ▼                       ▼
      DefaultPostHook          Sleep(2s)
      (状态上报+指标)        (优雅等待新Provider接管)
                        │
                        ▼
                   旧进程退出
                   新Provider继续
                   处理后续Phase
```
