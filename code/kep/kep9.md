# 基于 Cluster-API 的 Kubernetes 集群自动化安装方案设计

## 一、整体架构
```
┌────────────────────────────────────────────────────────────────────┐
│                        Management Cluster                          │
│  ┌──────────┐  ┌──────────────────┐  ┌──────────────────────────┐  │
│  │  CAPI    │  │  Custom          │  │  Addon / App             │  │
│  │  Core    │  │  Providers       │  │  Controller              │  │
│  │          │  │  ├ InfraProvider │  │  ├ ClusterResourceSet    │  │
│  │  Cluster │  │  ├ CPProvider    │  │  ├ AppTopologyController │  │
│  │  Class   │  │  └ BootProvider  │  │  └ CertRotationController│  │
│  └──────────┘  └──────────────────┘  └──────────────────────────┘  │
│                                                                    │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │                     Custom CRDs                              │  │
│  │  BareMetalMachine │ EtcdCluster │ APIServerLB │ AppTopology  │  │
│  │  KubeletConfig    │ ContainerdConfig │ CertPolicy            │  │
│  └──────────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────────┘
                              │
                    ┌─────────┴─────────┐
                    │   Workload Cluster│
                    │  (Target K8s)     │
                    └───────────────────┘
```

## 二、核心 CRD 设计

### 2.1 机器清单 — `BareMetalMachineList`
用户提供的机器信息映射为 CAPI 的 `Machine` + 自定义 `BareMetalMachine`：
```yaml
# d:\code\github\installer-service\config\crd\baremetalmachine.yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: BareMetalMachine
metadata:
  name: node-01
  labels:
    cluster.x-k8s.io/cluster-name: my-cluster
    node-role.kubernetes.io/etcd: ""
    node-role.kubernetes.io/control-plane: ""
spec:
  hostname: master-01
  address: 192.168.1.10
  port: 22
  credentials:
    username: root
    passwordSecretRef:
      name: node-01-ssh-secret
      namespace: default
  os:
    distro: ubuntu22.04
  network:
    gateway: 192.168.1.1
    dnsServers:
      - 8.8.8.8
  kubeletExtraConfig:        # 节点级 kubelet 定制
    maxPods: 200
    systemReserved:
      cpu: "500m"
      memory: "1Gi"
  containerdExtraConfig:     # 节点级 containerd 定制
    maxConcurrentDownloads: 10
    registryMirrors:
      - mirror: registry.example.com
```

### 2.2 外部服务配置 — `InfrastructureServices`
```yaml
apiVersion: installer.cluster.x-k8s.io/v1alpha1
kind: InfrastructureServices
metadata:
  name: my-cluster-infra
spec:
  nfs:
    server: 192.168.1.100
    path: /data/share
  ntp:
    servers:
      - ntp1.example.com
      - ntp2.example.com
  registry:
    type: harbor
    endpoint: https://registry.example.com
    credentialsSecretRef:
      name: registry-creds
  binarySource:
    endpoint: https://repo.example.com/kubernetes
  chartRepository:
    endpoint: https://charts.example.com
    credentialsSecretRef:
      name: chart-repo-creds
  loadBalancer:
    type: external
    apiServerEndpoint: 192.168.1.50:6443
    healthCheck:
      interval: 5s
      threshold: 3
```

### 2.3 控制面组件 — `EtcdCluster`
```yaml
apiVersion: controlplane.cluster.x-k8s.io/v1alpha1
kind: EtcdCluster
metadata:
  name: my-cluster-etcd
spec:
  mode: external                          # external = 外接 etcd 集群
  version: "3.5.9"
  replicas: 3
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/etcd: ""
  externalConfig:                         # 外接 etcd 配置
    endpoints:
      - https://192.168.1.20:2379
      - https://192.168.1.21:2379
      - https://192.168.1.22:2379
    caCertSecretRef:
      name: etcd-ca-cert
    clientCertSecretRef:
      name: etcd-client-cert
  autoInstall: true                       # 自动化安装 etcd 到指定节点
  dataDir: /var/lib/etcd
  resources:
    requests:
      cpu: "500m"
      memory: "1Gi"
  backup:
    enabled: true
    interval: 6h
    storage:
      nfs:
        server: 192.168.1.100
        path: /backup/etcd
```

