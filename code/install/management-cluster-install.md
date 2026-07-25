# 管理集群安装流程

> 本文档描述 BKE 管理集群的安装流程，包括 `openfuyao-system-controller` 组件的初始化过程以及各管理面组件的安装顺序和依赖关系。

---

## 目录

- [一、整体架构概览](#一整体架构概览)
- [二、安装流程详解](#二安装流程详解)
  - [2.1 阶段 0：环境准备](#21-阶段-0环境准备)
  - [2.2 阶段 1：基础设施层](#22-阶段-1基础设施层)
  - [2.3 阶段 2：监控层](#23-阶段-2监控层)
  - [2.4 阶段 3：业务层](#24-阶段-3业务层)
  - [2.5 阶段 4：应用层](#25-阶段-4应用层)
  - [2.6 阶段 5：后置处理](#26-阶段-5后置处理)
- [三、组件依赖关系](#三组件依赖关系)
- [四、与业务集群安装的区别](#四与业务集群安装的区别)
- [五、组件清单汇总](#五组件清单汇总)

---

## 一、整体架构概览

管理集群中运行着 `openfuyao-system-controller` 组件，它通过 InitContainer 执行 `install.sh` 脚本来安装其他管理面组件。

```
┌─────────────────────────────────────────────────────────────┐
│  openfuyao-system-controller (Deployment)                   │
│  ├─ InitContainer: Installer                                │
│  │   ├─ entrypoint.sh                                       │
│  │   ├─ install.sh (安装流程)                               │
│  │   └─ uninstall.sh (卸载流程)                             │
│  └─ Container: Controller                                   │
└─────────────────────────────────────────────────────────────┘
```

**安装流程总览**：

```
┌─────────────────────────────────────────────────────────────┐
│                    安装流程（按顺序执行）                      │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  阶段 0: 环境准备                                            │
│    ├─ set_kubeconfig                                         │
│    ├─ kubectl create ns                                      │
│    ├─ install_yq / install_jq / install_helm / install_cfssl │
│    ├─ generate_var / create_root_ca                          │
│    └─ add_helm_repo                                          │
│                                                              │
│  阶段 1: 基础设施层                                          │
│    ├─ install_ingress_nginx                                  │
│    └─ install_helm_chart_repository (Local Harbor)           │
│                                                              │
│  阶段 2: 监控层                                              │
│    ├─ install_kube_prometheus                                │
│    └─ install_monitoring_service                             │
│                                                              │
│  阶段 3: 业务层                                              │
│    ├─ install_console_website                                │
│    ├─ install_console_service                                │
│    ├─ install_marketplace_service                            │
│    ├─ install_application_management_service                 │
│    ├─ install_oauth_webhook_and_oauth_server                 │
│    ├─ install_plugin_management_service                      │
│    ├─ install_user_management_operator                       │
│    └─ install_web_terminal_service                           │
│                                                              │
│  阶段 4: 应用层                                              │
│    ├─ install_installer_website                              │
│    ├─ install_installer_service                              │
│    └─ install_metrics_server                                 │
│                                                              │
│  阶段 5: 后置处理                                            │
│    ├─ create_default_user                                    │
│    └─ postinstall.sh                                         │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

---

## 二、安装流程详解

### 2.1 阶段 0：环境准备

| 序号 | 组件/工具 | 说明 | 安装方式 |
|------|----------|------|---------|
| 1 | kubeconfig | 设置 Kubernetes 访问配置 | 环境变量设置 |
| 2 | openfuyao-system namespace | 管理面命名空间 | `kubectl create ns` |
| 3 | yq | YAML 处理工具 | 二进制安装 |
| 4 | jq | JSON 处理工具 | 二进制安装 |
| 5 | helm | Kubernetes 包管理器 | 二进制安装 |
| 6 | cfssl | CloudFlare SSL 证书工具 | 二进制安装 |
| 7 | Root CA | 根证书颁发机构 | cfssl 生成 |
| 8 | Helm Repo | Helm 仓库配置 | `helm repo add` |

### 2.2 阶段 1：基础设施层

#### 2.2.1 Ingress-Nginx

| 属性 | 值 |
|------|-----|
| 命名空间 | `ingress-nginx` |
| 类型 | DaemonSet |
| 组件 | `ingress-nginx-controller` |
| 功能 | 集群入口流量管理、TLS 终止、负载均衡 |

**安装内容**：

```
ingress-nginx/
├─ ingress-nginx-controller    # Ingress 控制器
├─ ingress-nginx-service       # Service (NodePort/LoadBalancer)
├─ ingress-nginx-tls-secret    # TLS 证书 Secret
└─ ingress-nginx-front-tls     # 前端 TLS 证书
```

#### 2.2.2 Local Harbor

| 属性 | 值 |
|------|-----|
| 命名空间 | `openfuyao-system` |
| 类型 | Helm Chart |
| Chart 版本 | 1.11.4 |
| 镜像版本 | v2.7.0 |
| 功能 | 本地镜像仓库、Helm Chart 仓库 |

**安装内容**：

```
local-harbor/
├─ harbor-core              # Harbor 核心服务
├─ harbor-portal            # Harbor Web 界面
├─ harbor-registry          # 镜像仓库
├─ harbor-chartmuseum       # Chart 仓库
├─ harbor-jobservice        # Job 服务
├─ harbor-database          # PostgreSQL 数据库
├─ harbor-redis             # Redis 缓存
└─ harbor-trivy             # 镜像扫描服务
```

**存储配置**：

```
HARBOR_REGISTRY_PV_SIZE="10Gi"
HARBOR_JOBSERVICE_PV_SIZE="10Gi"
HARBOR_DATABASE_PV_SIZE="10Gi"
HARBOR_CHARTMUSEUM_PV_SIZE="10Gi"
HARBOR_REDIS_PV_SIZE="10Gi"
```

### 2.3 阶段 2：监控层

#### 2.3.1 Kube-Prometheus

| 属性 | 值 |
|------|-----|
| 命名空间 | `monitoring` |
| 类型 | 多资源部署 |
| 功能 | 集群监控、告警、可视化 |

**安装内容**：

```
kube-prometheus/
├─ setup/                           # CRD 定义
│   ├─ prometheusCustomResourceDefinition.yaml
│   ├─ alertmanagerCustomResourceDefinition.yaml
│   └─ ... (其他 CRD)
│
├─ prometheusOperator/              # Prometheus Operator
│   ├─ deployment.yaml
│   ├─ service.yaml
│   └─ clusterRole.yaml
│
├─ prometheus/                      # Prometheus 实例
│   ├─ prometheus.yaml
│   ├─ service.yaml
│   └─ serviceMonitor.yaml
│
├─ alertmanager/                    # Alertmanager
│   ├─ alertmanager.yaml
│   ├─ service.yaml
│   └─ secret.yaml
│
├─ nodeExporter/                    # Node Exporter
│   ├─ daemonset.yaml
│   └─ service.yaml
│
├─ kubeStateMetrics/                # Kube State Metrics
│   ├─ deployment.yaml
│   └─ service.yaml
│
├─ blackboxExporter/                # Blackbox Exporter
│   ├─ deployment.yaml
│   └─ service.yaml
│
└─ kubernetes-components-service/   # Kubernetes 组件监控
    ├─ etcd-service.yaml
    ├─ kube-apiserver-service.yaml
    ├─ kube-controller-manager-service.yaml
    ├─ kube-scheduler-service.yaml
    └─ kube-proxy-service.yaml
```

**监控组件清单**：

| 组件 | 类型 | 说明 |
|------|------|------|
| prometheus-operator | Deployment | Prometheus 管理器 |
| prometheus | StatefulSet | 时序数据库 |
| alertmanager | StatefulSet | 告警管理器 |
| node-exporter | DaemonSet | 节点指标采集 |
| kube-state-metrics | Deployment | K8s 对象指标 |
| blackbox-exporter | Deployment | 黑盒探测 |
| grafana | Deployment | 可视化面板（可选） |

#### 2.3.2 Monitoring Service

| 属性 | 值 |
|------|-----|
| 命名空间 | `openfuyao-system` |
| 类型 | Helm Chart |
| 功能 | 监控服务 API、查询代理 |

**安装内容**：

```
monitoring-service/
├─ monitoring-service        # 监控服务
├─ oauth-proxy               # OAuth 代理
└─ service                   # Service
```

### 2.4 阶段 3：业务层

#### 2.4.1 Console Website

| 属性 | 值 |
|------|-----|
| 命名空间 | `openfuyao-system` |
| 类型 | Helm Chart |
| 功能 | 控制台前端界面 |

#### 2.4.2 Console Service

| 属性 | 值 |
|------|-----|
| 命名空间 | `openfuyao-system` |
| 类型 | Helm Chart |
| 功能 | 控制台后端 API 服务 |

**依赖服务**：

```
serverHost:
  monitoring: "http://monitoring-service.openfuyao-system.svc.cluster.local:80"
  consoleWebsite: "http://console-website.openfuyao-system.svc.cluster.local:80"
```

**安全配置**：

```
symmetricKey:
  tokenKey: "$(openssl rand -base64 32)"
  secretKey: "$(openssl rand -base64 32)"
```

#### 2.4.3 Marketplace Service

| 属性 | 值 |
|------|-----|
| 命名空间 | `openfuyao-system` |
| 类型 | Helm Chart |
| 功能 | 应用市场服务、模板管理 |

#### 2.4.4 Application Management Service

| 属性 | 值 |
|------|-----|
| 命名空间 | `openfuyao-system` |
| 类型 | Helm Chart |
| 功能 | 应用生命周期管理 |

#### 2.4.5 OAuth Webhook & OAuth Server

| 属性 | 值 |
|------|-----|
| 命名空间 | `openfuyao-system` |
| 类型 | Helm Chart |
| 功能 | Kubernetes 认证集成、OAuth2 服务 |

**关键步骤**：

```
OAuth 安装流程：
├─ 1. generate_oauth_webhook_tls_cert()  # 生成 Webhook 证书
├─ 2. modify_kubernetes_manifests()      # 修改 kube-apiserver 配置
│     └─ 添加 --authentication-token-webhook-config-file
├─ 3. install_oauth_webhook()            # 安装 Webhook
└─ 4. install_oauth_server()             # 安装 OAuth Server
```

**OAuth Webhook 配置**：

```yaml
# /etc/kubernetes/webhook/auth-webhook-config.yaml
apiVersion: v1
kind: Config
clusters:
  - name: oauth-webhook
    cluster:
      server: https://oauth-webhook.openfuyao-system.svc.cluster.local:443/webhook
```

#### 2.4.6 Plugin Management Service

| 属性 | 值 |
|------|-----|
| 命名空间 | `openfuyao-system` |
| 类型 | Helm Chart |
| 功能 | 插件管理、插件生命周期 |

#### 2.4.7 User Management Operator

| 属性 | 值 |
|------|-----|
| 命名空间 | `openfuyao-system` |
| 类型 | Helm Chart |
| 功能 | 用户管理 Operator、默认用户创建 |

#### 2.4.8 Web Terminal Service

| 属性 | 值 |
|------|-----|
| 命名空间 | `openfuyao-system` |
| 类型 | Helm Chart |
| 功能 | Web 终端、kubectl 访问 |

### 2.5 阶段 4：应用层

#### 2.5.1 Installer Website

| 属性 | 值 |
|------|-----|
| 命名空间 | `openfuyao-system` |
| 类型 | Helm Chart |
| 功能 | 安装向导前端界面 |
| 前置条件 | `bke-controller-manager` 和 `capi-controller-manager` 已运行 |

#### 2.5.2 Installer Service

| 属性 | 值 |
|------|-----|
| 命名空间 | `openfuyao-system` |
| 类型 | Helm Chart |
| 功能 | 安装向导后端服务 |
| 前置条件 | `bke-controller-manager` 和 `capi-controller-manager` 已运行 |

#### 2.5.3 Metrics Server

| 属性 | 值 |
|------|-----|
| 命名空间 | `kube-system` |
| 类型 | Deployment |
| 功能 | 资源指标采集、HPA 支持 |

**前置配置**：

```
# 创建 front-proxy CA 证书 Secret
if [ -f "/etc/kubernetes/pki/front-proxy-ca.crt" ]; then
    kubectl create secret generic front-proxy-ca-cert \
        --from-file=front-proxy-ca.crt=/etc/kubernetes/pki/front-proxy-ca.crt \
        --namespace="kube-system"
fi
```

### 2.6 阶段 5：后置处理

- 创建默认用户（admin）
- 执行 `postinstall.sh` 后置脚本

---

## 三、组件依赖关系

```
┌──────────────────────────────────────────────────────────────┐
│                         组件依赖关系                          │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌─────────────────────────────────────────────────────┐     │
│  │                    应用层                             │     │
│  │  installer-website ──> installer-service             │     │
│  │          │                    │                      │     │
│  │          └────────────────────┘                      │     │
│  │                    │                                 │     │
│  │          依赖 bke-controller-manager                 │     │
│  │          依赖 capi-controller-manager                │     │
│  └─────────────────────────────────────────────────────┘     │
│                              ▲                               │
│  ┌─────────────────────────────────────────────────────┐     │
│  │                    业务层                             │     │
│  │  console-website ──> console-service                 │     │
│  │          │                    │                      │     │
│  │          │          ├──> monitoring-service          │     │
│  │          │          ├──> oauth-server                │     │
│  │          │          └──> local-harbor                │     │
│  │          │                                           │     │
│  │  marketplace-service ──> oauth-proxy                 │     │
│  │  application-management ─> oauth-proxy               │     │
│  │  plugin-management ──> oauth-proxy                   │     │
│  │  web-terminal-service ──> kubectl-openfuyao          │     │
│  └─────────────────────────────────────────────────────┘     │
│                              ▲                               │
│  ┌─────────────────────────────────────────────────────┐     │
│  │                    监控层                             │     │
│  │  monitoring-service ──> prometheus                   │     │
│  │          │                    │                      │     │
│  │          │          ├──> node-exporter               │     │
│  │          │          ├──> kube-state-metrics          │     │
│  │          │          └──> alertmanager                │     │
│  │          │                                           │     │
│  │  metrics-server ──> front-proxy-ca-cert              │     │
│  └─────────────────────────────────────────────────────┘     │
│                              ▲                               │
│  ┌─────────────────────────────────────────────────────┐     │
│  │                    认证层                             │     │
│  │  oauth-server ──> oauth-webhook                      │     │
│  │  oauth-webhook ──> kube-apiserver (webhook 配置)     │     │
│  └─────────────────────────────────────────────────────┘     │
│                              ▲                               │
│  ┌─────────────────────────────────────────────────────┐     │
│  │                    基础设施层                         │     │
│  │  ingress-nginx ──> Root CA (TLS 证书)                │     │
│  │  local-harbor ──> Root CA (镜像签名)                 │     │
│  └─────────────────────────────────────────────────────┘     │
│                              ▲                               │
│  ┌─────────────────────────────────────────────────────┐     │
│  │                    环境准备                           │     │
│  │  yq / jq / helm / cfssl                              │     │
│  │  Root CA                                             │     │
│  │  Helm Repo                                           │     │
│  └─────────────────────────────────────────────────────┘     │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

---

## 四、与业务集群安装的区别

| 维度 | 管理集群安装 | 业务集群安装 |
|------|------------|------------|
| **执行者** | `openfuyao-system-controller` | `bke-controller-manager` |
| **安装方式** | InitContainer 执行 `install.sh` 脚本 | Phase 框架逐步执行 |
| **安装内容** | 管理面组件（Console、监控、OAuth 等） | Kubernetes 控制面和工作节点 |
| **连接方式** | 直接连接管理集群 API | 通过 kubeconfig/token 连接业务集群 |
| **Phase 数量** | 无 Phase 概念，脚本顺序执行 | 16 个 Phase（CommonPhases + DeployPhases） |
| **幂等性** | 脚本内部检查 | 每个 Phase 自带幂等性 |
| **失败恢复** | 手动重新执行脚本 | 自动 Requeue 重试 |

---

## 五、组件清单汇总

### 按命名空间分组

| 命名空间 | 组件 | 类型 | 安装阶段 |
|---------|------|------|---------|
| `ingress-nginx` | ingress-nginx-controller | DaemonSet | 阶段 1 |
| `openfuyao-system` | local-harbor | Helm Chart | 阶段 1 |
| `monitoring` | prometheus-operator | Deployment | 阶段 2 |
| `monitoring` | prometheus | StatefulSet | 阶段 2 |
| `monitoring` | alertmanager | StatefulSet | 阶段 2 |
| `monitoring` | node-exporter | DaemonSet | 阶段 2 |
| `monitoring` | kube-state-metrics | Deployment | 阶段 2 |
| `monitoring` | blackbox-exporter | Deployment | 阶段 2 |
| `openfuyao-system` | monitoring-service | Helm Chart | 阶段 2 |
| `openfuyao-system` | console-website | Helm Chart | 阶段 3 |
| `openfuyao-system` | console-service | Helm Chart | 阶段 3 |
| `openfuyao-system` | marketplace-service | Helm Chart | 阶段 3 |
| `openfuyao-system` | application-management-service | Helm Chart | 阶段 3 |
| `openfuyao-system` | oauth-webhook | Helm Chart | 阶段 3 |
| `openfuyao-system` | oauth-server | Helm Chart | 阶段 3 |
| `openfuyao-system` | plugin-management-service | Helm Chart | 阶段 3 |
| `openfuyao-system` | user-management-operator | Helm Chart | 阶段 3 |
| `openfuyao-system` | web-terminal-service | Helm Chart | 阶段 3 |
| `openfuyao-system` | installer-website | Helm Chart | 阶段 4 |
| `openfuyao-system` | installer-service | Helm Chart | 阶段 4 |
| `kube-system` | metrics-server | Deployment | 阶段 4 |
