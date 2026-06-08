# 详细列出BKE项目中所有的job清单与功能特性

## 一、Job架构总览

从[job.go:38-44](file:////cluster-api-provider-bke/pkg/job/job.go#L38-L44)：
```go
type Job struct {
    BuiltIn builtin.BuiltIn  // 内置Job
    K8s     k8s.K8s          // Kubernetes操作Job
    Shell   shell.Shell      // Shell命令Job
    Task    map[string]*Task // 任务管理
}
```

## 二、所有Job清单

### 1. **内置Job (BuiltIn)**

从[builtin.go:43-87](file:////cluster-api-provider-bke/pkg/job/builtin/builtin.go#L43-L87)：

| 序号 | Job名称 | 功能描述 | 主要用途 |
|------|---------|----------|----------|
| 1 | **containerd** | Containerd容器运行时管理 | 安装、配置、启动Containerd服务 |
| 2 | **env** | 环境初始化 | 检查系统环境、配置内核参数、安装依赖 |
| 3 | **switchcluster** | 集群切换 | 切换kubectl上下文到目标集群 |
| 4 | **certs** | 证书管理 | 生成、更新、备份Kubernetes证书 |
| 5 | **kubelet** | Kubelet管理 | 配置、启动、重启Kubelet服务 |
| 6 | **kubeadm** | Kubeadm操作 | Init/Join/Upgrade控制平面和Worker节点 |
| 7 | **ha** | 高可用配置 | 配置HAProxy和Keepalived实现VIP |
| 8 | **downloader** | 文件下载器 | 下载镜像、二进制文件、配置文件 |
| 9 | **reset** | 节点重置 | 清理节点上的Kubernetes组件和数据 |
| 10 | **ping** | 连通性测试 | 测试节点网络连通性 |
| 11 | **backup** | 备份管理 | 备份证书、配置、数据 |
| 12 | **docker** | Docker容器运行时管理 | 安装、配置、启动Docker服务 |
| 13 | **collect** | 信息收集 | 收集节点诊断信息、日志 |
| 14 | **manifests** | Manifest管理 | 生成和管理Kubernetes Manifest文件 |
| 15 | **shutdown** | 节点关闭 | 安全关闭节点服务 |
| 16 | **selfupdate** | BKEAgent自更新 | 更新BKEAgent版本 |
| 17 | **cridocker** | CRI Docker管理 | 配置CRI Docker运行时 |
| 18 | **preprocess** | 预处理 | Bootstrap前的准备工作 |
| 19 | **postprocess** | 后处理 | Bootstrap后的清理工作 |

### 2. **其他Job模块**

| 序号 | Job名称 | 功能描述 | 主要用途 |
|------|---------|----------|----------|
| 20 | **shell** | Shell命令执行 | 执行任意Shell命令 |
| 21 | **k8s** | Kubernetes操作 | 操作Kubernetes资源（创建、删除、更新） |

## 三、详细功能特性

### 1. **Kubeadm Job** ⭐核心

从[kubeadm.go:1-100](file:////cluster-api-provider-bke/pkg/job/builtin/kubeadm/kubeadm.go#L1-L100)：

**功能特性**：
- ✅ **InitControlPlane**：初始化第一个Master节点
- ✅ **JoinControlPlane**：加入后续Master节点
- ✅ **JoinWorker**：加入Worker节点
- ✅ **UpgradeControlPlane**：升级Master节点
- ✅ **UpgradeWorker**：升级Worker节点
- ✅ **UpgradeEtcd**：升级Etcd集群

**参数**：
```go
"phase": "initControlPlane,joinControlPlane,joinWorker,upgradeControlPlane,upgradeWorker,upgradeEtcd"
"bkeConfig": "NameSpace:Name"
"backUpEtcd": "true,false"  // 升级时备份Etcd
"clusterType": "konk,bocloud"
```

**子模块**：
- `env/`：环境检查和初始化
- `certs/`：证书生成和管理
- `kubelet/`：Kubelet配置和启动
- `manifests/`：Manifest文件生成

### 2. **Reset Job** ⭐核心

从[reset.go:1-80](file:////cluster-api-provider-bke/pkg/job/builtin/reset/reset.go#L1-L80)：

**功能特性**：
- ✅ **多范围清理**：支持选择性清理不同组件
- ✅ **安全清理**：确保数据完整删除
- ✅ **额外清理**：支持自定义清理路径

**参数**：
```go
"scope": "cert,manifests,containerd-cfg,container,kubelet,containerRuntime,source,extra"
"extra": "extra file/dir/ipaddr to clean,split by ','"
```

**清理范围**：
- `cert`：清理证书文件
- `manifests`：清理Manifest文件
- `containerd-cfg`：清理Containerd配置
- `container`：清理容器
- `kubelet`：清理Kubelet数据
- `containerRuntime`：清理容器运行时
- `source`：清理源码
- `extra`：清理额外文件

### 3. **HA Job** ⭐核心

从[ha.go:1-80](file:////cluster-api-provider-bke/pkg/job/builtin/ha/ha.go#L1-L80)：

**功能特性**：
- ✅ **HAProxy配置**：配置负载均衡器
- ✅ **Keepalived配置**：配置VIP高可用
- ✅ **自动发现Master**：自动检测Master节点
- ✅ **VIP管理**：管理控制平面和Ingress VIP

**参数**：
```go
"haproxyImageName": "haproxy"
"haproxyImageTag": "2.1.4"
"keepAlivedImageName": "keepalived/keepalived"
"keepAlivedImageTag": "1.3.5"
"haNodes": "hostName:IP,hostName:IP"
"ingressVIP": "VIP for Ingress"
"controlPlaneEndpointVIP": "VIP for Control Plane"
"virtualRouterId": "51"
"wait": "false"  // 等待VIP就绪
```

### 4. **Backup Job**

从[backup.go:1-80](file:////cluster-api-provider-bke/pkg/job/builtin/backup/backup.go#L1-L80)：

**功能特性**：
- ✅ **目录备份**：备份整个目录
- ✅ **文件备份**：备份单个文件
- ✅ **自定义路径**：支持自定义备份路径

**参数**：
```go
"backupDirs": "dirs to backup, split by ','"
"backupFiles": "files to backup, split by ','"
"saveTo": "directory to save backup"
```

### 5. **Env Job**

**功能特性**：
- ✅ **系统检查**：检查操作系统版本、内核版本
- ✅ **依赖安装**：安装必要依赖包
- ✅ **内核配置**：配置内核参数（sysctl）
- ✅ **主机名配置**：设置主机名和hosts文件

**子模块**：
- `check.go`：环境检查
- `init.go`：环境初始化
- `hostfile.go`：Hosts文件配置
- `centos.go`：CentOS特定配置
- `machine.go`：机器信息获取

### 6. **Containerd Job**

**功能特性**：
- ✅ **安装配置**：安装Containerd
- ✅ **服务管理**：启动、重启Containerd服务
- ✅ **镜像仓库配置**：配置私有镜像仓库
- ✅ **CRI配置**：配置CRI接口

**子模块**：
- `service.go`：服务管理
- `hosts_toml.go`：镜像仓库配置

### 7. **Docker Job**

**功能特性**：
- ✅ **安装配置**：安装Docker
- ✅ **服务管理**：启动、重启Docker服务
- ✅ **镜像仓库配置**：配置私有镜像仓库
- ✅ **CRI配置**：配置CRI接口

### 8. **Kubelet Job**

**功能特性**：
- ✅ **配置生成**：生成Kubelet配置文件
- ✅ **服务管理**：启动、重启Kubelet服务
- ✅ **参数配置**：配置Kubelet启动参数

**子模块**：
- `service.go`：服务管理
- `command.go`：命令生成
- `containerd.go`：Containerd集成
- `docker.go`：Docker集成
- `run.go`：运行管理

### 9. **Certs Job**

**功能特性**：
- ✅ **证书生成**：生成Kubernetes证书
- ✅ **证书更新**：更新过期证书
- ✅ **证书备份**：备份证书文件
- ✅ **Etcd证书**：生成Etcd证书

### 10. **Downloader Job**

**功能特性**：
- ✅ **镜像下载**：下载容器镜像
- ✅ **二进制下载**：下载Kubernetes二进制文件
- ✅ **配置下载**：下载配置文件
- ✅ **离线包下载**：下载离线安装包

### 11. **Collect Job**

**功能特性**：
- ✅ **日志收集**：收集系统和服务日志
- ✅ **状态收集**：收集节点状态信息
- ✅ **诊断信息**：收集诊断信息
- ✅ **打包上传**：打包并上传收集的信息

### 12. **Manifests Job**

**功能特性**：
- ✅ **Manifest生成**：生成Kubernetes Manifest文件
- ✅ **模板渲染**：使用模板生成配置
- ✅ **版本管理**：管理不同版本的Manifest

### 13. **SwitchCluster Job**

**功能特性**：
- ✅ **上下文切换**：切换kubectl上下文
- ✅ **配置更新**：更新kubeconfig文件
- ✅ **集群验证**：验证目标集群可访问

### 14. **Ping Job**

**功能特性**：
- ✅ **网络测试**：测试网络连通性
- ✅ **端口测试**：测试端口可达性
- ✅ **延迟测试**：测试网络延迟

### 15. **Shutdown Job**

**功能特性**：
- ✅ **服务停止**：安全停止节点服务
- ✅ **资源释放**：释放节点资源
- ✅ **优雅关闭**：优雅关闭进程

### 16. **SelfUpdate Job**

**功能特性**：
- ✅ **版本检查**：检查新版本
- ✅ **自动更新**：自动更新BKEAgent
- ✅ **回滚支持**：支持版本回滚

### 17. **Preprocess Job**

**功能特性**：
- ✅ **预检查**：执行Bootstrap前检查
- ✅ **环境准备**：准备Bootstrap环境
- ✅ **依赖验证**：验证依赖是否满足

### 18. **Postprocess Job**

**功能特性**：
- ✅ **清理临时文件**：清理Bootstrap临时文件
- ✅ **状态验证**：验证Bootstrap成功
- ✅ **后续配置**：执行后续配置

### 19. **Shell Job**

**功能特性**：
- ✅ **命令执行**：执行任意Shell命令
- ✅ **脚本执行**：执行Shell脚本
- ✅ **输出捕获**：捕获命令输出

### 20. **K8s Job**

**功能特性**：
- ✅ **资源操作**：创建、删除、更新Kubernetes资源
- ✅ **资源查询**：查询Kubernetes资源状态
- ✅ **批量操作**：批量操作多个资源

### 21. **CRI Docker Job**

**功能特性**：
- ✅ **CRI配置**：配置CRI Docker接口
- ✅ **服务管理**：管理CRI Docker服务
- ✅ **集成配置**：配置与Kubernetes集成

## 四、Job分类

### 1. **核心Job**（Bootstrap必需）

```
┌─────────────────────────────────────────┐
│  Bootstrap流程核心Job                    │
├─────────────────────────────────────────┤
│  1. env        - 环境初始化              │
│  2. downloader - 下载二进制/镜像         │
│  3. containerd - 容器运行时安装          │
│  4. kubeadm    - Kubernetes安装          │
│  5. kubelet    - Kubelet配置启动         │
│  6. ha         - 高可用配置              │
│  7. certs      - 证书管理                │
│  8. reset      - 节点重置                │
└─────────────────────────────────────────┘
```

### 2. **辅助Job**（增强功能）

```
┌─────────────────────────────────────────┐
│  辅助Job                                 │
├─────────────────────────────────────────┤
│  1. backup     - 备份管理                │
│  2. collect    - 信息收集                │
│  3. ping       - 连通性测试              │
│  4. manifests  - Manifest管理            │
│  5. switchcluster - 集群切换             │
└─────────────────────────────────────────┘
```

### 3. **维护Job**（运维管理）

```
┌─────────────────────────────────────────┐
│  维护Job                                 │
├─────────────────────────────────────────┤
│  1. shutdown   - 节点关闭                │
│  2. selfupdate - 自更新                  │
│  3. preprocess - 预处理                  │
│  4. postprocess - 后处理                 │
└─────────────────────────────────────────┘
```

### 4. **通用Job**（工具类）

```
┌─────────────────────────────────────────┐
│  通用Job                                 │
├─────────────────────────────────────────┤
│  1. shell      - Shell命令执行           │
│  2. k8s        - Kubernetes操作          │
│  3. docker     - Docker管理              │
│  4. cridocker  - CRI Docker管理          │
└─────────────────────────────────────────┘
```

## 五、Job执行流程示例

### **Master节点Bootstrap流程**

```
┌─────────────────────────────────────────────────────────────┐
│  Master Bootstrap Job执行顺序                               │
└─────────────────────────────────────────────────────────────┘
  1. env check          → 环境检查
  2. downloader         → 下载二进制和镜像
  3. containerd install → 安装Containerd
  4. certs generate     → 生成证书
  5. kubeadm init       → 初始化控制平面
  6. kubelet start      → 启动Kubelet
  7. ha config          → 配置高可用
  8. postprocess        → 后处理清理
```

### **Worker节点Bootstrap流程**

```
┌─────────────────────────────────────────────────────────────┐
│  Worker Bootstrap Job执行顺序                               │
└─────────────────────────────────────────────────────────────┘
  1. env check          → 环境检查
  2. downloader         → 下载二进制和镜像
  3. containerd install → 安装Containerd
  4. kubeadm join       → 加入集群
  5. kubelet start      → 启动Kubelet
  6. postprocess        → 后处理清理
```

### **节点Reset流程**

```
┌─────────────────────────────────────────────────────────────┐
│  Reset Job执行顺序                                          │
└─────────────────────────────────────────────────────────────┘
  1. reset kubelet      → 停止Kubelet
  2. reset container    → 清理容器
  3. reset cert         → 清理证书
  4. reset manifests    → 清理Manifest
  5. reset source       → 清理源码
  6. reset extra        → 清理额外文件
```

## 六、总结

### **Job总数**

- **内置Job**：19个
- **其他Job**：2个
- **总计**：21个Job

### **核心特点**

1. ✅ **模块化设计**：每个Job职责单一
2. ✅ **插件化架构**：通过Plugin接口统一管理
3. ✅ **参数化配置**：所有Job支持参数配置
4. ✅ **可扩展性**：易于添加新Job
5. ✅ **幂等性**：多次执行安全

### **设计优势**

- ✅ **职责清晰**：每个Job功能明确
- ✅ **易于维护**：模块独立，便于测试
- ✅ **灵活组合**：可以灵活组合不同Job
- ✅ **可重用**：Job可在不同场景重用

BKE的Job系统设计精良，覆盖了Kubernetes集群管理的各个方面，是一个非常完善的插件化任务执行系统！