### 2.4 API Server 扩缩容 — `APIServerPool`
```yaml
apiVersion: controlplane.cluster.x-k8s.io/v1alpha1
kind: APIServerPool
metadata:
  name: my-cluster-apiserver
spec:
  version: "1.28.0"
  replicas: 3
  minReplicas: 2
  maxReplicas: 5
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/control-plane: ""
  loadBalancer:
    type: external
    endpoint: 192.168.1.50:6443
    backendPort: 6443
  autoScaling:
    enabled: true
    cpuTargetUtilization: 70
    metrics:
      - type: Resource
        resource:
          name: cpu
          target:
            type: Utilization
            averageUtilization: 70
  auditPolicy:
    logMaxAge: 30
    logMaxSize: 200
```

### 2.5 Scheduler / Controller-Manager — `ControlPlaneComponent`
```yaml
apiVersion: controlplane.cluster.x-k8s.io/v1alpha1
kind: ControlPlaneComponent
metadata:
  name: my-cluster-scheduler
spec:
  component: scheduler
  version: "1.28.0"
  mode: active-standby                   # 主备模式
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/scheduler: ""
  leaderElection:
    leaseDuration: 15s
    renewDeadline: 10s
    retryPeriod: 2s
  extraArgs:
    bind-address: "0.0.0.0"
---
apiVersion: controlplane.cluster.x-k8s.io/v1alpha1
kind: ControlPlaneComponent
metadata:
  name: my-cluster-controller-manager
spec:
  component: controller-manager
  version: "1.28.0"
  mode: active-standby
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/controller-manager: ""
  leaderElection:
    leaseDuration: 15s
    renewDeadline: 10s
    retryPeriod: 2s
```

### 2.6 节点级定制 — `KubeletConfig` / `ContainerdConfig`
```yaml
apiVersion: bootstrap.cluster.x-k8s.io/v1alpha1
kind: KubeletConfig
metadata:
  name: high-perf-kubelet
spec:
  nodeSelector:
    matchLabels:
      node-performance: high
  config:
    maxPods: 500
    kubeAPIQPS: 100
    kubeAPIBurst: 200
    systemReserved:
      cpu: "1"
      memory: "2Gi"
    kubeReserved:
      cpu: "500m"
      memory: "1Gi"
    evictionHard:
      memory.available: "200Mi"
      nodefs.available: "10%"
---
apiVersion: bootstrap.cluster.x-k8s.io/v1alpha1
kind: ContainerdConfig
metadata:
  name: high-perf-containerd
spec:
  nodeSelector:
    matchLabels:
      node-performance: high
  config:
    maxConcurrentDownloads: 20
    maxConcurrentUploads: 10
    registry:
      mirrors:
        "docker.io":
          endpoint: ["https://registry.example.com/docker"]
      configs:
        "registry.example.com":
          auth:
            usernameSecretRef:
              name: registry-creds
              key: username
```

### 2.7 应用拓扑安装 — `AppTopology`
```yaml
apiVersion: addons.cluster.x-k8s.io/v1alpha1
kind: AppTopology
metadata:
  name: my-cluster-apps
spec:
  clusterName: my-cluster
  apps:
    - name: calico
      type: helm
      chart: calico
      repo: https://charts.example.com
      version: 3.26.0
      namespace: kube-system
      valuesFrom:
        configMapRef: calico-values
      dependsOn: []                       # 无依赖，第一层
      phase: cni

    - name: coredns
      type: helm
      chart: coredns
      repo: https://charts.example.com
      version: 1.25.0
      namespace: kube-system
      dependsOn:
        - calico                          # 依赖 CNI 就绪
      phase: dns

    - name: kube-proxy
      type: helm
      chart: kube-proxy
      repo: https://charts.example.com
      version: 0.0.1
      namespace: kube-system
      dependsOn: []
      phase: proxy

    - name: metrics-server
      type: helm
      chart: metrics-server
      repo: https://charts.example.com
      version: 3.11.0
      namespace: kube-system
      dependsOn:
        - coredns
        - kube-proxy
      phase: monitoring

    - name: prometheus-stack
      type: helm
      chart: kube-prometheus-stack
      repo: https://charts.example.com
      version: 50.0.0
      namespace: monitoring
      dependsOn:
        - metrics-server
      phase: observability
```

