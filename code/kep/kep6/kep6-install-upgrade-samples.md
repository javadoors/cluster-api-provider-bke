# KEP-6 安装与升级样例

**文档版本**: v1.3  
**状态**: Draft  
**依赖**: KEP-6 详细设计文档 (kep6-detailed-design.md)

---

## 目录

1. [安装样例](#1-安装样例)
   - 1.1 containerd 在线安装
   - 1.2 containerd 离线安装
   - 1.3 docker 安装
   - 1.4 bkeagent 安装
   - 1.5 cluster-api 部署
   - 1.6 bkeagent-switch 切换
   - 1.7 bke-manifests 目录结构
   - 1.8 ReleaseImage 样例
   - 1.9 安装执行流程
2. [升级样例](#2-升级样例)
   - 2.1 版本变更对比
   - 2.2 containerd 升级
   - 2.3 docker 升级
   - 2.4 bkeagent 升级
   - 2.5 ReleaseImage 样例
   - 2.6 升级执行流程
3. [回滚样例](#3-回滚样例)
   - 3.1 Binary 组件回滚
   - 3.2 Helm 组件回滚
4. [Feature Gate 兼容性](#4-feature-gate-兼容性)
   - 4.1 Feature Gate ON 路径
   - 4.2 Feature Gate OFF 路径
   - 4.3 混合模式
5. [关键设计点说明](#5-关键设计点说明)

---

## 1. 安装样例

### 1.1 containerd 在线安装

**场景**：新建集群，使用 containerd 作为容器运行时，在线模式（镜像仓库可访问）。

**集群配置**：
```yaml
apiVersion: config.openfuyao.cn/v1beta1
kind: BKECluster
metadata:
  name: test-cluster
  namespace: default
spec:
  clusterConfig:
    cluster:
      containerRuntime:
        cri: containerd
      imageRepo:
        domain: registry.example.com
        authSecretRef:
          name: registry-secret
```

**containerd ComponentVersion YAML**：
```yaml
# bke-manifests/containerd/v1.7.18/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.18
spec:
  name: containerd
  type: binary
  version: v1.7.18

  binary:
    variables:
      logLevel: "info"
      snapshotter: "overlayfs"
      sandboxImage: "{{.Config.Cluster.ImageRepo.Domain}}/pause:3.9"

    artifacts:
      - name: containerd
        url: "{{.Config.Cluster.ImageRepo.Domain}}/binaries/containerd/{{version}}/containerd-{{version}}-linux-{{arch}}.tar.gz"
        checksum: "sha256:abc123def456..."
        installPath: "/"

    configTemplates:
      - name: config.toml
        path: "/etc/containerd/config.toml"
        mode: "0644"
        content: |
          version = 2
          root = "/var/lib/containerd"
          state = "/run/containerd"
          
          [plugins."io.containerd.grpc.v1.cri"]
            sandbox_image = "{{.Variables.sandboxImage}}"
            [plugins."io.containerd.grpc.v1.cri".containerd]
              snapshotter = "{{.Variables.snapshotter}}"
            [plugins."io.containerd.grpc.v1.cri".registry]
              config_path = "/etc/containerd/certs.d"

      - name: containerd.service
        path: "/etc/systemd/system/containerd.service"
        mode: "0644"
        content: |
          [Unit]
          Description=containerd container runtime
          After=network.target

          [Service]
          ExecStart=/usr/local/bin/containerd
          Restart=always

          [Install]
          WantedBy=multi-user.target

      # 在线模式：仅为 imageRepo 生成 hosts.toml
      - name: hosts.toml
        path: "/etc/containerd/certs.d/{{.Config.Cluster.ImageRepo.Domain}}/hosts.toml"
        mode: "0644"
        content: |
          server = "https://{{.Config.Cluster.ImageRepo.Domain}}"
          
          [host."https://{{.Config.Cluster.ImageRepo.Domain}}"]
            capabilities = ["pull", "resolve", "push"]
            skip_verify = true

    installScript: |
      #!/bin/bash
      set -e
      
      # 停止旧服务
      systemctl stop containerd || true
      
      # 解压安装
      tar -xzf {{.Artifacts.containerd.Path}} -C /
      chmod +x /usr/local/bin/containerd
      
      # 启动
      systemctl daemon-reload
      systemctl enable containerd
      systemctl start containerd
      
      # 验证
      containerd --version

    healthCheck:
      enabled: true
      timeout: "2m"
      script: |
        systemctl is-active containerd
        containerd --version | grep -q "{{.ComponentVersion}}"

  dependencies:
    - name: bkeagent
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```

**Selector ComponentVersion（容器运行时选择）**：
```yaml
# bke-manifests/container-runtime/v1.0.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: container-runtime-v1.0.0
spec:
  name: container-runtime
  type: selector
  version: v1.0.0

  subComponents:
    - name: containerd
      version: v1.7.18
      condition: "{{.Config.Cluster.ContainerRuntime.CRI == \"containerd\"}}"
    - name: docker
      version: v26.0.0
      condition: "{{.Config.Cluster.ContainerRuntime.CRI == \"docker\"}}"
    - name: cri-dockerd
      version: v0.3.9
      condition: "{{.Config.Cluster.ContainerRuntime.CRI == \"docker\"}}"
```

**执行流程**：
```
1. DAG 构建器加载 container-runtime selector
2. 评估 condition: Config.Cluster.ContainerRuntime.CRI == "containerd" → true
3. 展开为 containerd/v1.7.18 节点
4. BinaryComponentExecutor 执行:
   - 下载 containerd-1.7.18-linux-amd64.tar.gz
   - 渲染 config.toml（sandboxImage = registry.example.com/pause:3.9）
   - 渲染 containerd.service
   - 生成 hosts.toml（仅 imageRepo）
   - SSH 上传到节点
   - 执行 installScript
   - 健康检查通过
```

---

### 1.2 containerd 离线安装

**场景**：新建集群，使用 containerd 作为容器运行时，离线模式（需为公共仓库生成 hosts.toml 重定向）。

**集群配置**：
```yaml
apiVersion: config.openfuyao.cn/v1beta1
kind: BKECluster
metadata:
  name: offline-cluster
  namespace: default
spec:
  clusterConfig:
    cluster:
      containerRuntime:
        cri: containerd
        registry:
          configs:
            registry.example.com:
              host: registry.example.com
              skipVerify: true
            docker.io:
              host: docker.io
              skipVerify: true
            ghcr.io:
              host: ghcr.io
              skipVerify: true
      imageRepo:
        domain: registry.example.com
```

**关键差异**：离线模式需要为多个公共仓库生成 hosts.toml，将请求重定向到私有仓库。

**hosts.toml 生成结果**：
```
/etc/containerd/certs.d/
├── registry.example.com/
│   └── hosts.toml          # imageRepo 本身
├── docker.io/
│   └── hosts.toml          # 重定向到 registry.example.com
├── ghcr.io/
│   └── hosts.toml          # 重定向到 registry.example.com
├── quay.io/
│   └── hosts.toml          # 重定向到 registry.example.com
└── registry.k8s.io/
    └── hosts.toml          # 重定向到 registry.example.com
```

**hosts.toml 内容示例**：
```toml
# /etc/containerd/certs.d/docker.io/hosts.toml
server = "https://docker.io"

[host."https://registry.example.com"]
  capabilities = ["pull", "resolve"]
  skip_verify = true
```

---

### 1.3 docker 安装

**场景**：新建集群，使用 docker 作为容器运行时（K8s >= 1.24，需安装 cri-dockerd）。

**集群配置**：
```yaml
apiVersion: config.openfuyao.cn/v1beta1
kind: BKECluster
metadata:
  name: docker-cluster
  namespace: default
spec:
  clusterConfig:
    cluster:
      containerRuntime:
        cri: docker
        param:
          data-root: "/var/lib/docker"
          cgroup-driver: "systemd"
      imageRepo:
        domain: registry.example.com
      kubernetesVersion: "1.29.0"
```

**docker ComponentVersion YAML**：
```yaml
# bke-manifests/docker/v26.0.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: docker-v26.0.0
spec:
  name: docker
  type: binary
  version: v26.0.0

  binary:
    variables:
      cgroupDriver: "systemd"
      dataRoot: "/var/lib/docker"
      lowLevelRuntime: "runc"
      lowLevelRuntimePath: "/usr/local/bin/runc"
      registryMirrors: "https://registry.example.com"
      insecureRegistries: ""

    # Docker 通过包管理器安装，无 artifacts

    installScript: |
      #!/bin/bash
      set -e
      
      systemctl stop docker || true
      systemctl stop docker.socket || true
      
      # 通过包管理器安装
      if [ -f /etc/redhat-release ]; then
        yum install -y yum-utils
        yum install -y docker-ce
      elif [ -f /etc/os-release ] && grep -q ubuntu /etc/os-release; then
        apt-get update
        apt-get install -y docker-ce
      fi
      
      systemctl daemon-reload
      systemctl enable docker
      systemctl restart docker
      
      docker --version

    uninstallScript: |
      #!/bin/bash
      systemctl stop docker || true
      systemctl disable docker || true
      rm -f /usr/bin/docker /usr/bin/dockerd
      rm -f /usr/lib/systemd/system/docker.service
      rm -rf /etc/docker/
      systemctl daemon-reload

    configTemplates:
      - name: daemon.json
        path: "/etc/docker/daemon.json"
        mode: "0644"
        content: |
          {
            "exec-opts": ["native.cgroupdriver={{.Variables.cgroupDriver}}"],
            "data-root": "{{.Variables.dataRoot}}",
            "runtimes": {
              "{{.Variables.lowLevelRuntime}}": {"path": "{{.Variables.lowLevelRuntimePath}}"}
            }{{if .Variables.registryMirrors}},
            "registry-mirrors": ["{{.Variables.registryMirrors}}"]
            {{end}}
          }

    healthCheck:
      enabled: true
      timeout: "2m"
      script: |
        systemctl is-active docker
        docker --version | grep -q "{{.ComponentVersion}}"
        docker info > /dev/null 2>&1

  compatibility:
    constraints:
      - component: kubernetes-master
        rule: ">=1.24.0"

  dependencies:
    - name: bkeagent
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```

**cri-dockerd ComponentVersion YAML**：
```yaml
# bke-manifests/cri-dockerd/v0.3.9/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: cri-dockerd-v0.3.9
spec:
  name: cri-dockerd
  type: binary
  version: v0.3.9

  binary:
    variables:
      sandboxImage: "{{.Config.Cluster.ImageRepo.Domain}}/pause:3.9"

    artifacts:
      - name: cri-dockerd
        url: "{{.Config.Cluster.ImageRepo.Domain}}/cri-dockerd/{{version}}/cri-dockerd-{{version}}-{{arch}}"
        checksum: "sha256:cri-dockerd-checksum-placeholder"
        installPath: "/usr/bin"

    configTemplates:
      - name: cri-dockerd.service
        path: "/etc/systemd/system/cri-dockerd.service"
        mode: "0644"
        content: |
          [Unit]
          Description=CRI Interface for Docker
          After=network-online.target docker.service
          Requires=docker.service

          [Service]
          Type=notify
          ExecStart=/usr/bin/cri-dockerd --container-runtime-endpoint fd:// --pod-infra-container-image {{.Variables.sandboxImage}}
          Restart=always

          [Install]
          WantedBy=multi-user.target

      - name: cri-dockerd.socket
        path: "/etc/systemd/system/cri-dockerd.socket"
        mode: "0644"
        content: |
          [Unit]
          Description=CRI Docker Socket

          [Socket]
          ListenStream=/var/run/cri-dockerd.sock
          SocketMode=0660
          SocketUser=root
          SocketGroup=docker

          [Install]
          WantedBy=sockets.target

    installScript: |
      #!/bin/bash
      set -e
      
      systemctl stop cri-dockerd || true
      systemctl stop cri-dockerd.socket || true
      
      # 安装二进制
      install -m 0755 {{.Artifacts.cri-dockerd.Path}} /usr/bin/cri-dockerd
      
      # 安装依赖
      if [ -f /etc/redhat-release ]; then
        yum install -y socat || true
      elif [ -f /etc/os-release ] && grep -q ubuntu /etc/os-release; then
        apt-get install -y socat || true
      fi
      
      systemctl daemon-reload
      systemctl enable cri-dockerd
      systemctl start cri-dockerd

    uninstallScript: |
      #!/bin/bash
      systemctl stop cri-dockerd || true
      systemctl disable cri-dockerd || true
      rm -f /usr/bin/cri-dockerd
      rm -f /etc/systemd/system/cri-dockerd.service
      rm -f /etc/systemd/system/cri-dockerd.socket
      systemctl daemon-reload

    healthCheck:
      enabled: true
      timeout: "1m"
      script: |
        systemctl is-active cri-dockerd
        cri-dockerd --version | grep -q "{{.ComponentVersion}}"

  dependencies:
    - name: docker
      phase: Install

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "5m"
    failurePolicy: FailFast
```

**执行流程**：
```
1. DAG 构建器加载 container-runtime selector
2. 评估 condition: Config.Cluster.ContainerRuntime.CRI == "docker" → true
3. 展开为 docker/v26.0.0 + cri-dockerd/v0.3.9 两个节点
4. DAG 依赖关系: docker → cri-dockerd
5. BinaryComponentExecutor 执行:
   - docker: 包管理器安装 + daemon.json 配置
   - cri-dockerd: 二进制下载 + service/socket 配置
```

---

### 1.4 bkeagent 安装

**场景**：新建集群，安装 bkeagent 到所有节点。

**bkeagent ComponentVersion YAML**：
```yaml
# bke-manifests/bkeagent/v2.6.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: bkeagent-v2.6.0
spec:
  name: bkeagent
  type: binary
  version: v2.6.0

  binary:
    artifacts:
      - name: bkeagent
        url: "{{.Config.Cluster.ImageRepo.Domain}}/bkeagent/{{version}}/bkeagent_linux_{{arch}}"
        checksum: "sha256:xyz789abc012..."
        installPath: "/usr/local/bin"

    configTemplates:
      # 节点标识文件
      - name: node
        path: "/etc/openFuyao/bkeagent/node"
        mode: "0644"
        content: "{{.NodeHostname}}"

      # TLS 证书（从 Secret 获取）
      - name: trust-chain.crt
        path: "/etc/openFuyao/certs/trust-chain.crt"
        mode: "0644"
        secretRef:
          name: bkeagent-tls
          namespace: "{{.Namespace}}"
          key: trust-chain.crt

      - name: global-ca.crt
        path: "/etc/openFuyao/certs/global-ca.crt"
        mode: "0644"
        secretRef:
          name: bkeagent-tls
          namespace: "{{.Namespace}}"
          key: global-ca.crt

      # Kubeconfig（管理集群 admin kubeconfig）
      - name: kubeconfig
        path: "/etc/openFuyao/bkeagent/config"
        mode: "0600"
        secretRef:
          name: localkubeconfig
          namespace: kube-system
          key: config

      # systemd service
      - name: bkeagent.service
        path: "/etc/systemd/system/bkeagent.service"
        mode: "0644"
        content: |
          [Unit]
          Description=BKE Agent
          After=network.target

          [Service]
          ExecStart=/usr/local/bin/bkeagent --kubeconfig=/etc/openFuyao/bkeagent/config
          Restart=always
          RestartSec=5

          [Install]
          WantedBy=multi-user.target

    installScript: |
      #!/bin/bash
      set -e
      
      # 创建目录
      mkdir -p /etc/openFuyao/bkeagent
      mkdir -p /etc/openFuyao/certs
      
      # 停止旧服务
      systemctl stop bkeagent || true
      
      # 安装二进制
      install -m 0755 {{.Artifacts.bkeagent.Path}} /usr/local/bin/bkeagent
      
      # 启动
      systemctl daemon-reload
      systemctl enable bkeagent
      systemctl restart bkeagent
      
      # 验证
      sleep 2
      systemctl is-active bkeagent

    healthCheck:
      enabled: true
      timeout: "1m"
      script: |
        systemctl is-active bkeagent
        /usr/local/bin/bkeagent --version | grep -q "{{.ComponentVersion}}"

  dependencies: []

  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    timeout: "5m"
    failurePolicy: FailFast
```

**执行流程**：
```
1. BinaryComponentExecutor 执行:
   - 下载 bkeagent_linux_amd64（注意：无版本号）
   - 渲染 node 文件（NodeHostname）
   - 从 Secret 获取 TLS 证书
   - 从 Secret 获取 kubeconfig
   - 渲染 bkeagent.service
   - SSH 上传到节点
   - 执行 installScript
   - 健康检查通过
```

---

### 1.5 cluster-api 部署

**场景**：部署 cluster-api 组件，用于管理目标集群的生命周期，并创建目标集群的 kubeconfig Secret。

**cluster-api ComponentVersion YAML**：
```yaml
# bke-manifests/cluster-api/v1.5.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: cluster-api-v1.5.0
spec:
  name: cluster-api
  type: helm
  version: v1.5.0

  helm:
    chart:
      oci:
        repository: "registry.example.com/charts/cluster-api"
        tag: "v1.5.0"
      checksum: "sha256:cluster-api-checksum..."
    namespace: cluster-api-system
    releaseName: cluster-api
    values:
      image:
        repository: "registry.example.com/cluster-api"
        tag: "{{componentVersion}}"
    strategy:
      mode: Install
      wait: true
      waitTimeout: "5m"
      atomic: true
    healthCheck:
      enabled: true
      timeout: "3m"
      checks:
        - type: PodReady
          podReady:
            namespace: cluster-api-system
            labelSelector: "app=cluster-api"
            minReady: 1

  upgradeStrategy:
    mode: Parallel
    failurePolicy: FailFast
    timeout: "10m"
```

**执行流程**：
```
1. HelmComponentExecutor 执行:
   - 从 OCI Registry 拉取 cluster-api Chart
   - 渲染 Values（image.tag = componentVersion）
   - helm install --namespace cluster-api-system --atomic --wait
   - 等待 Pod Ready（cluster-api-system 命名空间）
   - 健康检查通过

2. 创建目标集群 kubeconfig Secret:
   - Secret 名称: {{clusterName}}-kubeconfig
   - 命名空间: {{clusterNamespace}}
   - 内容: 目标集群的 admin kubeconfig
   - 用途: bkeagent-switch 组件读取此 Secret 切换到目标集群
```

---

### 1.6 bkeagent-switch 切换

**场景**：cluster-api 部署完成后，切换 bkeagent 的监听目标从管理集群切换到目标集群。

**触发条件**：
- cluster-api addon 部署完成
- BKECluster 注解 `bke.bocloud.com/bkeagent-listener: bkecluster` 已设置

**bkeagent-switch ComponentVersion YAML**：
```yaml
# bke-manifests/bkeagent-switch/v2.6.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: bkeagent-switch-v2.6.0
spec:
  name: bkeagent-switch
  type: binary
  version: v2.6.0

  binary:
    # 无需下载制品（bkeagent 已安装）
    artifacts: []

    configTemplates:
      # 目标集群 kubeconfig（从 cluster-api 创建的 Secret 获取）
      - name: kubeconfig
        path: "/etc/openFuyao/bkeagent/config"
        mode: "0600"
        owner: "root:root"
        secretRef:
          name: "{{clusterName}}-kubeconfig"
          namespace: "{{clusterNamespace}}"
          key: value

      # 节点标识
      - name: node
        path: "/etc/openFuyao/bkeagent/node"
        mode: "0644"
        owner: "root:root"
        content: "{{nodeHostname}}"

      # 集群标识
      - name: cluster
        path: "/etc/openFuyao/bkeagent/cluster"
        mode: "0644"
        owner: "root:root"
        content: "{{clusterName}}"

    installScript: |
      #!/bin/bash
      set -e

      # 配置文件由 ConfigRenderer 自动上传到对应路径
      # 只需重启 bkeagent 使配置生效
      systemctl restart bkeagent

      # 等待 bkeagent 启动
      sleep 2

      # 验证 bkeagent 运行状态
      systemctl is-active bkeagent

      echo "bkeagent switched to cluster {{clusterName}}"

    uninstallScript: ""  # 切换是单向操作，无需卸载

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

    healthCheck:
      enabled: true
      timeout: "1m"
      interval: "3s"
      script: |
        systemctl is-active bkeagent

  dependencies:
    - name: cluster-api
      phase: Install

  upgradeStrategy:
    mode: Parallel  # 所有节点同时切换
    batchSize: 0
    timeout: "5m"
    failurePolicy: Continue  # 失败时继续，不阻塞后续流程

  nodeFilter:
    roles: []  # 所有角色
    skipCompleted: true  # 已切换的节点跳过
```

**执行流程**：
```
1. 前置检查:
   - 检查注解 bke.bocloud.com/bkeagent-listener
     → "current" 或缺失: 跳过（仍监听管理集群）
     → "bkecluster": 继续执行
   - 检查 Condition SwitchBKEAgent
     → True: 跳过（已切换）
     → False/缺失: 继续执行

2. 获取节点列表:
   NodeProvider.GetNodes() → [node1, node2, node3]

3. 节点过滤:
   - 排除 Failed/Deleting/Skipped 状态节点
   - 跳过已切换节点（NodeComponentStatuses 检查）

4. 渲染配置文件（Parallel 模式，所有节点并行）:
   - kubeconfig: 从 Secret {{clusterName}}-kubeconfig 读取
   - node: 渲染 {{nodeHostname}}
   - cluster: 渲染 {{clusterName}}

5. SSH 上传配置文件:
   - /etc/openFuyao/bkeagent/config (目标集群 kubeconfig)
   - /etc/openFuyao/bkeagent/node (节点标识)
   - /etc/openFuyao/bkeagent/cluster (集群标识)

6. 执行 installScript:
   - systemctl restart bkeagent
   - sleep 2
   - systemctl is-active bkeagent

7. 健康检查:
   - systemctl is-active bkeagent → ✅

8. 标记完成:
   - NodeComponentStatuses[bkeagent-switch][nodeIP] = Installed
   - Condition: SwitchBKEAgent = True
   - ListenerTarget: bkecluster
```

---

### 1.7 bke-manifests 目录结构

**目录结构**：
```
bke-manifests/
├── container-runtime/v1.0.0/component.yaml  ← type: selector
├── containerd/v1.7.18/component.yaml        ← type: binary
├── docker/v26.0.0/component.yaml            ← type: binary
├── cri-dockerd/v0.3.9/component.yaml        ← type: binary
├── bkeagent/v2.6.0/component.yaml           ← type: binary
├── bkeagent-switch/v2.6.0/component.yaml    ← type: binary
├── cluster-api/v1.5.0/component.yaml        ← type: helm
├── coredns/v1.11.1/component.yaml           ← type: helm
├── openfuyao-core/v26.03/component.yaml     ← type: yaml
└── kubernetes-master/v1.29.0/               ← type: inline
```

**说明**：
- `container-runtime` 是 selector 类型，在 DAG 构建期根据 `BKECluster.Spec.Cluster.ContainerRuntime.CRI` 展开为具体的容器运行时组件（containerd 或 docker）
- `bkeagent-switch` 依赖 `cluster-api`，在 cluster-api 部署完成后执行
- `kubernetes-master` 和 `kubernetes-worker` 是 inline 类型，无需 ComponentVersion YAML

---

### 1.8 ReleaseImage 样例

```yaml
# releaseimage-v2.6.0.yaml
apiVersion: config.openfuyao.cn/v1beta1
kind: ReleaseImage
metadata:
  name: bke-v2.6.0
spec:
  version: v2.6.0
  install:
    components:
      # Batch 1: bkeagent（监听管理集群）
      - name: bkeagent
        version: v2.6.0
      
      # Batch 2: 容器运行时（selector 类型，DAG 构建期展开）
      - name: container-runtime
        version: v1.0.0
      
      # Batch 3: Kubernetes 控制面（Inline）
      - name: kubernetes-master
        version: v1.29.0
        inline:
          handler: EnsureMasterInit
          version: v1.0.0
      
      # Batch 4: Kubernetes 工作节点（Inline）
      - name: kubernetes-worker
        version: v1.29.0
        inline:
          handler: EnsureWorkerJoin
          version: v1.0.0
      
      # Batch 5: cluster-api（Helm，创建目标集群 kubeconfig Secret）
      - name: cluster-api
        version: v1.5.0
      
      # Batch 6: bkeagent-switch（切换到目标集群）
      - name: bkeagent-switch
        version: v2.6.0
        dependencies:
          - name: cluster-api
      
      # Batch 7: 集群插件（Helm/YAML）
      - name: coredns
        version: v1.11.1
      
      - name: openfuyao-core
        version: v26.03
```

---

### 1.9 安装执行流程

```
用户创建 BKECluster (desiredVersion: v2.6.0, CRI: containerd)
  │
  ▼
BKEClusterReconciler.Reconcile()
  │
  ├─ 1. 解析 ReleaseImage v2.6.0
  │     releaseImage.GetInstallComponents()
  │     → [bkeagent, container-runtime, kubernetes-master, kubernetes-worker, 
  │        cluster-api, bkeagent-switch, coredns, openfuyao-core]
  │
  ├─ 2. 加载 ComponentVersion
  │     manifestStore.GetComponentManifests() 逐个加载组件定义
  │
  ├─ 3. 构建安装 DAG
  │     BuildInstallDAG(releaseImage)
  │
  │     DAG 拓扑批次:
  │     Batch 0: [finalizer, paused, manage, delete, dryrun]  (CommonPhases, inline)
  │     Batch 1: [bkeagent]                                   (binary, 监听管理集群)
  │     Batch 2: [containerd]                                 (binary, 依赖 bkeagent)
  │     Batch 3: [kubernetes-master]                          (inline, 依赖 containerd)
  │     Batch 4: [kubernetes-worker]                          (inline, 依赖 kubernetes-master)
  │     Batch 5: [cluster-api]                                (helm, 依赖 kubernetes-master)
  │     Batch 6: [bkeagent-switch]                            (binary, 依赖 cluster-api)
  │     Batch 7: [coredns, openfuyao-core]                   (helm/yaml, 依赖 kubernetes-master)
  │
  ├─ 4. Scheduler.ExecuteDAG(ctx, dag)
  │     │
  │     ├─ Batch 1: bkeagent (binary) - 监听管理集群
  │     │   └─ BinaryComponentExecutor
  │     │       ├─ 下载 bkeagent_linux_amd64
  │     │       ├─ 渲染配置（node, TLS, kubeconfig, service）
  │     │       ├─ Rolling 逐节点安装
  │     │       └─ 健康检查通过
  │     │
  │     ├─ Batch 2: containerd (binary)
  │     │   └─ BinaryComponentExecutor
  │     │       ├─ 下载 containerd-1.7.18-linux-amd64.tar.gz
  │     │       ├─ 渲染配置（config.toml, service, hosts.toml）
  │     │       ├─ Rolling 逐节点安装
  │     │       └─ 健康检查通过
  │     │
  │     ├─ Batch 3: kubernetes-master (inline)
  │     │   └─ InlineRunner.Execute(handler="EnsureMasterInit") → kubeadm init
  │     │
  │     ├─ Batch 4: kubernetes-worker (inline)
  │     │   └─ InlineRunner.Execute(handler="EnsureWorkerJoin") → kubeadm join
  │     │
  │     ├─ Batch 5: cluster-api (helm)
  │     │   └─ HelmComponentExecutor
  │     │       ├─ 拉取 Chart (OCI Registry)
  │     │       ├─ 渲染 Values
  │     │       ├─ helm install --atomic --wait
  │     │       ├─ HealthCheck: PodReady (cluster-api) → ✅
  │     │       └─ 创建目标集群 kubeconfig Secret: {{clusterName}}-kubeconfig
  │     │
  │     ├─ Batch 6: bkeagent-switch (binary) - 切换到目标集群
  │     │   └─ BinaryComponentExecutor
  │     │       ├─ 前置检查:
  │     │       │   ├─ 注解 bkeagent-listener = "bkecluster" → 继续
  │     │       │   └─ Condition SwitchBKEAgent = False → 继续
  │     │       ├─ 渲染配置:
  │     │       │   ├─ kubeconfig: 从 Secret {{clusterName}}-kubeconfig 读取
  │     │       │   ├─ node: 渲染 {{nodeHostname}}
  │     │       │   └─ cluster: 渲染 {{clusterName}}
  │     │       ├─ SSH 上传配置文件到所有节点
  │     │       ├─ 执行 installScript: systemctl restart bkeagent
  │     │       ├─ HealthCheck: systemctl is-active bkeagent → ✅
  │     │       └─ 标记完成:
  │     │           ├─ NodeComponentStatuses[bkeagent-switch] = Installed
  │     │           └─ Condition: SwitchBKEAgent = True
  │     │
  │     └─ Batch 7: Helm + YAML 组件 (并行)
  │         ├─ coredns: HelmComponentExecutor
  │         │   ├─ 拉取 Chart (OCI Registry)
  │         │   ├─ 渲染 Values
  │         │   ├─ helm install --atomic --wait
  │         │   └─ HealthCheck: PodReady (kube-dns) → ✅
  │         │
  │         └─ openfuyao-core: YamlComponentExecutor
  │             ├─ 下载清单 (crds.yaml + deployment.yaml)
  │             ├─ ApplyWithStrategy(ServerSideApply)
  │             └─ HealthCheck: PodReady → ✅
  │
  ├─ 5. 健康检查
  │     PodReady + EndpointReady 检查所有组件
  │
  └─ 6. 更新 BKECluster.Status
        phase: Ready
        conditions: 
          - {type: Ready, status: True}
          - {type: SwitchBKEAgent, status: True}
        listenerTarget: bkecluster
```

---

## 2. 升级样例

### 2.1 版本变更对比

| 组件 | 当前版本 | 目标版本 | 类型 | 升级策略 | FailurePolicy |
|------|---------|---------|------|---------|---------------|
| containerd | v1.7.15 | v1.7.18 | binary | Rolling | FailFast |
| docker | v24.0.0 | v26.0.0 | binary | Rolling | FailFast |
| cri-dockerd | v0.3.8 | v0.3.9 | binary | Rolling | FailFast |
| bkeagent | v2.5.0 | v2.6.0 | binary | Batch (batchSize=2) | Continue |
| coredns | v1.10.1 | v1.11.1 | helm | Parallel | FailFast |
| openfuyao-core | v26.01 | v26.03 | yaml | Parallel | FailFast |
| kubernetes-master | v1.29.0 | v1.29.0 | inline | — | 不升级 |
| kubernetes-worker | v1.29.0 | v1.29.0 | inline | — | 不升级 |

---

### 2.2 containerd 升级

**场景**：containerd v1.7.15 → v1.7.18，滚动升级。

**VersionContext 决策**：
```
SetCurrent("containerd", "v1.7.15")
SetTarget("containerd", "v1.7.18")

NeedsUpgrade("containerd") = true   // v1.7.15 != v1.7.18
HasCurrent("containerd") = true     // 已安装
Action = Upgrade
```

**升级执行流程**：
```
BinaryComponentExecutor 执行:
  │
  ├─ 1. 获取节点列表
  │     NodeProvider.GetNodes() → [node1, node2, node3]
  │
  ├─ 2. Rolling 逐节点升级 (batchSize=1)
  │     │
  │     ├─ node1:
  │     │   ├─ 下载 containerd-1.7.18-linux-amd64.tar.gz
  │     │   ├─ 渲染新配置（config.toml, service, hosts.toml）
  │     │   ├─ SSH 执行 installScript:
  │     │   │   ├─ systemctl stop containerd
  │     │   │   ├─ cp /usr/local/bin/containerd /usr/local/bin/containerd.bak.20260706120000
  │     │   │   ├─ tar -xzf containerd-1.7.18-linux-amd64.tar.gz -C /
  │     │   │   ├─ systemctl start containerd
  │     │   │   └─ containerd --version | grep -q "1.7.18"
  │     │   └─ 健康检查通过 → ✅
  │     │
  │     ├─ node2: (同上) → ✅
  │     │
  │     └─ node3: (同上) → ✅
  │
  ├─ 3. 更新 NodeComponentStatuses
  │     containerd: {node1: Installed, node2: Installed, node3: Installed}
  │
  └─ 4. 升级完成
```

---

### 2.3 docker 升级

**场景**：docker v24.0.0 → v26.0.0，docker 和 cri-dockerd 同时升级。

**VersionContext 决策**：
```
SetCurrent("docker", "v24.0.0")
SetTarget("docker", "v26.0.0")
SetCurrent("cri-dockerd", "v0.3.8")
SetTarget("cri-dockerd", "v0.3.9")

NeedsUpgrade("docker") = true
NeedsUpgrade("cri-dockerd") = true
Action = Upgrade (两者)
```

**升级执行流程**：
```
DAG 依赖关系: docker → cri-dockerd

Batch 1: docker 升级
  │
  ├─ Rolling 逐节点升级
  │   ├─ node1:
  │   │   ├─ 包管理器升级: yum upgrade docker-ce / apt upgrade docker-ce
  │   │   ├─ 渲染新 daemon.json
  │   │   ├─ systemctl restart docker
  │   │   └─ docker --version | grep -q "26.0.0" → ✅
  │   ├─ node2: → ✅
  │   └─ node3: → ✅
  │
Batch 2: cri-dockerd 升级 (依赖 docker 完成)
  │
  ├─ Rolling 逐节点升级
  │   ├─ node1:
  │   │   ├─ 下载 cri-dockerd-0.3.9-linux-amd64
  │   │   ├─ 渲染新 service/socket
  │   │   ├─ systemctl restart cri-dockerd
  │   │   └─ cri-dockerd --version | grep -q "0.3.9" → ✅
  │   ├─ node2: → ✅
  │   └─ node3: → ✅
```

---

### 2.4 bkeagent 升级

**场景**：bkeagent v2.5.0 → v2.6.0，分批升级（batchSize=2）。

**VersionContext 决策**：
```
SetCurrent("bkeagent", "v2.5.0")
SetTarget("bkeagent", "v2.6.0")

NeedsUpgrade("bkeagent") = true
Action = Upgrade
```

**升级执行流程**：
```
BinaryComponentExecutor 执行:
  │
  ├─ 1. 获取节点列表
  │     NodeProvider.GetNodes() → [node1, node2, node3]
  │
  ├─ 2. Batch 升级 (batchSize=2)
  │     │
  │     ├─ Batch 1: [node1, node2] (并行)
  │     │   ├─ node1:
  │     │   │   ├─ 下载 bkeagent_linux_amd64 (v2.6.0)
  │     │   │   ├─ 渲染新配置
  │     │   │   ├─ systemctl stop bkeagent
  │     │   │   ├─ cp /usr/local/bin/bkeagent /usr/local/bin/bkeagent.bak.20260706120000
  │     │   │   ├─ install -m 0755 bkeagent_linux_amd64 /usr/local/bin/bkeagent
  │     │   │   ├─ systemctl start bkeagent
  │     │   │   └─ 健康检查通过 → ✅
  │     │   │
  │     │   └─ node2: (同上) → ✅
  │     │
  │     ├─ 检查集群健康
  │     │   └─ 所有节点 Ready → 继续
  │     │
  │     └─ Batch 2: [node3]
  │         └─ node3: (同上) → ✅
  │
  ├─ 3. FailurePolicy = Continue
  │     └─ node3 失败时：记录警告，继续执行（不终止）
  │
  └─ 4. 升级完成
```

---

### 2.5 ReleaseImage 样例

```yaml
# releaseimage-v2.6.0.yaml (升级场景)
apiVersion: config.openfuyao.cn/v1beta1
kind: ReleaseImage
metadata:
  name: bke-v2.6.0
spec:
  version: v2.6.0
  upgrade:
    components:
      # 容器运行时（selector 类型）
      - name: container-runtime
        version: v1.0.0
      
      # bkeagent
      - name: bkeagent
        version: v2.6.0
      
      # CoreDNS
      - name: coredns
        version: v1.11.1
      
      # openfuyao-core
      - name: openfuyao-core
        version: v26.03
      
      # kubernetes-master/worker 版本不变，不升级
```

---

### 2.6 升级执行流程

```
用户修改 ClusterVersion desiredVersion: v2.5.0 → v2.6.0
  │
  ▼
ClusterVersionReconciler.Reconcile()
  │
  ├─ 1. 解析目标 ReleaseImage v2.6.0
  │     releaseImage.GetUpgradeComponents()
  │     → [container-runtime, bkeagent, coredns, openfuyao-core]
  │
  ├─ 2. 解析当前 ReleaseImage v2.5.0
  │     currentReleaseImage.GetUpgradeComponents()
  │     → [containerd:v1.7.15, bkeagent:v2.5.0, coredns:v1.10.1, openfuyao-core:v26.01]
  │
  ├─ 3. 构建 VersionContext (版本对比)
  │     vc.SetCurrent("containerd", "v1.7.15")
  │     vc.SetTarget("containerd", "v1.7.18")
  │     vc.SetCurrent("bkeagent", "v2.5.0")
  │     vc.SetTarget("bkeagent", "v2.6.0")
  │     ... (每个组件设置 current/target)
  │
  │     VersionContext 决策结果:
  │     containerd:       HasCurrent=true, NeedsUpgrade=true  → Action=Upgrade
  │     bkeagent:         HasCurrent=true, NeedsUpgrade=true  → Action=Upgrade
  │     coredns:          HasCurrent=true, NeedsUpgrade=true  → Action=Upgrade
  │     openfuyao-core:   HasCurrent=true, NeedsUpgrade=true  → Action=Upgrade
  │     kubernetes-master: HasCurrent=true, NeedsUpgrade=false → Skip
  │
  ├─ 4. 构建升级 DAG
  │     BuildUpgradeDAG(releaseImage)
  │
  │     DAG 拓扑批次:
  │     Batch 0: [provider]                                    (manifest, 前置)
  │     Batch 1: [bkeagent]                                    (binary, 所有节点)
  │     Batch 2: [containerd]                                  (binary, 依赖 bkeagent)
  │     Batch 3: [coredns, openfuyao-core]                     (helm/yaml, 并行)
  │
  ├─ 5. Scheduler.ExecuteDAG(ctx, dag, versionContext)
  │     │
  │     ├─ Batch 1: bkeagent 升级
  │     │   └─ BinaryComponentExecutor
  │     │       ├─ Batch 升级 (batchSize=2)
  │     │       └─ FailurePolicy=Continue
  │     │
  │     ├─ Batch 2: containerd 升级
  │     │   └─ BinaryComponentExecutor
  │     │       ├─ Rolling 逐节点升级
  │     │       └─ FailurePolicy=FailFast
  │     │
  │     └─ Batch 3: Helm + YAML 组件升级 (并行)
  │         ├─ coredns: HelmComponentExecutor
  │         │   ├─ helm upgrade --atomic --wait
  │         │   │   ├─ 成功 → Release 更新到 v1.11.1
  │         │   │   └─ 失败 → helm 自动回滚到 v1.10.1 (atomic)
  │         │   └─ HealthCheck: PodReady (kube-dns) → ✅
  │         │
  │         └─ openfuyao-core: YamlComponentExecutor
  │             ├─ ApplyWithStrategy(ServerSideApply): 增量更新
  │             └─ PruneResources(): 删除废弃资源 → ✅
  │
  ├─ 6. 健康检查
  │     所有组件 PodReady + EndpointReady
  │
  └─ 7. 更新 BKECluster.Status
        phase: Ready
        conditions: [{type: Upgraded, status: True}]
        versions:
          containerd: v1.7.18
          bkeagent: v2.6.0
          coredns: v1.11.1
          openfuyao-core: v26.03
```

---

## 3. 回滚样例

### 3.1 Binary 组件回滚

**场景**：containerd 升级失败，使用 uninstallScript 回滚。

**触发条件**：
- FailurePolicy = Rollback
- 升级过程中 installScript 执行失败

**回滚执行流程**：
```
BinaryComponentExecutor 执行:
  │
  ├─ 1. 升级 node1
  │     ├─ 下载 containerd-1.7.18
  │     ├─ 执行 installScript
  │     └─ ❌ 失败（containerd 启动失败）
  │
  ├─ 2. 触发回滚
  │     └─ 执行 uninstallScript:
  │         ├─ systemctl stop containerd
  │         ├─ systemctl disable containerd
  │         ├─ rm -f /usr/local/bin/containerd
  │         ├─ rm -f /usr/local/bin/containerd-shim-runc-v2
  │         ├─ rm -f /usr/lib/systemd/system/containerd.service
  │         ├─ rm -rf /etc/containerd/
  │         └─ systemctl daemon-reload
  │
  ├─ 3. 重新安装旧版本
  │     ├─ 下载 containerd-1.7.15
  │     ├─ 执行 installScript
  │     └─ ✅ 成功
  │
  └─ 4. 继续升级下一个节点
```

---

### 3.2 Helm 组件回滚

**场景**：coredns 升级失败，使用 Helm atomic 自动回滚。

**触发条件**：
- Strategy.Atomic = true
- helm upgrade 失败

**回滚执行流程**：
```
HelmComponentExecutor 执行:
  │
  ├─ 1. 执行 helm upgrade
  │     helm upgrade coredns ./coredns-1.11.1.tgz \
  │       --namespace kube-system \
  │       --atomic \
  │       --wait \
  │       --timeout 5m
  │
  ├─ 2. 升级过程中检查
  │     ├─ Pod 启动
  │     ├─ HealthCheck: PodReady
  │     └─ ❌ Pod 未就绪（镜像拉取失败）
  │
  ├─ 3. Helm 自动回滚
  │     ├─ 检测到 --atomic 且升级失败
  │     ├─ 自动执行 helm rollback coredns
  │     └─ 恢复到 v1.10.1
  │
  ├─ 4. 验证回滚成功
  │     ├─ Pod 重新就绪
  │     └─ HealthCheck: PodReady → ✅
  │
  └─ 5. 返回错误
        └─ 升级失败，但集群状态已恢复
```

---

## 4. Feature Gate 兼容性

### 4.1 Feature Gate ON 路径

**场景**：Feature Gate BinaryComponentSupport = true，使用新路径。

**执行流程**：
```
BKEClusterReconciler.Reconcile()
  │
  ├─ 1. 检查 Feature Gate
  │     featuregate.BinaryComponentEnabled(cluster) = true
  │
  ├─ 2. 使用新路径
  │     ├─ containerd: BinaryComponentExecutor (SSH 推送)
  │     ├─ bkeagent: BinaryComponentExecutor (SSH 推送)
  │     └─ EnsureNodesEnv: scope 不含 "runtime"
  │
  └─ 3. DAG 执行
        └─ 所有 binary 组件通过 BinaryInstaller 安装
```

---

### 4.2 Feature Gate OFF 路径

**场景**：Feature Gate BinaryComponentSupport = false，使用旧路径。

**执行流程**：
```
BKEClusterReconciler.Reconcile()
  │
  ├─ 1. 检查 Feature Gate
  │     featuregate.BinaryComponentEnabled(cluster) = false
  │
  ├─ 2. 使用旧路径
  │     ├─ containerd: EnsureNodesEnv (scope=runtime)
  │     ├─ bkeagent: EnsureBKEAgent Phase
  │     └─ EnsureContainerdUpgrade: resetContainerd + redeployContainerd
  │
  └─ 3. Phase 执行
        └─ 所有组件通过 bkeagent 内置命令安装
```

---

### 4.3 混合模式

**场景**：部分组件使用新路径，部分使用旧路径。

**配置示例**：
```yaml
apiVersion: config.openfuyao.cn/v1beta1
kind: BKECluster
metadata:
  name: mixed-cluster
spec:
  featureGates:
    BinaryComponentSupport: true
    HelmComponentSupport: false  # Helm 使用旧路径
```

**执行流程**：
```
BKEClusterReconciler.Reconcile()
  │
  ├─ 1. 检查 Feature Gate
  │     BinaryComponentSupport = true
  │     HelmComponentSupport = false
  │
  ├─ 2. 混合执行
  │     ├─ containerd: BinaryComponentExecutor (新路径)
  │     ├─ bkeagent: BinaryComponentExecutor (新路径)
  │     ├─ coredns: EnsureAddonDeploy Phase (旧路径)
  │     └─ openfuyao-core: YamlComponentExecutor (新路径)
  │
  └─ 3. DAG + Phase 混合执行
```

---

## 5. 关键设计点说明

### 5.1 ComponentVersion YAML 存放路径约定

```
bke-manifests/
├── container-runtime/v1.0.0/component.yaml  ← type: selector (容器运行时互斥选择)
├── containerd/v1.7.18/component.yaml        ← type: binary (被 selector 引用)
├── docker/v26.0.0/component.yaml            ← type: binary (被 selector 引用)
├── cri-dockerd/v0.3.9/component.yaml        ← type: binary (被 selector 引用, 依赖 docker)
├── bkeagent/v2.6.0/component.yaml           ← type: binary
├── coredns/v1.11.1/component.yaml           ← type: helm
├── openfuyao-core/v26.03/component.yaml     ← type: yaml (含 subComponents)
└── kubernetes-master/v1.29.0/               ← type: inline (无需 YAML, 由 inline handler 定义)
```

### 5.2 Selector 类型展开机制

**展开时机**：DAG 构建期（BuildDAGFromBundle）

**展开规则**：
```go
func (s *Scheduler) expandSelectorComponents(
    ctx context.Context,
    execCtx *ExecutionContext,
    cv *configv1alpha1.ComponentVersion,
) ([]topology.ComponentNode, error) {
    // 遍历 subComponents
    for _, sub := range cv.Spec.SubComponents {
        // 评估 condition
        matched, err := s.evaluateCondition(sub.Condition, execCtx.TemplateContext)
        if matched {
            // 创建 DAG 节点
            nodes = append(nodes, topology.ComponentNode{
                Name:    sub.Name,
                Version: sub.Version,
            })
        }
    }
    return nodes, nil
}
```

**condition 评估**：
```go
func (s *Scheduler) evaluateCondition(condition string, tmplCtx manifest.TemplateContext) (bool, error) {
    // 使用 TemplateRenderer 渲染 condition
    result, err := s.templateRenderer.RenderScript(condition, tmplCtx)
    if err != nil {
        return false, err
    }
    // 渲染结果为 "true" 时返回 true
    return strings.TrimSpace(result) == "true", nil
}
```

### 5.3 在线/离线场景差异

| 维度 | 在线模式 | 离线模式 |
|------|---------|---------|
| **镜像仓库** | 可访问公共仓库 | 仅可访问私有仓库 |
| **hosts.toml** | 仅为 imageRepo 生成 | 为所有公共仓库生成重定向 |
| **制品下载** | 从公共仓库下载 | 从私有仓库下载 |
| **isOffline 变量** | `false` | `true` |

**isOffline 判定逻辑**：
```go
// BinaryComponentExecutor 中
if regConfig, ok := nodeTmpl.Config.Cluster.ContainerRuntime.Registry[nodeTmpl.ImageRegistry]; ok {
    if regConfig.SkipVerify && nodeTmpl.ImageRegistry != "cr.openfuyao.cn" {
        nodeTmpl.Variables["isOffline"] = "true"
    }
}
```

### 5.4 多架构支持

**架构发现**：BinaryInstaller.Install() 通过 SSH 执行 `uname -m`

**制品 URL 模板**：
```yaml
artifacts:
  - name: containerd
    url: "{{.Config.Cluster.ImageRepo.Domain}}/binaries/containerd/{{version}}/containerd-{{version}}-linux-{{arch}}.tar.gz"
```

**架构映射**：
```
uname -m 输出    →    arch 变量值
x86_64           →    amd64
aarch64          →    arm64
```

### 5.5 ReleaseImage install vs upgrade components 区别

- `spec.install.components`：新集群安装时使用，包含所有组件（含 CommonPhases）
- `spec.upgrade.components`：升级时使用，仅包含需要升级的组件，未列出的组件保持不变

### 5.6 VersionContext 在升级流程中的决策时机

| 决策点 | VersionContext 方法 | 判定结果 | 后续动作 |
|--------|-------------------|---------|---------|
| DAG 构建时 | `NeedsUpgrade(name)` | false | 组件不加入 DAG，跳过执行 |
| Executor 执行时 | `NeedsUpgrade(name)` | false | 组件已在目标版本，返回 nil 跳过 |
| Executor 执行时 | `HasCurrent(name)` | true | Action = Upgrade |
| Executor 执行时 | `HasCurrent(name)` | false | Action = Install |

### 5.7 FailurePolicy 在不同场景下的行为

| 场景 | FailurePolicy | 行为 |
|------|---------------|------|
| Rolling 模式单节点失败 | FailFast | 立即返回错误，终止整个组件升级 |
| Rolling 模式单节点失败 | Continue | 记录警告日志，继续升级下一个节点 |
| Rolling 模式单节点失败 | Rollback | 对该节点执行 UninstallScript，继续下一个节点 |
| Batch 模式单批失败 | FailFast | 终止后续批次，已升级批次保留 |
| Helm `--atomic` 失败 | — | Helm SDK 自动回滚到上一个 Release |

---

**文档版本**: v1.3  
**维护者**: openFuyao Team
