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
   - 1.10 Helm 组件安装（coredns）
   - 1.11 YAML 组件安装（openfuyao-core）
   - 1.12 多架构混合集群安装
2. [升级样例](#2-升级样例)
   - 2.1 版本变更对比
   - 2.2 containerd 升级
   - 2.3 docker 升级
   - 2.4 bkeagent 升级
   - 2.5 ReleaseImage 样例
   - 2.6 升级执行流程
   - 2.7 Helm 组件升级（coredns）
   - 2.8 YAML 组件升级（openfuyao-core）
   - 2.9 升级策略演示（Continue/Rollback）
3. [回滚样例](#3-回滚样例)
   - 3.1 Binary 组件回滚
   - 3.2 Helm 组件回滚
4. [Feature Gate 兼容性](#4-feature-gate-兼容性)
   - 4.1 Feature Gate ON 路径
   - 4.2 Feature Gate OFF 路径
   - 4.3 混合模式
5. [关键设计点说明](#5-关键设计点说明)
6. [扩缩容样例](#6-扩缩容样例)
   - 6.1 节点扩容（Scale-Out）
   - 6.2 扩容+升级并发场景
   - 6.3 节点缩容（Scale-In）
7. [Selector 依赖样例](#7-selector-依赖样例)
   - 7.1 Selector 依赖传递（containerd 场景）
   - 7.2 Selector 依赖传递（docker 场景）
   - 7.3 其他组件对 selector 的依赖
8. [错误处理与恢复样例](#8-错误处理与恢复样例)
   - 8.1 可重试错误处理
   - 8.2 不可重试错误处理
   - 8.3 部分失败处理
9. [幂等性保证样例](#9-幂等性保证样例)
   - 9.1 重复安装幂等性
   - 9.2 重复升级幂等性
   - 9.3 中断恢复场景
10. [健康检查详细样例](#10-健康检查详细样例)
    - 10.1 PodReady 检查
    - 10.2 EndpointReady 检查
    - 10.3 健康检查失败处理
11. [大规模场景样例](#11-大规模场景样例)
    - 11.1 中规模安装（3M+10W）
    - 11.2 并行性能验证

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

    uninstallScript: |
      #!/bin/bash
      systemctl stop containerd || true
      systemctl disable containerd || true
      rm -f /usr/local/bin/containerd
      rm -f /usr/local/bin/containerd-shim-runc-v2
      rm -f /usr/local/bin/containerd-shim-shimless-v2
      rm -f /usr/local/bin/ctr
      rm -f /usr/lib/systemd/system/containerd.service
      rm -rf /etc/containerd/
      systemctl daemon-reload

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

**containerd ComponentVersion YAML（离线模式）**：
```yaml
# bke-manifests/containerd/v1.7.18/component.yaml (离线模式配置)
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
      # 静态配置：config.toml
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

      # 静态配置：containerd.service
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

      # 主仓库 hosts.toml（在线/离线均生成）
      - name: hosts.toml
        path: "/etc/containerd/certs.d/{{.Config.Cluster.ImageRepo.Domain}}/hosts.toml"
        mode: "0644"
        content: |
          server = "https://{{.Config.Cluster.ImageRepo.Domain}}"
          
          [host."https://{{.Config.Cluster.ImageRepo.Domain}}"]
            capabilities = ["pull", "resolve", "push"]
            skip_verify = true

      # 离线重定向：docker.io（仅离线模式生成）
      - name: docker.io-hosts.toml
        path: "/etc/containerd/certs.d/docker.io/hosts.toml"
        mode: "0644"
        condition: "{{.isOffline}}"
        content: |
          server = "https://docker.io"
          
          [host."https://{{.Config.Cluster.ImageRepo.Domain}}"]
            capabilities = ["pull", "resolve"]
            skip_verify = true

      # 离线重定向：ghcr.io（仅离线模式生成）
      - name: ghcr.io-hosts.toml
        path: "/etc/containerd/certs.d/ghcr.io/hosts.toml"
        mode: "0644"
        condition: "{{.isOffline}}"
        content: |
          server = "https://ghcr.io"
          
          [host."https://{{.Config.Cluster.ImageRepo.Domain}}"]
            capabilities = ["pull", "resolve"]
            skip_verify = true

      # 离线重定向：quay.io（仅离线模式生成）
      - name: quay.io-hosts.toml
        path: "/etc/containerd/certs.d/quay.io/hosts.toml"
        mode: "0644"
        condition: "{{.isOffline}}"
        content: |
          server = "https://quay.io"
          
          [host."https://{{.Config.Cluster.ImageRepo.Domain}}"]
            capabilities = ["pull", "resolve"]
            skip_verify = true

      # 离线重定向：registry.k8s.io（仅离线模式生成）
      - name: registry.k8s.io-hosts.toml
        path: "/etc/containerd/certs.d/registry.k8s.io/hosts.toml"
        mode: "0644"
        condition: "{{.isOffline}}"
        content: |
          server = "https://registry.k8s.io"
          
          [host."https://{{.Config.Cluster.ImageRepo.Domain}}"]
            capabilities = ["pull", "resolve"]
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

    uninstallScript: |
      #!/bin/bash
      systemctl stop containerd || true
      systemctl disable containerd || true
      rm -f /usr/local/bin/containerd
      rm -f /usr/local/bin/containerd-shim-runc-v2
      rm -f /usr/local/bin/containerd-shim-shimless-v2
      rm -f /usr/local/bin/ctr
      rm -f /usr/lib/systemd/system/containerd.service
      rm -rf /etc/containerd/
      systemctl daemon-reload

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

    uninstallScript: |
      #!/bin/bash
      systemctl stop bkeagent || true
      systemctl disable bkeagent || true
      rm -f /usr/local/bin/bkeagent
      rm -f /usr/lib/systemd/system/bkeagent.service
      rm -rf /etc/openFuyao/bkeagent
      rm -rf /etc/openFuyao/certs
      systemctl daemon-reload

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

### 1.10 Helm 组件安装（coredns）

**场景**：新建集群，使用 Helm 安装 coredns 组件（DNS 服务）。

**集群配置**：
```yaml
apiVersion: config.openfuyao.cn/v1beta1
kind: BKECluster
metadata:
  name: helm-cluster
  namespace: default
spec:
  clusterConfig:
    cluster:
      imageRepo:
        domain: registry.example.com
```

**coredns ComponentVersion YAML**：
```yaml
# bke-manifests/coredns/v1.11.1/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: coredns-v1.11.1
spec:
  name: coredns
  type: helm
  version: v1.11.1

  helm:
    chart:
      oci:
        repository: "registry.example.com/charts/coredns"
        tag: "v1.11.1"
      checksum: "sha256:coredns-chart-checksum..."
    namespace: kube-system
    releaseName: coredns
    values:
      image:
        repository: "{{.Config.Cluster.ImageRepo.Domain}}/coredns/coredns"
        tag: "{{.ComponentVersion}}"
      replicaCount: 2
      resources:
        limits:
          cpu: "100m"
          memory: "128Mi"
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
            namespace: kube-system
            labelSelector: "k8s-app=kube-dns"
            minReady: 1

  dependencies:
    - name: kubernetes-master
      phase: Install

  upgradeStrategy:
    mode: Parallel
    failurePolicy: FailFast
    timeout: "10m"
```

**执行流程**：
```
HelmComponentExecutor 执行:
  │
  ├─ 1. 拉取 Chart
  │     ├─ 从 OCI Registry 拉取 coredns Chart
  │     ├─ 校验 checksum
  │     └─ 解压到临时目录
  │
  ├─ 2. 渲染 Values
  │     ├─ 替换模板变量:
  │     │   ├─ {{.Config.Cluster.ImageRepo.Domain}} → registry.example.com
  │     │   └─ {{.ComponentVersion}} → v1.11.1
  │     └─ 生成最终 values.yaml
  │
  ├─ 3. 执行 helm install
  │     helm install coredns ./coredns-1.11.1.tgz \
  │       --namespace kube-system \
  │       --values values.yaml \
  │       --atomic \
  │       --wait \
  │       --timeout 5m
  │
  ├─ 4. 健康检查
  │     ├─ PodReady 检查:
  │     │   ├─ 查询 Pod: labelSelector="k8s-app=kube-dns"
  │     │   ├─ 检查 Pod 状态: Ready
  │     │   └─ 检查 Ready Pod 数量: >= minReady (1)
  │     └─ 检查通过 → ✅
  │
  └─ 5. 更新状态
        ├─ ComponentStatuses["coredns"] = {Version: "v1.11.1", Phase: "Installed"}
        └─ Release: coredns (v1.11.1)
```

**关键设计点**：
- ✅ **OCI Chart 拉取**：从私有 OCI Registry 拉取 Chart，支持离线环境
- ✅ **Values 模板渲染**：支持 TemplateContext 变量替换
- ✅ **原子安装**：`--atomic` 标志确保失败自动回滚
- ✅ **健康检查**：PodReady 检查确保 DNS 服务就绪
- ✅ **依赖管理**：依赖 kubernetes-master，确保在集群初始化后安装

---

### 1.11 YAML 组件安装（openfuyao-core）

**场景**：新建集群，使用 YAML 类型安装 openfuyao-core 组件（集群管理核心）。

**集群配置**：
```yaml
apiVersion: config.openfuyao.cn/v1beta1
kind: BKECluster
metadata:
  name: yaml-cluster
  namespace: default
spec:
  clusterConfig:
    cluster:
      imageRepo:
        domain: registry.example.com
```

**openfuyao-core ComponentVersion YAML**：
```yaml
# bke-manifests/openfuyao-core/v26.03/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: openfuyao-core-v26.03
spec:
  name: openfuyao-core
  type: yaml
  version: v26.03

  yaml:
    manifests:
      - url: "{{.Config.Cluster.ImageRepo.Domain}}/manifests/openfuyao-core/v26.03/crds.yaml"
        checksum: "sha256:crds-checksum..."
      - url: "{{.Config.Cluster.ImageRepo.Domain}}/manifests/openfuyao-core/v26.03/deployment.yaml"
        checksum: "sha256:deployment-checksum..."
      - url: "{{.Config.Cluster.ImageRepo.Domain}}/manifests/openfuyao-core/v26.03/service.yaml"
        checksum: "sha256:service-checksum..."
    namespace: openfuyao-system
    applyStrategy: ServerSideApply
    prune: true
    pruneLabelSelector:
      app.kubernetes.io/managed-by: openfuyao-core
    healthCheck:
      enabled: true
      timeout: "3m"
      checks:
        - type: PodReady
          podReady:
            namespace: openfuyao-system
            labelSelector: "app.kubernetes.io/name=openfuyao-core"
            minReady: 1
        - type: EndpointReady
          endpointReady:
            namespace: openfuyao-system
            serviceName: openfuyao-core
            minReady: 1

  dependencies:
    - name: kubernetes-master
      phase: Install

  upgradeStrategy:
    mode: Parallel
    failurePolicy: FailFast
    timeout: "10m"
```

**执行流程**：
```
YamlComponentExecutor 执行:
  │
  ├─ 1. 下载清单
  │     ├─ 从 URL 下载 crds.yaml
  │     ├─ 从 URL 下载 deployment.yaml
  │     ├─ 从 URL 下载 service.yaml
  │     └─ 校验 checksum
  │
  ├─ 2. 解析多文档 YAML
  │     ├─ 解析每个文件的多个文档（--- 分隔）
  │     ├─ 转换为 unstructured.Unstructured 对象
  │     └─ 按 GVK 排序（CRD 优先）
  │
  ├─ 3. 应用到集群
  │     ├─ 按策略应用: ServerSideApply
  │     ├─ 创建命名空间: openfuyao-system
  │     ├─ 应用 CRD（优先）
  │     ├─ 应用 Deployment
  │     └─ 应用 Service
  │
  ├─ 4. 健康检查
  │     ├─ PodReady 检查:
  │     │   ├─ 查询 Pod: labelSelector="app.kubernetes.io/name=openfuyao-core"
  │     │   ├─ 检查 Pod 状态: Ready
  │     │   └─ 检查 Ready Pod 数量: >= minReady (1)
  │     ├─ EndpointReady 检查:
  │     │   ├─ 查询 Service: openfuyao-core
  │     │   ├─ 检查 Endpoint: 有可用地址
  │     │   └─ 检查 Endpoint 数量: >= minReady (1)
  │     └─ 检查通过 → ✅
  │
  └─ 5. 更新状态
        ├─ ComponentStatuses["openfuyao-core"] = {Version: "v26.03", Phase: "Installed"}
        └─ 记录已应用的资源列表（用于 Prune）
```

**关键设计点**：
- ✅ **多文档 YAML 支持**：支持 `---` 分隔的多个资源
- ✅ **ServerSideApply 策略**：增量更新，保留其他管理者的字段
- ✅ **Prune 裁剪**：删除不再需要的资源（按 labelSelector）
- ✅ **双重健康检查**：PodReady + EndpointReady 确保服务完全就绪
- ✅ **依赖管理**：依赖 kubernetes-master，确保在集群初始化后安装

---

### 1.12 多架构混合集群安装

**场景**：新建集群，包含 amd64 和 arm64 混合节点，需要按架构下载对应制品。

**集群配置**：
```yaml
apiVersion: config.openfuyao.cn/v1beta1
kind: BKECluster
metadata:
  name: multi-arch-cluster
  namespace: default
spec:
  nodeRefs:
    - name: master-amd64
      ip: 192.168.1.10
    - name: worker-amd64-1
      ip: 192.168.1.11
    - name: worker-arm64-1
      ip: 192.168.1.20
    - name: worker-arm64-2
      ip: 192.168.1.21
  clusterConfig:
    cluster:
      containerRuntime:
        cri: containerd
      imageRepo:
        domain: registry.example.com
```

**containerd ComponentVersion YAML**（支持多架构）：
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
      sandboxImage: "{{.Config.Cluster.ImageRepo.Domain}}/pause:3.9"

    artifacts:
      - name: containerd
        # 制品 URL 包含 {{arch}} 模板变量
        url: "{{.Config.Cluster.ImageRepo.Domain}}/binaries/containerd/{{version}}/containerd-{{version}}-linux-{{arch}}.tar.gz"
        checksum: "sha256:abc123def456..."
        installPath: "/"

    supportedArchitectures: ["amd64", "arm64"]
    supportedOS:
      - name: centos
        versions: ["7", "8"]
      - name: ubuntu
        versions: ["20.04", "22.04"]

    # ... 其他配置省略
```

**执行流程**：
```
BinaryComponentExecutor 执行:
  │
  ├─ 1. 获取节点列表
  │     NodeProvider.GetNodes() → [master-amd64, worker-amd64-1, worker-arm64-1, worker-arm64-2]
  │
  ├─ 2. 逐节点安装（Rolling 模式）
  │     │
  │     ├─ master-amd64 (192.168.1.10):
  │     │   ├─ 架构发现: SSH 执行 `uname -m` → x86_64 → amd64
  │     │   ├─ 渲染制品 URL:
  │     │   │   containerd-1.7.18-linux-amd64.tar.gz
  │     │   ├─ 下载制品（amd64 版本）
  │     │   ├─ SSH 推送并安装
  │     │   └─ 健康检查通过 → ✅
  │     │
  │     ├─ worker-amd64-1 (192.168.1.11):
  │     │   ├─ 架构发现: x86_64 → amd64
  │     │   ├─ 下载制品（amd64 版本）
  │     │   └─ 安装 → ✅
  │     │
  │     ├─ worker-arm64-1 (192.168.1.20):
  │     │   ├─ 架构发现: SSH 执行 `uname -m` → aarch64 → arm64
  │     │   ├─ 渲染制品 URL:
  │     │   │   containerd-1.7.18-linux-arm64.tar.gz
  │     │   ├─ 下载制品（arm64 版本）
  │     │   ├─ SSH 推送并安装
  │     │   └─ 健康检查通过 → ✅
  │     │
  │     └─ worker-arm64-2 (192.168.1.21):
  │         ├─ 架构发现: aarch64 → arm64
  │         ├─ 下载制品（arm64 版本）
  │         └─ 安装 → ✅
  │
  └─ 3. 更新状态
        ComponentStatuses["containerd"] = {
          "192.168.1.10": {Version: "v1.7.18", Phase: "Installed", Arch: "amd64"},
          "192.168.1.11": {Version: "v1.7.18", Phase: "Installed", Arch: "amd64"},
          "192.168.1.20": {Version: "v1.7.18", Phase: "Installed", Arch: "arm64"},
          "192.168.1.21": {Version: "v1.7.18", Phase: "Installed", Arch: "arm64"}
        }
```

**架构映射规则**：
```
uname -m 输出    →    arch 变量值    →    制品文件名
x86_64           →    amd64          →    containerd-1.7.18-linux-amd64.tar.gz
aarch64          →    arm64          →    containerd-1.7.18-linux-arm64.tar.gz
```

**关键设计点**：
- ✅ **架构自动发现**：通过 SSH 执行 `uname -m` 自动检测节点架构
- ✅ **按架构下载制品**：根据架构渲染不同的制品 URL
- ✅ **混合集群支持**：同一集群可以包含不同架构的节点
- ✅ **架构记录**：在 NodeComponentStatuses 中记录每个节点的架构信息

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

### 2.7 Helm 组件升级（coredns）

**场景**：coredns v1.10.1 → v1.11.1，使用 helm upgrade 升级。

**VersionContext 决策**：
```
SetCurrent("coredns", "v1.10.1")
SetTarget("coredns", "v1.11.1")

NeedsUpgrade("coredns") = true
HasCurrent("coredns") = true
Action = Upgrade
```

**执行流程**：
```
HelmComponentExecutor 执行:
  │
  ├─ 1. 拉取新 Chart
  │     ├─ 从 OCI Registry 拉取 coredns v1.11.1 Chart
  │     ├─ 校验 checksum
  │     └─ 解压到临时目录
  │
  ├─ 2. 渲染 Values
  │     ├─ 替换模板变量:
  │     │   ├─ {{.Config.Cluster.ImageRepo.Domain}} → registry.example.com
  │     │   └─ {{.ComponentVersion}} → v1.11.1
  │     └─ 生成最终 values.yaml
  │
  ├─ 3. 执行 helm upgrade
  │     helm upgrade coredns ./coredns-1.11.1.tgz \
  │       --namespace kube-system \
  │       --values values.yaml \
  │       --atomic \
  │       --wait \
  │       --timeout 5m
  │
  ├─ 4. 健康检查
  │     ├─ PodReady 检查:
  │     │   ├─ 查询 Pod: labelSelector="k8s-app=kube-dns"
  │     │   ├─ 检查 Pod 状态: Ready
  │     │   └─ 检查 Ready Pod 数量: >= minReady (1)
  │     └─ 检查通过 → ✅
  │
  └─ 5. 更新状态
        ├─ ComponentStatuses["coredns"] = {Version: "v1.11.1", Phase: "Installed"}
        └─ Release: coredns (v1.11.1, revision=2)
```

**失败处理**：
```
如果 helm upgrade 失败:
  ├─ --atomic 标志触发自动回滚
  ├─ Release 回滚到 v1.10.1 (revision=1)
  ├─ 健康检查失败
  └─ 返回错误，升级终止
```

---

### 2.8 YAML 组件升级（openfuyao-core）

**场景**：openfuyao-core v26.01 → v26.03，使用 ServerSideApply 增量更新 + Prune 裁剪。

**VersionContext 决策**：
```
SetCurrent("openfuyao-core", "v26.01")
SetTarget("openfuyao-core", "v26.03")

NeedsUpgrade("openfuyao-core") = true
HasCurrent("openfuyao-core") = true
Action = Upgrade
```

**执行流程**：
```
YamlComponentExecutor 执行:
  │
  ├─ 1. 下载新清单
  │     ├─ 从 URL 下载 v26.03 的 crds.yaml
  │     ├─ 从 URL 下载 v26.03 的 deployment.yaml
  │     ├─ 从 URL 下载 v26.03 的 service.yaml
  │     └─ 校验 checksum
  │
  ├─ 2. 解析多文档 YAML
  │     ├─ 解析每个文件的多个文档
  │     ├─ 转换为 unstructured.Unstructured 对象
  │     └─ 按 GVK 排序
  │
  ├─ 3. 应用到集群（增量更新）
  │     ├─ 按策略应用: ServerSideApply
  │     ├─ 更新 CRD（如果有变更）
  │     ├─ 更新 Deployment（增量更新，保留其他管理者的字段）
  │     └─ 更新 Service（如果有变更）
  │
  ├─ 4. Prune 裁剪
  │     ├─ 查询标签匹配的资源:
  │     │   labelSelector: app.kubernetes.io/managed-by=openfuyao-core
  │     ├─ 对比当前清单中的资源
  │     ├─ 删除不在 v26.03 清单中的资源:
  │     │   ├─ 删除废弃的 ConfigMap
  │     │   └─ 删除废弃的 Service
  │     └─ Prune 完成 → ✅
  │
  ├─ 5. 健康检查
  │     ├─ PodReady 检查:
  │     │   ├─ 查询 Pod: labelSelector="app.kubernetes.io/name=openfuyao-core"
  │     │   ├─ 检查 Pod 状态: Ready
  │     │   └─ 检查 Ready Pod 数量: >= minReady (1)
  │     ├─ EndpointReady 检查:
  │     │   ├─ 查询 Service: openfuyao-core
  │     │   ├─ 检查 Endpoint: 有可用地址
  │     │   └─ 检查 Endpoint 数量: >= minReady (1)
  │     └─ 检查通过 → ✅
  │
  └─ 6. 更新状态
        ├─ ComponentStatuses["openfuyao-core"] = {Version: "v26.03", Phase: "Installed"}
        └─ 记录已应用的资源列表（用于下次 Prune）
```

**关键设计点**：
- ✅ **ServerSideApply 增量更新**：只更新变更的字段，保留其他管理者的字段
- ✅ **Prune 裁剪**：删除不再需要的资源，保持集群整洁
- ✅ **双重健康检查**：PodReady + EndpointReady 确保服务完全就绪

---

### 2.9 升级策略演示（Continue/Rollback）

**场景**：containerd 升级过程中部分节点失败，演示 Continue 和 Rollback 策略。

#### 2.9.1 Continue 策略

**配置**：
```yaml
spec:
  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    failurePolicy: Continue  # 失败后继续
```

**执行流程**：
```
BinaryComponentExecutor 执行:
  │
  ├─ node-1: 升级成功 → ✅
  │
  ├─ node-2: 升级失败 ❌
  │   ├─ 记录错误日志
  │   ├─ NodeComponentStatuses["containerd"]["192.168.1.11"] = {Phase: "Failed"}
  │   └─ 继续执行下一个节点（不终止）
  │
  ├─ node-3: 升级成功 → ✅
  │
  └─ 升级完成（部分成功）
        ComponentStatuses["containerd"] = {
          "192.168.1.10": {Version: "v1.7.18", Phase: "Installed"},
          "192.168.1.11": {Version: "v1.7.15", Phase: "Failed"},  # 保持旧版本
          "192.168.1.12": {Version: "v1.7.18", Phase: "Installed"}
        }
```

**适用场景**：
- ✅ 非关键组件升级（如日志收集器）
- ✅ 允许部分节点暂时保持旧版本
- ✅ 最大化升级进度

#### 2.9.2 Rollback 策略

**配置**：
```yaml
spec:
  upgradeStrategy:
    mode: Rolling
    batchSize: 1
    failurePolicy: Rollback  # 失败后回滚
```

**执行流程**：
```
BinaryComponentExecutor 执行:
  │
  ├─ node-1: 升级成功 → ✅
  │
  ├─ node-2: 升级失败 ❌
  │   ├─ 执行 uninstallScript:
  │   │   ├─ systemctl stop containerd
  │   │   ├─ rm -f /usr/local/bin/containerd
  │   │   └─ rm -rf /etc/containerd/
  │   ├─ 重新安装旧版本 (v1.7.15):
  │   │   ├─ 下载 containerd-1.7.15
  │   │   ├─ 执行 installScript
  │   │   └─ 健康检查通过 → ✅
  │   └─ NodeComponentStatuses["containerd"]["192.168.1.11"] = {Version: "v1.7.15", Phase: "Installed"}
  │
  ├─ node-3: 升级成功 → ✅
  │
  └─ 升级完成（部分回滚）
        ComponentStatuses["containerd"] = {
          "192.168.1.10": {Version: "v1.7.18", Phase: "Installed"},
          "192.168.1.11": {Version: "v1.7.15", Phase: "Installed"},  # 回滚到旧版本
          "192.168.1.12": {Version: "v1.7.18", Phase: "Installed"}
        }
```

**适用场景**：
- ✅ 关键组件升级（如容器运行时）
- ✅ 需要保证所有节点版本一致
- ✅ 失败后自动恢复到稳定状态

**策略对比**：

| 策略 | 失败后行为 | 适用场景 | 风险 |
|------|----------|---------|------|
| **FailFast** | 立即终止整个升级 | 关键组件，不允许部分成功 | 升级中断，需要手动处理 |
| **Continue** | 记录错误，继续执行 | 非关键组件，允许部分失败 | 集群版本不一致 |
| **Rollback** | 回滚到旧版本，继续执行 | 关键组件，需要版本一致 | 升级进度慢，资源消耗大 |

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
├── bkeagent-switch/v2.6.0/component.yaml    ← type: binary (切换监听目标)
├── cluster-api/v1.5.0/component.yaml        ← type: helm (管理目标集群)
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

## 6. 扩缩容样例

### 6.1 节点扩容（Scale-Out）

**场景**：向现有集群添加新节点，需要在新节点上安装 bkeagent、containerd，并执行 kubeadm join 加入集群。

**触发条件**：
- BKECluster.Spec.NodeRefs 增加新节点
- 新节点状态为 Ready

**集群配置变更**：
```yaml
# 变更前
spec:
  nodeRefs:
    - name: node-1
      ip: 192.168.1.10
    - name: node-2
      ip: 192.168.1.11

# 变更后
spec:
  nodeRefs:
    - name: node-1
      ip: 192.168.1.10
    - name: node-2
      ip: 192.168.1.11
    - name: node-3        # 新增节点
      ip: 192.168.1.12
```

**执行流程**：
```
BKEClusterReconciler.Reconcile() 检测到新节点
  │
  ├─ 1. DAG 构建
  │     BuildUpgradeDAG(releaseImage)
  │     ├─ bkeagent: skipCompleted=true, 仅对新节点执行
  │     ├─ containerd: skipCompleted=true, 仅对新节点执行
  │     └─ kubernetes-worker: 仅对新节点执行 kubeadm join
  │
  ├─ 2. Scheduler.ExecuteDAG(ctx, dag)
  │     │
  │     ├─ Batch 1: bkeagent (binary)
  │     │   └─ BinaryComponentExecutor
  │     │       ├─ isAlreadyAtTarget(node-3) = false (无 NodeComponentStatuses 记录)
  │     │       ├─ SSH 推送 bkeagent 到 node-3
  │     │       ├─ 执行 installScript
  │     │       ├─ HealthCheck: systemctl is-active bkeagent → ✅
  │     │       └─ MarkSuccess:
  │     │           ├─ NodeComponentStatuses["bkeagent"]["192.168.1.12"] = Installed
  │     │           └─ BKENode.StateCode |= NodeAgentReadyFlag  ← 关键：同步设置就绪标志
  │     │
  │     ├─ Batch 2: containerd (binary, 依赖 bkeagent)
  │     │   └─ BinaryComponentExecutor
  │     │       ├─ isAlreadyAtTarget(node-3) = false
  │     │       ├─ SSH 推送 containerd 到 node-3
  │     │       ├─ 执行 installScript
  │     │       ├─ HealthCheck: systemctl is-active containerd → ✅
  │     │       └─ MarkSuccess:
  │     │           └─ NodeComponentStatuses["containerd"]["192.168.1.12"] = Installed
  │     │
  │     └─ Batch 3: kubernetes-worker (inline)
  │         └─ InlineRunner.Execute(handler="EnsureWorkerJoin")
  │             ├─ 检查 NodeAgentReadyFlag = true → 继续
  │             ├─ 执行 kubeadm join 192.168.1.1:6443
  │             └─ 节点加入集群 → ✅
  │
  └─ 3. 更新 BKECluster.Status
        phase: Ready
        nodes: [node-1, node-2, node-3]
```

**幂等性保证**：
- `isAlreadyAtTarget` 检查 `NodeComponentStatuses`，已安装节点跳过
- `skipCompleted=true` 确保只对未安装节点执行
- `NodeAgentReadyFlag` 同步设置，确保后续组件能正确判断 bkeagent 就绪状态

---

### 6.2 扩容+升级并发场景

**场景**：扩容新节点的同时触发版本升级（v2.5.0 → v2.6.0），需要确保新节点安装目标版本，已有节点升级到目标版本。

**触发条件**：
- BKECluster.Spec.NodeRefs 增加新节点
- BKECluster.Spec.DesiredVersion 从 v2.5.0 变更为 v2.6.0

**VersionContext 决策**：
```
SetCurrent("bkeagent", "v2.5.0")
SetTarget("bkeagent", "v2.6.0")

NeedsUpgrade("bkeagent") = true
Action = Upgrade
```

**执行流程**：
```
BKEClusterReconciler.Reconcile() 检测到扩容+升级
  │
  ├─ 1. DAG 构建
  │     BuildUpgradeDAG(releaseImage, versionContext)
  │     ├─ bkeagent: skipCompleted=true, 对所有节点执行
  │     └─ containerd: skipCompleted=true, 对所有节点执行
  │
  ├─ 2. Scheduler.ExecuteDAG(ctx, dag, versionContext)
  │     │
  │     ├─ Batch 1: bkeagent 升级
  │     │   └─ BinaryComponentExecutor
  │     │       │
  │     │       ├─ node-1 (已有节点):
  │     │       │   ├─ isAlreadyAtTarget: NodeComponentStatuses["bkeagent"]["192.168.1.10"] = {Version: "v2.5.0"}
  │     │       │   ├─ targetVersion = "v2.6.0"
  │     │       │   ├─ v2.5.0 != v2.6.0 → 不跳过
  │     │       │   ├─ SSH 升级 bkeagent 到 v2.6.0
  │     │       │   └─ MarkSuccess: Version = "v2.6.0"
  │     │       │
  │     │       ├─ node-2 (已有节点): (同上) → ✅
  │     │       │
  │     │       └─ node-3 (新节点):
  │     │           ├─ isAlreadyAtTarget: 无 NodeComponentStatuses 记录 → 不跳过
  │     │           ├─ SSH 安装 bkeagent v2.6.0
  │     │           └─ MarkSuccess: Version = "v2.6.0"
  │     │
  │     └─ Batch 2: containerd 升级
  │         └─ BinaryComponentExecutor
  │             ├─ node-1, node-2: 升级到 v1.7.18 → ✅
  │             └─ node-3: 安装 v1.7.18 → ✅
  │
  └─ 3. 更新 BKECluster.Status
        phase: Ready
        versions:
          bkeagent: v2.6.0
          containerd: v1.7.18
        nodes: [node-1, node-2, node-3]
```

**幂等保护机制**：

`isAlreadyAtTarget` 的状态分支处理：

```go
func (f *BKENodeFilter) isAlreadyAtTarget(
    ctx context.Context,
    node Node,
    cv *configv1alpha1.ComponentVersion,
    execCtx *ExecutionContext,
) bool {
    componentName := cv.Spec.Name
    targetVersion := cv.Spec.Version

    // 从 NodeComponentStatuses 读取
    if execCtx.Cluster.Status.NodeComponentStatuses != nil {
        if compStatuses, ok := execCtx.Cluster.Status.NodeComponentStatuses[componentName]; ok {
            if status, ok := compStatuses[node.IP]; ok {
                // 已安装且版本匹配 → 跳过
                if status.Phase == "Installed" && status.Version == targetVersion {
                    return true
                }
                // 正在安装中 → 跳过 (避免并发)
                if status.Phase == "Installing" {
                    return true
                }
                // 其他状态 (Failed/Timeout/版本不匹配等) → 不跳过
                return false
            }
        }
    }

    return false
}
```

**场景验证矩阵**：

| 场景 | NodeComponentStatuses | VersionContext | isAlreadyAtTarget | 行为 |
|------|----------------------|----------------|-------------------|------|
| 新节点首次安装 | 无记录 | NeedsUpgrade=true | false → 不跳过 | 安装到目标版本 ✅ |
| 已安装到目标版本 | `{Phase: Installed, Version: v1.7.18}` | target=v1.7.18 | true → 跳过 | 幂等跳过 ✅ |
| 安装失败后重试 | `{Phase: Failed, Version: ""}` | NeedsUpgrade=true | false → 不跳过 | 重试安装 ✅ |
| 安装失败后版本变更 | `{Phase: Failed, Version: ""}` | target 变为 v1.7.20 | false → 不跳过 | 安装到新版本 ✅ |
| 升级中目标版本又变 | `{Phase: Installed, Version: v1.7.18}` | target 变为 v1.7.20 | false → 不跳过 | 升级到 v1.7.20 ✅ |
| 扩容+升级同时 | `{Phase: Installed, Version: v2.6.0}` (安装 Phase 已写入) | target=v2.6.0 | true → 跳过 | 升级 Phase 幂等跳过 ✅ |
| 二次升级中间态 | `{Phase: Installed, Version: v1.7.18}` | target=v1.7.20 | false → 不跳过 | 升级到 v1.7.20 ✅ |

---

### 6.3 节点缩容（Scale-In）

**场景**：从集群移除节点，需要清理节点上的组件并删除 BKENode CRD。

**触发条件**：
- BKECluster.Spec.NodeRefs 移除节点
- 节点状态为 Ready

**集群配置变更**：
```yaml
# 变更前
spec:
  nodeRefs:
    - name: node-1
      ip: 192.168.1.10
    - name: node-2
      ip: 192.168.1.11
    - name: node-3
      ip: 192.168.1.12

# 变更后
spec:
  nodeRefs:
    - name: node-1
      ip: 192.168.1.10
    - name: node-2
      ip: 192.168.1.11
    # node-3 已移除
```

**执行流程**：
```
BKEClusterReconciler.Reconcile() 检测到节点移除
  │
  ├─ 1. 标记节点为 Deleting
  │     BKENode.Status.StateCode |= NodeDeletingFlag
  │
  ├─ 2. 驱逐节点上的 Pod
  │     kubectl cordon node-3
  │     kubectl drain node-3 --ignore-daemonsets --delete-emptydir-data
  │
  ├─ 3. 清理节点上的组件
  │     │
  │     ├─ 执行 containerd uninstallScript:
  │     │   ├─ systemctl stop containerd
  │     │   ├─ systemctl disable containerd
  │     │   ├─ rm -f /usr/local/bin/containerd
  │     │   ├─ rm -rf /etc/containerd/
  │     │   └─ systemctl daemon-reload
  │     │
  │     ├─ 执行 bkeagent uninstallScript:
  │     │   ├─ systemctl stop bkeagent
  │     │   ├─ systemctl disable bkeagent
  │     │   ├─ rm -f /usr/local/bin/bkeagent
  │     │   ├─ rm -rf /etc/openFuyao/bkeagent
  │     │   └─ systemctl daemon-reload
  │     │
  │     └─ 执行 kubeadm reset:
  │         ├─ kubeadm reset -f
  │         └─ 清理 CNI 配置
  │
  ├─ 4. 从集群中删除节点
  │     kubectl delete node node-3
  │
  ├─ 5. 删除 BKENode CRD
  │     kubectl delete bkenode node-3 -n default
  │
  └─ 6. 清理 NodeComponentStatuses
        删除 NodeComponentStatuses 中 node-3 的记录
```

**注意事项**：
- 缩容前需要确保节点上的 Pod 已迁移
- 控制面节点缩容需要特别处理（etcd 成员移除）
- 缩容后需要更新 BKECluster.Status.Nodes

---

## 7. Selector 依赖样例

### 7.1 Selector 依赖传递（containerd 场景）

**场景**：CRI=containerd，container-runtime selector 展开为 containerd，containerd 依赖 bkeagent。

**集群配置**：
```yaml
apiVersion: config.openfuyao.cn/v1beta1
kind: BKECluster
metadata:
  name: containerd-cluster
spec:
  clusterConfig:
    cluster:
      containerRuntime:
        cri: containerd
```

**ComponentVersion YAML 配置**：

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
      condition: '{{.Config.Cluster.ContainerRuntime.CRI == "containerd"}}'
    - name: docker
      version: v26.0.0
      condition: '{{.Config.Cluster.ContainerRuntime.CRI == "docker"}}'
    - name: cri-dockerd
      version: v0.3.9
      condition: '{{.Config.Cluster.ContainerRuntime.CRI == "docker"}}'
```

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
  dependencies:
    - name: bkeagent  # containerd 依赖 bkeagent
  # ... 其他配置省略
```

**DAG 构建流程**：
```
1. DAG 构建器加载 container-runtime selector
2. 评估 condition: Config.Cluster.ContainerRuntime.CRI == "containerd" → true
3. 展开为 containerd/v1.7.18 节点
4. 加载 containerd ComponentVersion
5. 读取 dependencies: [bkeagent]
6. 构建 DAG 边: bkeagent → containerd
```

**DAG 执行顺序**：
```
Batch 0: [bkeagent]
  ├─ node-1: SSH 推送 bkeagent → ✅
  ├─ node-2: SSH 推送 bkeagent → ✅
  └─ node-3: SSH 推送 bkeagent → ✅

Batch 1: [containerd] (依赖 bkeagent)
  ├─ node-1: SSH 推送 containerd → ✅
  ├─ node-2: SSH 推送 containerd → ✅
  └─ node-3: SSH 推送 containerd → ✅

Batch 2: [kubernetes-master, kubernetes-worker]
  ├─ kubernetes-master: kubeadm init → ✅
  └─ kubernetes-worker: kubeadm join → ✅
```

---

### 7.2 Selector 依赖传递（docker 场景）

**场景**：CRI=docker，container-runtime selector 展开为 docker + cri-dockerd，docker 依赖 bkeagent，cri-dockerd 依赖 docker + bkeagent。

**集群配置**：
```yaml
apiVersion: config.openfuyao.cn/v1beta1
kind: BKECluster
metadata:
  name: docker-cluster
spec:
  clusterConfig:
    cluster:
      containerRuntime:
        cri: docker
      kubernetesVersion: "1.29.0"  # K8s >= 1.24 需要 cri-dockerd
```

**ComponentVersion YAML 配置**：

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
  dependencies:
    - name: bkeagent  # docker 依赖 bkeagent
  # ... 其他配置省略
```

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
  dependencies:
    - name: docker      # cri-dockerd 依赖 docker
    - name: bkeagent    # cri-dockerd 依赖 bkeagent
  # ... 其他配置省略
```

**DAG 构建流程**：
```
1. DAG 构建器加载 container-runtime selector
2. 评估 condition: Config.Cluster.ContainerRuntime.CRI == "docker" → true
3. 展开为 docker/v26.0.0 + cri-dockerd/v0.3.9 两个节点
4. 加载 docker ComponentVersion
   - 读取 dependencies: [bkeagent]
   - 构建 DAG 边: bkeagent → docker
5. 加载 cri-dockerd ComponentVersion
   - 读取 dependencies: [docker, bkeagent]
   - 构建 DAG 边: docker → cri-dockerd, bkeagent → cri-dockerd
```

**DAG 执行顺序**：
```
Batch 0: [bkeagent]
  ├─ node-1: SSH 推送 bkeagent → ✅
  ├─ node-2: SSH 推送 bkeagent → ✅
  └─ node-3: SSH 推送 bkeagent → ✅

Batch 1: [docker] (依赖 bkeagent)
  ├─ node-1: 包管理器安装 docker → ✅
  ├─ node-2: 包管理器安装 docker → ✅
  └─ node-3: 包管理器安装 docker → ✅

Batch 2: [cri-dockerd] (依赖 docker + bkeagent)
  ├─ node-1: SSH 推送 cri-dockerd → ✅
  ├─ node-2: SSH 推送 cri-dockerd → ✅
  └─ node-3: SSH 推送 cri-dockerd → ✅

Batch 3: [kubernetes-master, kubernetes-worker]
  ├─ kubernetes-master: kubeadm init → ✅
  └─ kubernetes-worker: kubeadm join → ✅
```

**依赖关系图**：
```
bkeagent
  ├─→ docker
  │     └─→ cri-dockerd
  └─→ cri-dockerd (冗余依赖，但无害)
```

---

### 7.3 其他组件对 selector 的依赖

**场景**：kubernetes-master 依赖 container-runtime selector，selector 展开为 docker + cri-dockerd，kubernetes-master 需要等待两者都完成。

**ComponentVersion YAML 配置**：

```yaml
# bke-manifests/kubernetes-master/v1.29.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: kubernetes-master-v1.29.0
spec:
  name: kubernetes-master
  type: inline
  version: v1.29.0
  dependencies:
    - name: container-runtime  # 依赖 selector
  inline:
    handler: EnsureMasterInit
    version: v1.0.0
```

**DAG 构建流程**：
```
1. DAG 构建器加载 kubernetes-master
2. 读取 dependencies: [container-runtime]
3. 查找 container-runtime 是否为 selector → 是
4. 展开 container-runtime 为 [docker, cri-dockerd]
5. 将对 selector 的依赖转换为对所有子组件的依赖（AND 语义）
6. 构建 DAG 边:
   - docker → kubernetes-master
   - cri-dockerd → kubernetes-master
```

**DAG 执行顺序**：
```
Batch 0: [bkeagent]
  └─ 所有节点: SSH 推送 bkeagent → ✅

Batch 1: [docker] (依赖 bkeagent)
  └─ 所有节点: 包管理器安装 docker → ✅

Batch 2: [cri-dockerd] (依赖 docker + bkeagent)
  └─ 所有节点: SSH 推送 cri-dockerd → ✅

Batch 3: [kubernetes-master] (依赖 docker + cri-dockerd)
  └─ master 节点: kubeadm init → ✅
```

**实际效果分析**：

虽然 kubernetes-master 同时依赖 docker 和 cri-dockerd，但由于 cri-dockerd 已经依赖 docker，所以 docker 的依赖是冗余的。最终效果等同于：

```
bkeagent → docker → cri-dockerd → kubernetes-master
```

**AND 语义的优势**：
- ✅ 实现简单：不需要额外逻辑判断依赖哪个子组件
- ✅ 正确性保证：DAG 拓扑排序自动处理执行顺序
- ✅ 冗余依赖无害：即使依赖了多个组件，DAG 也会正确执行
- ✅ 通用性强：适用于任何 selector 展开场景

---

## 8. 错误处理与恢复样例

### 8.1 可重试错误处理

**场景**：containerd 制品下载过程中网络超时，触发自动重试。

**错误类型**：
- 网络超时（HTTP 504 Gateway Timeout）
- 连接重置（Connection Reset）
- DNS 解析失败（Temporary Failure）

**重试配置**：
```yaml
spec:
  binary:
    artifacts:
      - name: containerd
        url: "https://registry.example.com/binaries/containerd/v1.7.18/containerd-v1.7.18-linux-amd64.tar.gz"
        checksum: "sha256:abc123def456..."
        installPath: "/"
        retryCount: 3        # 最大重试次数
        retryInterval: "5s"  # 重试间隔
```

**执行流程**：
```
BinaryInstaller.Install() 执行:
  │
  ├─ 第 1 次尝试:
  │   ├─ 下载制品: HTTP GET
  │   ├─ 网络超时 ❌
  │   └─ 记录错误日志: "Download failed: timeout, retrying..."
  │
  ├─ 等待 5 秒
  │
  ├─ 第 2 次尝试:
  │   ├─ 下载制品: HTTP GET
  │   ├─ 网络超时 ❌
  │   └─ 记录错误日志: "Download failed: timeout, retrying..."
  │
  ├─ 等待 5 秒
  │
  ├─ 第 3 次尝试:
  │   ├─ 下载制品: HTTP GET
  │   ├─ 下载成功 ✅
  │   ├─ 校验 checksum ✅
  │   └─ 保存到缓存
  │
  └─ 继续执行安装脚本
```

**指数退避策略**：
```
重试间隔: 5s → 10s → 20s → 40s (最大 1 分钟)
公式: min(retryInterval * 2^(attempt-1), maxInterval)
```

**适用场景**：
- ✅ 网络不稳定环境
- ✅ 大制品下载（容易超时）
- ✅ 临时性故障（如 CDN 节点故障）

---

### 8.2 不可重试错误处理

**场景**：containerd 制品 checksum 校验失败，立即终止升级。

**错误类型**：
- Checksum 校验失败
- 制品不存在（HTTP 404）
- 权限不足（HTTP 403）
- 配置错误（无效的模板变量）

**执行流程**：
```
BinaryInstaller.Install() 执行:
  │
  ├─ 1. 下载制品
  │     ├─ HTTP GET 请求
  │     └─ 下载成功 ✅
  │
  ├─ 2. 校验 checksum
  │     ├─ 计算实际 checksum: sha256:xyz789...
  │     ├─ 对比预期 checksum: sha256:abc123...
  │     └─ 校验失败 ❌
  │
  ├─ 3. 错误处理
  │     ├─ 记录错误日志: "Checksum mismatch: expected abc123, got xyz789"
  │     ├─ 删除损坏的制品
  │     ├─ 标记节点为 Failed:
  │     │   NodeComponentStatuses["containerd"]["192.168.1.10"] = {Phase: "Failed"}
  │     └─ 返回错误（不重试）
  │
  └─ 4. 根据 FailurePolicy 处理
        ├─ FailFast: 终止整个升级
        ├─ Continue: 继续下一个节点
        └─ Rollback: 回滚到旧版本
```

**错误分类逻辑**：
```go
func classifyError(err error) ErrorType {
    switch {
    case isNetworkError(err):
        return RetryableError  // 可重试
    case isChecksumError(err):
        return FatalError      // 不可重试
    case isPermissionError(err):
        return FatalError      // 不可重试
    case isConfigError(err):
        return FatalError      // 不可重试
    default:
        return UnknownError
    }
}
```

**适用场景**：
- ✅ 制品被篡改（checksum 不匹配）
- ✅ 制品 URL 错误（404）
- ✅ 配置错误（无法通过重试解决）

---

### 8.3 部分失败处理

**场景**：Rolling 升级过程中，部分节点成功，部分节点失败。

**集群状态**：
```
节点列表: [node-1, node-2, node-3, node-4, node-5]
升级策略: Rolling, batchSize=1, failurePolicy=Continue
```

**执行流程**：
```
BinaryComponentExecutor 执行:
  │
  ├─ node-1: 升级成功 → ✅
  │   NodeComponentStatuses["containerd"]["192.168.1.10"] = {Version: "v1.7.18", Phase: "Installed"}
  │
  ├─ node-2: 升级成功 → ✅
  │   NodeComponentStatuses["containerd"]["192.168.1.11"] = {Version: "v1.7.18", Phase: "Installed"}
  │
  ├─ node-3: 升级失败 ❌
  │   ├─ 错误: installScript 执行超时
  │   ├─ NodeComponentStatuses["containerd"]["192.168.1.12"] = {Version: "v1.7.15", Phase: "Failed"}
  │   └─ 记录警告日志，继续执行
  │
  ├─ node-4: 升级成功 → ✅
  │   NodeComponentStatuses["containerd"]["192.168.1.13"] = {Version: "v1.7.18", Phase: "Installed"}
  │
  └─ node-5: 升级失败 ❌
      ├─ 错误: checksum 校验失败
      ├─ NodeComponentStatuses["containerd"]["192.168.1.14"] = {Version: "v1.7.15", Phase: "Failed"}
      └─ 记录警告日志，继续执行
```

**最终集群状态**：
```yaml
ComponentStatuses:
  containerd:
    "192.168.1.10": {Version: "v1.7.18", Phase: "Installed"}  # 新版本
    "192.168.1.11": {Version: "v1.7.18", Phase: "Installed"}  # 新版本
    "192.168.1.12": {Version: "v1.7.15", Phase: "Failed"}     # 旧版本，失败
    "192.168.1.13": {Version: "v1.7.18", Phase: "Installed"}  # 新版本
    "192.168.1.14": {Version: "v1.7.15", Phase: "Failed"}     # 旧版本，失败

BKECluster.Status:
  phase: PartialSuccess
  conditions:
    - type: Upgraded
      status: True
      message: "3/5 nodes upgraded successfully"
```

**后续处理**：
```
1. 查看失败节点日志:
   kubectl logs -n openfuyao-system bke-controller-manager-xxx

2. 手动修复失败节点:
   - 检查网络连接
   - 检查制品 URL 和 checksum
   - 检查节点资源（磁盘空间、内存）

3. 重新触发升级:
   kubectl annotate bkecluster test-cluster \
     bke.bocloud.com/retry-failed-nodes=true

4. 系统自动重试失败节点:
   - 跳过已成功的节点
   - 仅重试 Failed 状态的节点
```

---

## 9. 幂等性保证样例

### 9.1 重复安装幂等性

**场景**：containerd 已安装到目标版本，再次触发安装，系统自动跳过。

**当前状态**：
```yaml
ComponentStatuses:
  containerd:
    "192.168.1.10": {Version: "v1.7.18", Phase: "Installed"}
    "192.168.1.11": {Version: "v1.7.18", Phase: "Installed"}
    "192.168.1.12": {Version: "v1.7.18", Phase: "Installed"}
```

**触发条件**：
```
用户误操作或系统重试，再次触发 containerd 安装
```

**执行流程**：
```
BinaryComponentExecutor 执行:
  │
  ├─ node-1 (192.168.1.10):
  │   ├─ isAlreadyAtTarget():
  │   │   ├─ 查询 NodeComponentStatuses["containerd"]["192.168.1.10"]
  │   │   ├─ Phase = "Installed"
  │   │   ├─ Version = "v1.7.18" == targetVersion
  │   │   └─ 返回 true → 跳过
  │   └─ 记录日志: "containerd already installed on node-1, skip"
  │
  ├─ node-2 (192.168.1.11):
  │   ├─ isAlreadyAtTarget() = true → 跳过
  │   └─ 记录日志: "containerd already installed on node-2, skip"
  │
  └─ node-3 (192.168.1.12):
      ├─ isAlreadyAtTarget() = true → 跳过
      └─ 记录日志: "containerd already installed on node-3, skip"
```

**幂等性保证**：
- ✅ 不重复下载制品
- ✅ 不重复执行 installScript
- ✅ 不重复更新状态
- ✅ 快速返回，节省资源

---

### 9.2 重复升级幂等性

**场景**：containerd 已升级到目标版本，再次触发升级，系统自动跳过。

**当前状态**：
```yaml
ComponentStatuses:
  containerd:
    "192.168.1.10": {Version: "v1.7.18", Phase: "Installed"}
    "192.168.1.11": {Version: "v1.7.18", Phase: "Installed"}
    "192.168.1.12": {Version: "v1.7.18", Phase: "Installed"}

VersionContext:
  Current: "v1.7.18"
  Target: "v1.7.18"
```

**触发条件**：
```
用户误操作或系统重试，再次触发 containerd 升级
```

**执行流程**：
```
BinaryComponentExecutor 执行:
  │
  ├─ 1. VersionContext 检查
  │     NeedsUpgrade("containerd") = false  // v1.7.18 == v1.7.18
  │     └─ 整个组件跳过
  │
  └─ 2. 记录日志
        "containerd already at target version v1.7.18, skip upgrade"
```

**幂等性保证**：
- ✅ DAG 构建时跳过（不加入 DAG）
- ✅ 不执行任何节点操作
- ✅ 快速返回，零开销

---

### 9.3 中断恢复场景

**场景**：升级过程中控制器重启，恢复后从中断点继续。

**中断前状态**：
```yaml
ComponentStatuses:
  containerd:
    "192.168.1.10": {Version: "v1.7.18", Phase: "Installed"}  # 已完成
    "192.168.1.11": {Version: "v1.7.18", Phase: "Installing"} # 中断
    "192.168.1.12": {Version: "v1.7.15", Phase: "Pending"}    # 未开始
```

**恢复流程**：
```
控制器重启后:
  │
  ├─ 1. 重建 DAG
  │     BuildUpgradeDAG(releaseImage)
  │     └─ containerd 节点加入 DAG
  │
  ├─ 2. 检查节点状态
  │     │
  │     ├─ node-1 (192.168.1.10):
  │     │   ├─ Phase = "Installed"
  │     │   └─ 跳过（已完成）
  │     │
  │     ├─ node-2 (192.168.1.11):
  │     │   ├─ Phase = "Installing"
  │     │   ├─ 检查是否真的在安装中
  │     │   ├─ 如果超时 → 标记为 Failed，重试
  │     │   └─ 如果正常 → 跳过（避免并发）
  │     │
  │     └─ node-3 (192.168.1.12):
  │         ├─ Phase = "Pending"
  │         └─ 继续升级
  │
  └─ 3. 继续执行
        ├─ node-2: 重试或跳过
        └─ node-3: 升级到 v1.7.18
```

**中断恢复保证**：
- ✅ 已完成的节点不重复执行
- ✅ 中断的节点可以重试
- ✅ 未开始的节点正常执行
- ✅ 状态一致性保证

---

## 10. 健康检查详细样例

### 10.1 PodReady 检查

**场景**：coredns 安装后，检查 Pod 是否就绪。

**健康检查配置**：
```yaml
spec:
  helm:
    healthCheck:
      enabled: true
      timeout: "3m"
      checks:
        - type: PodReady
          podReady:
            namespace: kube-system
            labelSelector: "k8s-app=kube-dns"
            minReady: 2  # 至少 2 个 Pod Ready
```

**执行流程**：
```
HealthChecker.Check() 执行:
  │
  ├─ 1. 查询 Pod
  │     kubectl get pods -n kube-system -l k8s-app=kube-dns
  │     └─ 返回 Pod 列表: [coredns-xxx-1, coredns-xxx-2, coredns-xxx-3]
  │
  ├─ 2. 检查 Pod 状态
  │     │
  │     ├─ coredns-xxx-1:
  │     │   ├─ Status.Phase = "Running"
  │     │   ├─ Status.Conditions:
  │     │   │   └─ Type = "Ready", Status = "True"
  │     │   └─ Ready = true ✅
  │     │
  │     ├─ coredns-xxx-2:
  │     │   ├─ Status.Phase = "Running"
  │     │   ├─ Status.Conditions:
  │     │   │   └─ Type = "Ready", Status = "True"
  │     │   └─ Ready = true ✅
  │     │
  │     └─ coredns-xxx-3:
  │         ├─ Status.Phase = "Running"
  │         ├─ Status.Conditions:
  │         │   └─ Type = "Ready", Status = "False"
  │         └─ Ready = false ❌
  │
  ├─ 3. 统计 Ready Pod 数量
  │     readyCount = 2
  │
  ├─ 4. 检查是否满足 minReady
  │     readyCount (2) >= minReady (2) → 通过 ✅
  │
  └─ 5. 返回结果
        HealthCheckResult: Success
```

**超时处理**：
```
如果 3 分钟内未满足 minReady:
  ├─ 记录错误日志: "PodReady check timeout: 1/2 ready"
  ├─ 返回错误
  └─ 根据 FailurePolicy 处理
```

---

### 10.2 EndpointReady 检查

**场景**：openfuyao-core 安装后，检查 Service Endpoint 是否就绪。

**健康检查配置**：
```yaml
spec:
  yaml:
    healthCheck:
      enabled: true
      timeout: "3m"
      checks:
        - type: EndpointReady
          endpointReady:
            namespace: openfuyao-system
            serviceName: openfuyao-core
            minReady: 1  # 至少 1 个 Endpoint Ready
```

**执行流程**：
```
HealthChecker.Check() 执行:
  │
  ├─ 1. 查询 Service
  │     kubectl get service openfuyao-core -n openfuyao-system
  │     └─ 返回 Service 对象
  │
  ├─ 2. 查询 Endpoint
  │     kubectl get endpoints openfuyao-core -n openfuyao-system
  │     └─ 返回 Endpoint 对象
  │
  ├─ 3. 检查 Endpoint 地址
  │     Endpoint.Subsets[0].Addresses:
  │       - IP: "192.168.1.10"
  │         NodeName: "master-amd64"
  │         TargetRef:
  │           Kind: "Pod"
  │           Name: "openfuyao-core-xxx-1"
  │       - IP: "192.168.1.11"
  │         NodeName: "worker-amd64-1"
  │         TargetRef:
  │           Kind: "Pod"
  │           Name: "openfuyao-core-xxx-2"
  │
  ├─ 4. 统计 Ready Endpoint 数量
  │     readyCount = 2
  │
  ├─ 5. 检查是否满足 minReady
  │     readyCount (2) >= minReady (1) → 通过 ✅
  │
  └─ 6. 返回结果
        HealthCheckResult: Success
```

**Endpoint 与 Pod 的区别**：
- ✅ PodReady: 检查 Pod 是否就绪（进程存活 + 健康检查通过）
- ✅ EndpointReady: 检查 Service 是否有可用的后端（Pod Ready + 注册到 Endpoint）
- ✅ EndpointReady 更严格，确保服务可以被访问

---

### 10.3 健康检查失败处理

**场景**：coredns 健康检查失败，Pod 未就绪。

**执行流程**：
```
HealthChecker.Check() 执行:
  │
  ├─ 1. 查询 Pod
  │     kubectl get pods -n kube-system -l k8s-app=kube-dns
  │     └─ 返回 Pod 列表: [coredns-xxx-1]
  │
  ├─ 2. 检查 Pod 状态
  │     coredns-xxx-1:
  │       ├─ Status.Phase = "Running"
  │       ├─ Status.Conditions:
  │       │   └─ Type = "Ready", Status = "False"
  │       ├─ Status.ContainerStatuses[0]:
  │       │   ├─ Ready = false
  │       │   └─ State.Waiting:
  │       │       ├─ Reason = "CrashLoopBackOff"
  │       │       └─ Message = "back-off 5m0s restarting failed container"
  │       └─ Ready = false ❌
  │
  ├─ 3. 统计 Ready Pod 数量
  │     readyCount = 0
  │
  ├─ 4. 检查是否满足 minReady
  │     readyCount (0) < minReady (1) → 失败 ❌
  │
  ├─ 5. 等待并重试（直到超时）
  │     for now < deadline {
  │       sleep(interval)
  │       readyCount = checkPodReady()
  │       if readyCount >= minReady {
  │         return Success
  │       }
  │     }
  │
  └─ 6. 超时处理
        ├─ 记录错误日志: "PodReady check timeout: 0/1 ready"
        ├─ 记录 Pod 状态: "Pod coredns-xxx-1 in CrashLoopBackOff"
        ├─ 返回错误
        └─ 根据 FailurePolicy 处理
              ├─ FailFast: 终止安装
              ├─ Continue: 继续下一个组件
              └─ Rollback: 回滚到旧版本
```

**失败诊断**：
```bash
# 查看 Pod 日志
kubectl logs coredns-xxx-1 -n kube-system

# 查看 Pod 事件
kubectl describe pod coredns-xxx-1 -n kube-system

# 查看 Pod 状态
kubectl get pod coredns-xxx-1 -n kube-system -o yaml
```

---

## 11. 大规模场景样例

### 11.1 中规模安装（3M+10W）

**场景**：中规模集群安装，3 Master + 10 Worker 节点。

**集群配置**：
```yaml
apiVersion: config.openfuyao.cn/v1beta1
kind: BKECluster
metadata:
  name: medium-cluster
  namespace: default
spec:
  nodeRefs:
    # Master 节点
    - name: master-1
      ip: 192.168.1.10
    - name: master-2
      ip: 192.168.1.11
    - name: master-3
      ip: 192.168.1.12
    # Worker 节点
    - name: worker-1
      ip: 192.168.1.20
    - name: worker-2
      ip: 192.168.1.21
    - name: worker-3
      ip: 192.168.1.22
    - name: worker-4
      ip: 192.168.1.23
    - name: worker-5
      ip: 192.168.1.24
    - name: worker-6
      ip: 192.168.1.25
    - name: worker-7
      ip: 192.168.1.26
    - name: worker-8
      ip: 192.168.1.27
    - name: worker-9
      ip: 192.168.1.28
    - name: worker-10
      ip: 192.168.1.29
  clusterConfig:
    cluster:
      containerRuntime:
        cri: containerd
      imageRepo:
        domain: registry.example.com
```

**执行流程**：
```
DAG Scheduler 执行:
  │
  ├─ Batch 1: bkeagent (Rolling, batchSize=1)
  │   ├─ master-1: SSH 推送 → ✅ (10s)
  │   ├─ master-2: SSH 推送 → ✅ (10s)
  │   ├─ master-3: SSH 推送 → ✅ (10s)
  │   ├─ worker-1~10: SSH 推送 → ✅ (10s each)
  │   └─ 总耗时: 13 * 10s = 130s ≈ 2.2 分钟
  │
  ├─ Batch 2: containerd (Rolling, batchSize=1)
  │   ├─ master-1: 下载 + 安装 → ✅ (30s)
  │   ├─ master-2: 下载 + 安装 → ✅ (30s)
  │   ├─ master-3: 下载 + 安装 → ✅ (30s)
  │   ├─ worker-1~10: 下载 + 安装 → ✅ (30s each)
  │   └─ 总耗时: 13 * 30s = 390s ≈ 6.5 分钟
  │
  ├─ Batch 3: kubernetes-master (Inline)
  │   ├─ master-1: kubeadm init → ✅ (120s)
  │   ├─ master-2: kubeadm join → ✅ (60s)
  │   ├─ master-3: kubeadm join → ✅ (60s)
  │   └─ 总耗时: 120s + 60s + 60s = 240s = 4 分钟
  │
  ├─ Batch 4: kubernetes-worker (Inline)
  │   ├─ worker-1~10: kubeadm join → ✅ (60s each)
  │   └─ 总耗时: 10 * 60s = 600s = 10 分钟
  │
  ├─ Batch 5: coredns (Helm, Parallel)
  │   ├─ helm install → ✅ (60s)
  │   └─ 总耗时: 60s = 1 分钟
  │
  └─ Batch 6: openfuyao-core (YAML, Parallel)
      ├─ apply manifests → ✅ (30s)
      └─ 总耗时: 30s = 0.5 分钟

总耗时: 2.2 + 6.5 + 4 + 10 + 1 + 0.5 = 24.2 分钟 ≈ 25 分钟
```

**性能优化**：
- ✅ **并行下载**：多个节点可以同时下载制品（如果网络带宽足够）
- ✅ **缓存复用**：已下载的制品可以缓存在本地，避免重复下载
- ✅ **批量操作**：kubeadm join 可以并行执行（如果控制面负载允许）

---

### 11.2 并行性能验证

**场景**：验证 Rolling 和 Parallel 模式的性能差异。

**Rolling 模式（batchSize=1）**：
```
节点列表: [node-1, node-2, node-3, node-4, node-5]
执行顺序: node-1 → node-2 → node-3 → node-4 → node-5
总耗时: 5 * 30s = 150s = 2.5 分钟
```

**Parallel 模式（batchSize=0）**：
```
节点列表: [node-1, node-2, node-3, node-4, node-5]
执行顺序: [node-1, node-2, node-3, node-4, node-5] (并行)
总耗时: 30s = 0.5 分钟
```

**Batch 模式（batchSize=2）**：
```
节点列表: [node-1, node-2, node-3, node-4, node-5]
执行顺序:
  Batch 1: [node-1, node-2] (并行) → 30s
  Batch 2: [node-3, node-4] (并行) → 30s
  Batch 3: [node-5] → 30s
总耗时: 3 * 30s = 90s = 1.5 分钟
```

**性能对比**：

| 模式 | batchSize | 并行度 | 总耗时 | 适用场景 |
|------|----------|--------|--------|---------|
| **Rolling** | 1 | 1 | 2.5 分钟 | 关键组件，需要逐节点验证 |
| **Batch** | 2 | 2 | 1.5 分钟 | 平衡性能和安全 |
| **Parallel** | 0 | N | 0.5 分钟 | 非关键组件，快速升级 |

**资源消耗对比**：

| 模式 | 网络带宽 | 控制面负载 | 磁盘 I/O | 风险 |
|------|---------|-----------|---------|------|
| **Rolling** | 低 | 低 | 低 | 低 |
| **Batch** | 中 | 中 | 中 | 中 |
| **Parallel** | 高 | 高 | 高 | 高 |

**建议**：
- ✅ 关键组件（containerd、bkeagent）：使用 Rolling 模式
- ✅ 非关键组件（coredns、openfuyao-core）：使用 Parallel 模式
- ✅ 中等规模集群（10-50 节点）：使用 Batch 模式（batchSize=5）

---

**文档版本**: v1.5  
**维护者**: openFuyao Team