拓扑排序后的安装层级：
```
Layer 0:  calico, kube-proxy              (无依赖)
Layer 1:  coredns                          (依赖 calico)
Layer 2:  metrics-server                   (依赖 coredns + kube-proxy)
Layer 3:  prometheus-stack                 (依赖 metrics-server)
```

### 2.8 证书轮转策略 — `CertRotationPolicy`
```yaml
apiVersion: certs.cluster.x-k8s.io/v1alpha1
kind: CertRotationPolicy
metadata:
  name: my-cluster-certs
spec:
  clusterName: my-cluster
  ca:
    validity: 87600h                       # 10 年
    rotation:
      enabled: true
      notifyBeforeExpiry: 720h             # 到期前 30 天通知
      autoRotateBefore: 360h               # 到期前 15 天自动轮转
  certs:
    - name: apiserver
      validity: 8760h                      # 1 年
      rotation:
        enabled: true
        autoRotateBefore: 720h
    - name: apiserver-kubelet-client
      validity: 8760h
      rotation:
        enabled: true
        autoRotateBefore: 720h
    - name: etcd-server
      validity: 8760h
      rotation:
        enabled: true
        autoRotateBefore: 720h
    - name: etcd-peer
      validity: 8760h
      rotation:
        enabled: true
        autoRotateBefore: 720h
    - name: front-proxy-client
      validity: 8760h
      rotation:
        enabled: true
        autoRotateBefore: 720h
  webhook:
    enabled: true
    failurePolicy: Fail
```

## 三、Provider 设计

### 3.1 Infrastructure Provider — `CAPBM` (Cluster API Provider Bare Metal)
**职责**：管理已装好 OS 的裸金属机器的生命周期
```
┌─────────────────────────────────────────────────┐
│              BareMetalMachine Controller         │
│                                                  │
│  Reconcile Loop:                                 │
│  1. SSH 连接目标机器，验证可达性                  │
│  2. 检查 OS 版本、内核版本                        │
│  3. 配置 NTP / 主机名 / 网络                     │
│  4. 安装 containerd / kubeadm / kubelet          │
│  5. 返回 Machine.Status.Ready = true             │
│  6. 机器删除时执行清理                            │
└─────────────────────────────────────────────────┘
```

关键逻辑：
```go
type BareMetalMachineReconciler struct {
    client.Client
    SSHClientFactory SSHClientFactory
}

func (r *BareMetalMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    bm := &infrastructurev1beta1.BareMetalMachine{}
    if err := r.Get(ctx, req.NamespacedName, bm); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    machine, err := util.GetOwnerMachine(ctx, r.Client, bm.ObjectMeta)
    if err != nil {
        return ctrl.Result{}, err
    }

    switch bm.Status.Ready {
    case false:
        sshClient, err := r.SSHClientFactory.New(bm.Spec.Address, bm.Spec.Credentials)
        if err != nil {
            return ctrl.Result{}, fmt.Errorf("SSH connect failed: %w", err)
        }
        defer sshClient.Close()

        if err := r.provisionMachine(ctx, sshClient, bm, machine); err != nil {
            bm.Status.ErrorMessage = err.Error()
            r.Status().Update(ctx, bm)
            return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
        }

        bm.Status.Ready = true
        bm.Status.Address = bm.Spec.Address
        r.Status().Update(ctx, bm)
    }

    return ctrl.Result{}, nil
}

func (r *BareMetalMachineReconciler) provisionMachine(ctx context.Context, ssh SSHClient, bm *infrastructurev1beta1.BareMetalMachine, machine *clusterv1.Machine) error {
    if err := ssh.ConfigureHostname(bm.Spec.Hostname); err != nil {
        return fmt.Errorf("configure hostname: %w", err)
    }
    if err := ssh.ConfigureNTP(bm.Spec.NTP); err != nil {
        return fmt.Errorf("configure NTP: %w", err)
    }
    if err := ssh.InstallRuntime(bm.Spec.ContainerdExtraConfig); err != nil {
        return fmt.Errorf("install containerd: %w", err)
    }
    if err := ssh.InstallKubeadm(machine.Spec.Version); err != nil {
        return fmt.Errorf("install kubeadm: %w", err)
    }
    return nil
}
```

