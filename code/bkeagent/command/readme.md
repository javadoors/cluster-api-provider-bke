# 整理完整的 **Command清单与功能特性**

## Command清单与功能特性

### 一、Command分类概览

Command CRD是BKE系统中用于向节点下发执行指令的核心资源，所有Command都通过 `pkg/command/command.go` 中定义的常量进行命名和管理。

### 二、详细Command清单

#### 1. **Bootstrap Command** (bootstrap-前缀)
**命令名称格式**: `bootstrap-{nodeIP}-{timestamp}`

**功能特性**:
- **用途**: 节点引导初始化，是节点加入集群的核心命令
- **执行阶段**: 
  - `InitControlPlane`: 初始化第一个控制平面节点
  - `JoinControlPlane`: 加入其他控制平面节点
  - `JoinWorker`: 加入工作节点
- **包含步骤**:
  1. 检查容器运行时环境
  2. 执行Kubeadm引导操作
- **标签**: 
  - `MasterInitCommandLabel`: Master初始化
  - `MasterJoinCommandLabel`: Master加入
  - `WorkerJoinCommandLabel`: Worker加入
- **代码位置**: [bootstrap.go](file:////cluster-api-provider-bke/pkg/command/bootstrap.go)

#### 2. **HA Deploy Command** (k8s-ha-deploy)
**命令名称**: `k8s-ha-deploy`

**功能特性**:
- **用途**: 部署高可用负载均衡器（HAProxy/Keepalived）
- **执行场景**: 
  - 集群初始化时部署控制平面负载均衡
  - 控制平面节点变更时更新负载均衡配置
- **关键操作**:
  - 安装HAProxy和Keepalived
  - 配置负载均衡规则
  - 配置虚拟IP和高可用
- **代码位置**: [command.go](file:////cluster-api-provider-bke/pkg/command/command.go#L128)

#### 3. **K8sEnv Init Command** (k8s-env-init)
**命令名称**: `k8s-env-init`

**功能特性**:
- **用途**: Kubernetes环境初始化和检查
- **检查项**:
  - IPv4转发是否开启
  - Docker存储容量（默认检查/var/lib/docker是否大于300G）
  - 系统依赖包
  - 内核参数配置
- **执行模式**:
  - `init=true`: 执行初始化
  - `check=true`: 仅检查不初始化
  - `scope=runtime`: 检查容器运行时
- **代码位置**: [command.go](file:////cluster-api-provider-bke/pkg/command/command.go#L131)

#### 4. **Containerd Reset Command** (k8s-containerd-reset)
**命令名称**: `k8s-containerd-reset`

**功能特性**:
- **用途**: 重置Containerd运行时环境
- **执行场景**: 
  - Containerd升级前清理
  - 节点重置时清理运行时
- **清理范围**:
  - Containerd配置文件
  - Containerd数据目录
  - Containerd服务状态
- **代码位置**: [command.go](file:////cluster-api-provider-bke/pkg/command/command.go#L134)

#### 5. **Containerd Redeploy Command** (k8s-containerd-redeploy)
**命令名称**: `k8s-containerd-redeploy`

**功能特性**:
- **用途**: 重新部署Containerd运行时
- **执行场景**: 
  - Containerd版本升级
  - Containerd配置更新
- **操作步骤**:
  1. 清理旧版本Containerd
  2. 安装新版本Containerd
  3. 应用新配置
  4. 重启Containerd服务
- **代码位置**: [command.go](file:////github/cluster-api-provider-bke/pkg/command/command.go#L137)

#### 6. **K8sEnv DryRun Command** (k8s-env-dry-run)
**命令名称**: `k8s-env-dry-run`

**功能特性**:
- **用途**: 环境预检查（干运行，不执行实际操作）
- **执行场景**: 
  - 集群创建前的环境验证
  - 节点加入前的预检查
- **检查内容**:
  - 系统资源（CPU、内存、磁盘）
  - 网络配置
  - 依赖软件
  - 端口占用
- **特点**: 只检查不修改，返回检查结果
- **代码位置**: [command.go](file:////cluster-api-provider-bke/pkg/command/command.go#L140)

#### 7. **Hosts Generate Command** (k8s-hosts-generate)
**命令名称**: `k8s-hosts-generate`

**功能特性**:
- **用途**: 生成和管理/etc/hosts文件
- **执行场景**: 
  - 集群初始化时配置节点主机名解析
  - 节点加入时更新hosts映射
- **操作内容**:
  - 添加集群节点IP和主机名映射
  - 配置控制平面端点解析
  - 配置负载均衡虚拟IP解析
- **代码位置**: [command.go](file:////cluster-api-provider-bke/pkg/command/command.go#L143)

#### 8. **Image PrePull Command** (k8s-image-pre-pull)
**命令名称**: `k8s-image-pre-pull`

**功能特性**:
- **用途**: 预拉取Kubernetes镜像
- **执行场景**: 
  - 节点初始化前预下载镜像
  - 版本升级前预拉取新版本镜像
- **优势**:
  - 加快节点初始化速度
  - 避免初始化时因镜像拉取失败导致超时
  - 支持离线环境镜像预加载
- **代码位置**: [command.go](file:////cluster-api-provider-bke/pkg/command/command.go#L146)

#### 9. **Switch Cluster Command** (switch-cluster-前缀)
**命令名称格式**: `switch-cluster-{clusterName}-{timestamp}`

**功能特性**:
- **用途**: 切换节点所属集群（多集群场景）
- **执行场景**: 
  - 节点从一个集群迁移到另一个集群
  - 集群重建时复用现有节点
- **操作步骤**:
  1. 清理当前集群配置
  2. 重置节点状态
  3. 应用新集群配置
  4. 重新引导节点
- **代码位置**: [command.go](file:////cluster-api-provider-bke/pkg/command/command.go#L149)

#### 10. **Reset Node Command** (reset-node-前缀)
**命令名称格式**: `reset-node-{nodeIP}-{timestamp}`

**功能特性**:
- **用途**: 重置节点到初始状态
- **执行场景**: 
  - 节点删除时清理
  - 节点故障修复时重置
  - 集群删除时批量重置
- **清理范围** (可配置scope):
  - `cert`: 清理证书文件
  - `manifests`: 清理Kubernetes清单文件
  - `containerd-cfg`: 清理Containerd配置
  - `container`: 清理容器
  - `kubelet`: 清理Kubelet
  - `containerRuntime`: 清理容器运行时
  - `source`: 清理源码目录
  - `extra`: 清理额外文件/目录/IP地址
- **代码位置**: [command.go](file:////cluster-api-provider-bke/pkg/command/command.go#L151)

#### 11. **Upgrade Node Command** (upgrade-node-前缀)
**命令名称格式**: `upgrade-node-{nodeIP}-{timestamp}`

**功能特性**:
- **用途**: 升级节点Kubernetes版本
- **执行场景**: 
  - 控制平面版本升级
  - 工作节点版本升级
  - 组件版本升级
- **升级流程**:
  1. 检查当前版本
  2. 下载新版本二进制文件
  3. 升级Kubeadm配置
  4. 升级Kubelet
  5. 重启服务
- **代码位置**: [command.go](file:////cluster-api-provider-bke/pkg/command/command.go#L153)

#### 12. **Ping Command** (ping-前缀)
**命令名称格式**: `ping-{nodeIP}-{timestamp}`

**功能特性**:
- **用途**: 检查BKEAgent连通性
- **执行场景**: 
  - 节点加入前连通性测试
  - 集群操作前节点可用性检查
  - 故障诊断时节点连通性验证
- **检查内容**:
  - BKEAgent服务是否运行
  - 节点网络是否可达
  - Agent版本是否匹配
- **代码位置**: [command.go](file:////cluster-api-provider-bke/pkg/command/command.go#L155)

#### 13. **Collect Cert Command** (collect-前缀)
**命令名称格式**: `collect-{certType}-{timestamp}`

**功能特性**:
- **用途**: 收集和分发集群证书
- **执行场景**: 
  - 控制平面初始化后收集证书
  - 节点加入前分发证书
  - 证书轮换时更新证书
- **操作内容**:
  - 收集CA证书
  - 收集etcd证书
  - 收集front-proxy证书
  - 分发证书到目标节点
- **代码位置**: [command.go](file:////cluster-api-provider-bke/pkg/command/command.go#L157)

### 三、Command执行类型

每个Command包含多个ExecCommand，支持三种执行类型：

| Type | 执行器 | 命令格式 | 典型场景 |
|------|--------|----------|----------|
| **BuiltIn** | `builtin.Task` | `[插件名, key=value, ...]` | 环境初始化、重置、HA部署 |
| **Shell** | `shell.Task` | `[cmd, arg1, arg2, ...]` | 自定义Shell命令 |
| **Kubernetes** | `k8s.Task` | `[type:ns/name:op:path]` | ConfigMap/Secret读写 |

**示例**:
```go
// BuiltIn类型
Command: []string{"Kubeadm", "phase=InitControlPlane", "bkeConfig=ns:name"}
Type: CommandBuiltIn

// Shell类型
Command: []string{"iptables", "--table", "nat", "--list"}
Type: CommandShell

// Kubernetes类型
Command: []string{"secret:ns/name:ro:/tmp/secret.json"}
Type: CommandKubernetes
```

### 四、Command生命周期管理

#### 1. **Command创建流程**
```
Phase触发 → 构建CommandSpec → 创建Command CRD → BKEAgent Watch → 执行命令
```

#### 2. **Command状态管理**
- **Pending**: 命令待执行
- **Running**: 命令执行中
- **Complete**: 命令执行成功
- **Failed**: 命令执行失败

#### 3. **Command清理策略**
- **TTLSecondsAfterFinished**: 完成后自动清理时间（默认600秒）
- **BackoffLimit**: 失败重试次数（默认3次）
- **ActiveDeadlineSecond**: 命令执行超时时间（默认1000秒）

### 五、Command与Phase的对应关系

| Phase | 对应Command | 说明 |
|-------|------------|------|
| EnsureMasterInit | Bootstrap Command | 初始化第一个Master节点 |
| EnsureMasterJoin | Bootstrap Command | 其他Master节点加入 |
| EnsureWorkerJoin | Bootstrap Command | Worker节点加入 |
| EnsureMasterUpgrade | Upgrade Node Command | Master节点升级 |
| EnsureWorkerUpgrade | Upgrade Node Command | Worker节点升级 |
| EnsureDeleteOrReset | Reset Node Command | 节点删除/重置 |
| EnsureLoadBalance | HA Deploy Command | 负载均衡部署 |
| EnsureNodesEnv | K8sEnv Init Command | 节点环境初始化 |
| EnsureDryRun | K8sEnv DryRun Command | 环境预检查 |
| EnsureCerts | Collect Cert Command | 证书收集分发 |

### 六、Command设计特点

#### 1. **声明式设计**
- 通过CRD声明期望执行的命令
- BKEAgent Watch并执行命令
- 状态通过Status字段反馈

#### 2. **可组合性**
- 一个Command可包含多个ExecCommand
- ExecCommand按顺序执行
- 支持失败跳过配置

#### 3. **可观测性**
- 每个ExecCommand都有唯一ID
- Status记录每个命令的执行状态、输出、错误
- 支持日志收集和上报

#### 4. **容错机制**
- BackoffLimit: 失败重试
- BackoffDelay: 重试延迟
- BackoffIgnore: 失败跳过
- ActiveDeadlineSecond: 超时控制

#### 5. **资源管理**
- OwnerReference: 关联父资源
- TTLSecondsAfterFinished: 自动清理
- Unique: 唯一性保证（删除旧命令）

### 七、总结

BKE系统通过Command CRD实现了**声明式命令下发**机制，将管理集群的控制意图转化为工作节点的具体执行操作。Command系统具有以下核心价值：
1. **统一抽象**: 将各类操作统一为Command CRD
2. **解耦设计**: 管理集群与工作节点通过CRD解耦
3. **可扩展性**: 通过插件机制支持自定义命令
4. **可靠性**: 完善的重试、超时、清理机制
5. **可观测性**: 详细的状态跟踪和日志记录

所有Command都遵循相同的生命周期管理模式，确保了系统的一致性和可维护性。