### 3.2 Control Plane Provider — 自定义 `GranularControlPlane`
**核心思路**：不使用默认的 `KubeadmControlPlane`（它将所有控制面组件打包），而是拆分为独立管理的子组件。
```
┌────────────────────────────────────────────────────────────────┐
│              GranularControlPlane Controller                   │
│                                                                │
│  管理的生命周期：                                               │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐       │
│  │ Etcd     │  │APIServer │  │Scheduler │  │CtrlMgr   │       │
│  │ Cluster  │  │ Pool     │  │ Component│  │Component │       │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘       │
│       │             │             │              │            │
│       ▼             ▼             ▼              ▼            │
│  独立扩缩容    独立扩缩容    主备选举      主备选举              │
│  标签选择      标签选择      标签选择      标签选择              │
│  外接/内置     LB 集成                                         │
└───────────────────────────────────────────────────────────────┘
```

```go
type GranularControlPlaneReconciler struct {
    client.Client
}

func (r *GranularControlPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    gcp := &controlplanev1alpha1.GranularControlPlane{}
    if err := r.Get(ctx, req.NamespacedName, gcp); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    cluster, err := util.GetClusterFromMetadata(ctx, r.Client, gcp.ObjectMeta)
    if err != nil {
        return ctrl.Result{}, err
    }

    if !cluster.Status.InfrastructureReady {
        return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
    }

    // Phase 1: Etcd 就绪
    if err := r.reconcileEtcd(ctx, gcp, cluster); err != nil {
        return ctrl.Result{}, err
    }

    // Phase 2: API Server 就绪
    if err := r.reconcileAPIServer(ctx, gcp, cluster); err != nil {
        return ctrl.Result{}, err
    }

    // Phase 3: Scheduler + Controller Manager
    if err := r.reconcileScheduler(ctx, gcp, cluster); err != nil {
        return ctrl.Result{}, err
    }
    if err := r.reconcileControllerManager(ctx, gcp, cluster); err != nil {
        return ctrl.Result{}, err
    }

    gcp.Status.Ready = r.allComponentsReady(gcp)
    r.Status().Update(ctx, gcp)

    return ctrl.Result{}, nil
}
```

### 3.3 Bootstrap Provider — `KubeadmBootstrapWithConfig`
**职责**：在 `KubeadmConfig` 基础上，注入节点级 `KubeletConfig` / `ContainerdConfig`
```go
type KubeadmBootstrapWithConfigReconciler struct {
    client.Client
}

func (r *KubeadmBootstrapWithConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    kbc := &bootstrapv1alpha1.KubeadmBootstrapWithConfig{}
    if err := r.Get(ctx, req.NamespacedName, kbc); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    machine, err := util.GetOwnerMachine(ctx, r.Client, kbc.ObjectMeta)
    if err != nil {
        return ctrl.Result{}, err
    }

    bm := &infrastructurev1beta1.BareMetalMachine{}
    if err := r.Get(ctx, types.NamespacedName{
        Name:      machine.Spec.InfrastructureRef.Name,
        Namespace: machine.Namespace,
    }, bm); err != nil {
        return ctrl.Result{}, err
    }

    // 合并节点级 Kubelet 配置
    kubeletConfig, err := r.resolveKubeletConfig(ctx, bm)
    if err != nil {
        return ctrl.Result{}, err
    }

    // 合并节点级 Containerd 配置
    containerdConfig, err := r.resolveContainerdConfig(ctx, bm)
    if err != nil {
        return ctrl.Result{}, err
    }

    // 生成 cloud-init / kubeadm join config
    joinConfig, err := r.generateJoinConfig(kbc, kubeletConfig, containerdConfig)
    if err != nil {
        return ctrl.Result{}, err
    }

    kbc.Status.BootstrapData = joinConfig
    kbc.Status.Ready = true
    r.Status().Update(ctx, kbc)

    return ctrl.Result{}, nil
}

func (r *KubeadmBootstrapWithConfigReconciler) resolveKubeletConfig(ctx context.Context, bm *infrastructurev1beta1.BareMetalMachine) (*kubeletv1alpha1.KubeletConfiguration, error) {
    configs := &kubeletv1alpha1.KubeletConfigList{}
    if err := r.List(ctx, configs, client.MatchingLabels(bm.Labels)); err != nil {
        return nil, err
    }
    if len(configs.Items) == 0 {
        return nil, nil
    }
    merged := &kubeletv1alpha1.KubeletConfiguration{}
    for _, c := range configs.Items {
        mergeKubeletConfig(merged, &c.Spec.Config)
    }
    return merged, nil
}
```

## 四、扩展机制设计

### 4.1 CNI / CoreDNS / Kube-Proxy — 通过 `ClusterResourceSet` + `AppTopology`
利用 CAPI 原生的 `ClusterResourceSet` 机制，结合自定义 `AppTopology` 控制器：
```
┌──────────────────────────────────────────────────────────┐
│              AppTopology Controller                      │
│                                                          │
│  1. 读取 AppTopology CR，构建 DAG                         │
│  2. 拓扑排序，得到安装层级                                 │
│  3. 逐层安装：                                            │
│     Layer 0 → 检查就绪 → Layer 1 → 检查就绪 → ...         │
│  4. 每个应用创建对应的 ClusterResourceSet                 │
│  5. 健康检查通过后标记 App.Status.Phase = Ready            │
└──────────────────────────────────────────────────────────┘
```

```go
type AppTopologyReconciler struct {
    client.Client
    HelmClient HelmClient
}

type AppNode struct {
    Name       string
    DependsOn  []string
    Phase      string
    Layer      int
}

func (r *AppTopologyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    at := &addonsv1alpha1.AppTopology{}
    if err := r.Get(ctx, req.NamespacedName, at); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    dag := r.buildDAG(at.Spec.Apps)
    layers := r.topologicalSort(dag)

    for layerIdx, layer := range layers {
        for _, app := range layer {
            if app.Phase == "Ready" {
                continue
            }
            if !r.dependenciesReady(app, dag) {
                return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
            }

            if err := r.installApp(ctx, at.Spec.ClusterName, app); err != nil {
                app.Phase = "Failed"
                r.Status().Update(ctx, at)
                return ctrl.Result{}, err
            }

            if r.isAppHealthy(ctx, at.Spec.ClusterName, app) {
                app.Phase = "Ready"
            } else {
                return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
            }
        }
        _ = layerIdx
    }

    at.Status.Phase = "Completed"
    r.Status().Update(ctx, at)
    return ctrl.Result{}, nil
}

func (r *AppTopologyReconciler) buildDAG(apps []addonsv1alpha1.AppSpec) map[string]*AppNode {
    dag := make(map[string]*AppNode)
    for _, app := range apps {
        dag[app.Name] = &AppNode{
            Name:      app.Name,
            DependsOn: app.DependsOn,
            Phase:     "Pending",
        }
    }
    return dag
}

func (r *AppTopologyReconciler) topologicalSort(dag map[string]*AppNode) [][]*AppNode {
    inDegree := make(map[string]int)
    for name, node := range dag {
        inDegree[name] = len(node.DependsOn)
    }

    var layers [][]*AppNode
    for {
        var currentLayer []*AppNode
        for name, deg := range inDegree {
            if deg == 0 {
                currentLayer = append(currentLayer, dag[name])
                delete(inDegree, name)
            }
        }
        if len(currentLayer) == 0 {
            break
        }
        for _, node := range currentLayer {
            for name, n := range dag {
                for _, dep := range n.DependsOn {
                    if dep == node.Name {
                        inDegree[name]--
                    }
                }
            }
        }
        layers = append(layers, currentLayer)
    }
    return layers
}
```

### 4.2 ClusterResourceSet 集成
```yaml
apiVersion: addons.cluster.x-k8s.io/v1beta1
kind: ClusterResourceSet
metadata:
  name: calico-crs
  labels:
    app-topology.cluster.x-k8s.io/name: my-cluster-apps
    app-topology.cluster.x-k8s.io/app: calico
    app-topology.cluster.x-k8s.io/phase: cni
spec:
  clusterSelector:
    matchLabels:
      cluster.x-k8s.io/cluster-name: my-cluster
  resources:
    - name: calico-manifest
      kind: ConfigMap
    - name: calico-values
      kind: Secret
  strategy: Reconcile
```

## 五、证书自动轮转设计

### 5.1 架构
```
┌─────────────────────────────────────────────────────────────┐
│              CertRotation Controller                        │
│                                                             │
│  ┌─────────────┐    ┌──────────────┐    ┌───────────────┐  │
│  │ Cert Watcher│───▶│ Expiry Check │──▶│ Rotate Action │  │
│  │ (定期扫描)   │    │ (剩余时间判断)│    │ (生成+分发)    │  │
│  └─────────────┘    └──────────────┘    └───────────────┘  │
│         │                                       │          │
│         │              ┌──────────────┐         │          │
│         └──────────────│ Event Record │◀───────┘          │
│                        │ (通知/告警)   │                    │
│                        └──────────────┘                     │
└─────────────────────────────────────────────────────────────┘
```

### 5.2 Controller 实现
```go
type CertRotationReconciler struct {
    client.Client
    CertStore    CertStore
    Notifier     Notifier
}

func (r *CertRotationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    policy := &certsv1alpha1.CertRotationPolicy{}
    if err := r.Get(ctx, req.NamespacedName, policy); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    cluster, err := util.GetClusterFromMetadata(ctx, r.Client, policy.ObjectMeta)
    if err != nil {
        return ctrl.Result{}, err
    }

    now := time.Now()
    requeueAfter := time.Hour

    // 检查 CA 证书
    if policy.Spec.CA.Rotation.Enabled {
        caCert, err := r.CertStore.GetCACert(ctx, cluster.Name)
        if err != nil {
            return ctrl.Result{}, err
        }
        expiry := caCert.NotAfter
        rotateBefore := policy.Spec.CA.Rotation.AutoRotateBefore.Duration
        notifyBefore := policy.Spec.CA.Rotation.NotifyBeforeExpiry.Duration

        if now.After(expiry.Add(-rotateBefore)) {
            if err := r.rotateCA(ctx, cluster, policy); err != nil {
                return ctrl.Result{}, fmt.Errorf("rotate CA: %w", err)
            }
        } else if now.After(expiry.Add(-notifyBefore)) {
            r.Notifier.Notify(ctx, "CA cert expiring soon", cluster.Name, expiry)
        }

        nextCheck := expiry.Add(-rotateBefore).Sub(now) / 2
        if nextCheck < requeueAfter {
            requeueAfter = nextCheck
        }
    }

    // 检查各组件证书
    for _, certSpec := range policy.Spec.Certs {
        cert, err := r.CertStore.GetComponentCert(ctx, cluster.Name, certSpec.Name)
        if err != nil {
            continue
        }
        expiry := cert.NotAfter
        rotateBefore := certSpec.Rotation.AutoRotateBefore.Duration
        notifyBefore := certSpec.Rotation.NotifyBeforeExpiry.Duration

        if now.After(expiry.Add(-rotateBefore)) {
            if err := r.rotateComponentCert(ctx, cluster, certSpec.Name, policy); err != nil {
                return ctrl.Result{}, fmt.Errorf("rotate %s: %w", certSpec.Name, err)
            }
        } else if now.After(expiry.Add(-notifyBefore)) {
            r.Notifier.Notify(ctx, fmt.Sprintf("%s cert expiring soon", certSpec.Name), cluster.Name, expiry)
        }

        nextCheck := expiry.Add(-rotateBefore).Sub(now) / 2
        if nextCheck < requeueAfter {
            requeueAfter = nextCheck
        }
    }

    return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *CertRotationReconciler) rotateCA(ctx context.Context, cluster *clusterv1.Cluster, policy *certsv1alpha1.CertRotationPolicy) error {
    // 1. 生成新 CA（保留旧 CA 以支持滚动更新）
    newCA, err := r.CertStore.GenerateCA(ctx, cluster.Name, policy.Spec.CA.Validity.Duration)
    if err != nil {
        return err
    }

    // 2. 将新 CA 追加到 trust bundle（双 CA 共存期）
    if err := r.CertStore.AppendCAToTrustBundle(ctx, cluster.Name, newCA); err != nil {
        return err
    }

    // 3. 用新 CA 重新签发所有组件证书
    for _, certSpec := range policy.Spec.Certs {
        if err := r.rotateComponentCert(ctx, cluster, certSpec.Name, policy); err != nil {
            return err
        }
    }

    // 4. 分发新证书到所有节点
    if err := r.distributeCerts(ctx, cluster); err != nil {
        return err
    }

    // 5. 滚动重启控制面组件
    if err := r.rollingRestartControlPlane(ctx, cluster); err != nil {
        return err
    }

    // 6. 确认所有组件使用新 CA 后，移除旧 CA
    if err := r.CertStore.RemoveOldCAFromTrustBundle(ctx, cluster.Name); err != nil {
        return err
    }

    return nil
}

func (r *CertRotationReconciler) rotateComponentCert(ctx context.Context, cluster *clusterv1.Cluster, componentName string, policy *certsv1alpha1.CertRotationPolicy) error {
    // 1. 查找该组件的证书规格
    var certSpec *certsv1alpha1.CertSpec
    for _, c := range policy.Spec.Certs {
        if c.Name == componentName {
            certSpec = &c
            break
        }
    }
    if certSpec == nil {
        return fmt.Errorf("cert spec not found for %s", componentName)
    }

    // 2. 用当前 CA 签发新证书
    ca, err := r.CertStore.GetCurrentCA(ctx, cluster.Name)
    if err != nil {
        return err
    }
    newCert, err := r.CertStore.IssueCert(ctx, ca, componentName, certSpec.Validity.Duration)
    if err != nil {
        return err
    }

    // 3. 更新 Secret
    if err := r.CertStore.UpdateComponentCertSecret(ctx, cluster.Name, componentName, newCert); err != nil {
        return err
    }

    // 4. 触发组件热重载或滚动重启
    if err := r.restartComponent(ctx, cluster, componentName); err != nil {
        return err
    }

    return nil
}
```

### 5.3 证书轮转流程（双 CA 滚动更新）
```
时间线：
────────────────────────────────────────────────────────────────▶

  T0                T1                    T2                    T3
  │                 │                     │                     │
  │  通知：CA即将过期│  新CA生成            │  所有组件使用新CA    │  旧CA移除
  │                 │  双CA共存期开始      │  双CA共存期结束      │
  │                 │                     │                     │
  │  [旧CA签发]     │  [新CA+旧CA同时信任]  │  [新CA签发]         │  [仅新CA]
  │                │  组件逐步重启         │                     │
```

## 六、完整工作流
```
用户提交 Cluster 定义
        │
        ▼
┌─────────────────────┐
│ 1. InfraServices    │ ─── 配置 NFS/NTP/Registry/LB 等
│    Controller       │
└─────────┬───────────┘
          │
          ▼
┌─────────────────────┐
│ 2. BareMetalMachine │ ─── SSH 连接、配置主机名/网络/运行时
│    Controller       │     安装 containerd/kubeadm/kubelet
└─────────┬───────────┘
          │
          ▼
┌─────────────────────┐
│ 3. EtcdCluster      │ ─── 外接/内置 etcd 部署
│    Controller       │     标签选择节点 → 自动安装
└─────────┬───────────┘
          │
          ▼
┌─────────────────────┐
│ 4. APIServerPool    │ ─── API Server 部署 + LB 注册
│    Controller       │     支持扩缩容
└─────────┬───────────┘
          │
          ▼
┌─────────────────────────────────┐
│ 5. Scheduler / ControllerManager│ ─── 主备部署
│    Controller                   │
└─────────┬───────────────────────┘
          │
          ▼
┌─────────────────────┐
│ 6. Bootstrap        │ ─── 生成 join config，注入节点级定制
│    Controller       │     (KubeletConfig / ContainerdConfig)
└─────────┬───────────┘
          │
          ▼
┌─────────────────────┐
│ 7. AppTopology      │ ─── 拓扑排序 → 逐层安装
│    Controller       │     Layer0: calico, kube-proxy
│                     │     Layer1: coredns
│                     │     Layer2: metrics-server
│                     │     Layer3: 用户应用...
└─────────┬───────────┘
          │
          ▼
┌─────────────────────┐
│ 8. CertRotation     │ ─── 持续监控证书有效期
│    Controller       │     自动轮转 + 通知
└─────────────────────┘
```

## 七、关键设计决策总结
| 需求 | 方案 | CAPI 机制 |
|------|------|-----------|
| 已装 OS 机器接入 | 自定义 `BareMetalMachine` CRD + Infrastructure Provider | Infrastructure Provider |
| 外部服务配置 | `InfrastructureServices` CRD，在 Bootstrap 阶段注入 | Cluster 配置扩展 |
| etcd 外接/内置 | `EtcdCluster` CRD，`mode: external/internal` | Control Plane Provider |
| etcd 标签选择安装 | `nodeSelector` 匹配 `node-role.kubernetes.io/etcd` 标签 | Machine 标签 |
| API Server 扩缩容 | `APIServerPool` CRD + HPA 语义 + LB 后端注册 | Control Plane Provider |
| Scheduler/CM 主备 | `ControlPlaneComponent` CRD + leader-election | Control Plane Provider |
| Kubelet 定制化 | `KubeletConfig` CRD，Bootstrap 阶段按标签合并 | Bootstrap Provider |
| Containerd 定制化 | `ContainerdConfig` CRD，Bootstrap 阶段按标签合并 | Bootstrap Provider |
| CNI/CoreDNS/Kube-Proxy | `AppTopology` + `ClusterResourceSet` | CAPI Addon 机制 |
| 应用拓扑安装 | DAG + 拓扑排序 + 逐层健康检查 | CAPI Addon 扩展 |
| 证书自动轮转 | `CertRotationPolicy` CRD + 双 CA 滚动更新 | 自定义 Controller |

## 八、API 汇总
| CRD | Group | 归属 Provider |
|-----|-------|---------------|
| `BareMetalMachine` | `infrastructure.cluster.x-k8s.io` | Infrastructure Provider |
| `InfrastructureServices` | `installer.cluster.x-k8s.io` | Infrastructure Provider |
| `EtcdCluster` | `controlplane.cluster.x-k8s.io` | Control Plane Provider |
| `APIServerPool` | `controlplane.cluster.x-k8s.io` | Control Plane Provider |
| `ControlPlaneComponent` | `controlplane.cluster.x-k8s.io` | Control Plane Provider |
| `GranularControlPlane` | `controlplane.cluster.x-k8s.io` | Control Plane Provider |
| `KubeletConfig` | `bootstrap.cluster.x-k8s.io` | Bootstrap Provider |
| `ContainerdConfig` | `bootstrap.cluster.x-k8s.io` | Bootstrap Provider |
| `AppTopology` | `addons.cluster.x-k8s.io` | Addon Provider |
| `CertRotationPolicy` | `certs.cluster.x-k8s.io` | Cert Provider |

以上方案完全遵循 Cluster-API 的 Provider 扩展模型，通过自定义 Infrastructure / Control Plane / Bootstrap 三类 Provider 以及 Addon 扩展机制，实现了所有需求的解耦与自动化。每个 CRD 对应独立的 Controller，职责单一，可独立演进和测试。

        
