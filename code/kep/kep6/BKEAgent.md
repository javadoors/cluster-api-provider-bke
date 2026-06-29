# BKEAgent

## BKEAgent 安装完整清单

基于代码库全面梳理，以下是 bkeagent 安装涉及的所有内容。

### 一、安装的二进制文件

| 二进制 | 安装路径 | 说明 |
|:---|:---|:---|
| `bkeagent` | `/usr/local/bin/bkeagent` | Agent 主程序（按架构分发：`bkeagent_linux_amd64` / `bkeagent_linux_arm64`） |

> 升级时会备份旧二进制为 `/usr/local/bin/bkeagent.bak.{timestamp}`

### 二、安装的配置文件

| 文件 | 路径 | 生成方式 | 代码位置 |
|:---|:---|:---|:---|
| **bkeagent.service** | `/etc/systemd/system/bkeagent.service` | 模板渲染（14 行模板） | `hack/bkeagent.service.tmpl` |
| **config**（kubeconfig） | `/etc/openFuyao/bkeagent/config` | 低权限 kubeconfig 生成 | `phaseutil/localkubeconfig.go` |
| **node** | `/etc/openFuyao/bkeagent/node` | 写入主机名 | SSH `echo {hostname} > ...` |
| **trust-chain.crt** | `/etc/openFuyao/certs/trust-chain.crt` | 镜像仓库 CA 信任链 | SSH 文件上传 |
| **Global CA cert+key** | `/etc/openFuyao/certs/` | 集群 CA 证书（仅 cluster-api addon 场景） | SSH 文件上传 |
| **CSR config 文件**（15 个） | `/etc/openFuyao/certs/cert_config/` | 证书签名配置 | SSH 文件上传 |

### 三、创建的目录

| 目录 | 用途 |
|:---|:---|
| `/etc/openFuyao/bkeagent/` | Agent 配置根目录（权限 777） |
| `/etc/openFuyao/certs/` | 证书目录（权限 755） |
| `/etc/openFuyao/certs/cert_config/` | CSR 配置子目录 |
| `/var/log/openFuyao/` | Agent 日志目录（运行时自动创建） |

### 四、systemd 服务配置

来源：`hack/bkeagent.service.tmpl`（14 行模板）

```ini
[Unit]
Description= bkeagent
After=network.target

[Service]
Environment="DEBUG=true"
ExecStart=/usr/local/bin/bkeagent --kubeconfig=/etc/openFuyao/bkeagent/config --health-port={healthPort} --ntpserver={ntpServer}
KillMode=process
RestartSec=5
Restart=on-failure
SuccessExitStatus=0

[Install]
WantedBy=multi-user.target
```

| 配置项 | 值 | 说明 |
|:---|:---|:---|
| `ExecStart` | `/usr/local/bin/bkeagent --kubeconfig=... --health-port=... --ntpserver=...` | 启动命令（模板渲染） |
| `KillMode` | `process` | 仅终止主进程 |
| `RestartSec` | `5` | 重启间隔 5 秒 |
| `Restart` | `on-failure` | 失败时自动重启 |
| `Environment` | `DEBUG=true` | 调试环境变量 |

### 五、Kubeconfig 与 RBAC

#### 5.1 Kubeconfig 生成

来源：`pkg/phaseframe/phaseutil/localkubeconfig.go`（590 行）

| 场景 | 方式 | 来源 Secret |
|:---|:---|:---|
| 无 cluster-api addon | 低权限证书 kubeconfig | `kube-system/least-privilege-kubeconfig` |
| 有 cluster-api addon | 完整 kubeconfig | `kube-system/localkubeconfig` |

**证书参数**：
| 参数 | 值 |
|:---|:---|
| CN | `bkeagent-cert-user` |
| 有效期 | 100 年 |
| Context | `bkeagent-context` |
| User | `bkeagent-cert-user` |

#### 5.2 RBAC 资源（3 个 ClusterRole + RoleBinding + ClusterRoleBinding）

| 资源名 | 类型 | 权限 |
|:---|:---|:---|
| `bkeagent-readwrite` | ClusterRole | secrets(get), configmaps(get/list/watch/create/update) |
| `bkeagent-configmap-only` | ClusterRole | configmaps(get/list/watch)，限定 `cluster-system`、`user-system` 命名空间 |
| `bkeagent-cluster-access` | ClusterRole | BKE CRDs (bkeclusters, bkenodes, containerdconfigs, kubeletconfigs) + commands.bkeagent.bocloud.com CRUD |
| `bkeagent` | RoleBinding | 每个目标命名空间绑定 `bkeagent-readwrite` |
| `bkeagent-cluster-access` | ClusterRoleBinding | 绑定 `bkeagent-cluster-access` ClusterRole，Subject: User `bkeagent-cert-user` |

### 六、安装流程（代码调用链）

```
集群创建
  └── EnsureBKEAgent 阶段（DeployPhases 第一个）
        ├── 1. loadLocalKubeConfig()
        │     ├── 无 cluster-api: GetLeastPrivilegeKubeConfig() + CreateBKEAgentRBAC()
        │     └── 有 cluster-api: GetLocalKubeConfig()
        │
        ├── 2. getNeedPushNodes()
        │     └── 查询 BKENode CRD，筛选无 NodeAgentPushedFlag 的节点
        │
        ├── 3. pushAgent()
        │     ├── prepareServiceFile()
        │     │     └── RenderBKEAgentServiceFile() → 渲染 bkeagent.service
        │     │
        │     └── performAgentPush() → sshPushAgent()
        │           ├── Pre-Command（清理旧文件）:
        │           │     systemctl stop bkeagent
        │           │     systemctl disable bkeagent
        │           │     rm -rf /usr/local/bin/bkeagent*
        │           │     rm -f /etc/systemd/system/bkeagent.service
        │           │     rm -rf /etc/openFuyao/bkeagent
        │           │
        │           ├── 文件上传（所有节点）:
        │           │     bkeagent.service → /etc/systemd/system/
        │           │     trust-chain.crt → /etc/openFuyao/certs/
        │           │     Global CA cert+key → /etc/openFuyao/certs/（可选）
        │           │     15 个 CSR config → /etc/openFuyao/certs/cert_config/
        │           │
        │           ├── 文件上传（按节点架构）:
        │           │     bkeagent_linux_{arch} → /usr/local/bin/
        │           │     echo {hostname} > /etc/openFuyao/bkeagent/node
        │           │
        │           ├── Start-Command:
        │           │     mv bkeagent_linux_* → bkeagent
        │           │     chmod +x /usr/local/bin/bkeagent
        │           │     echo kubeconfig > /etc/openFuyao/bkeagent/config
        │           │     systemctl daemon-reload
        │           │     systemctl enable bkeagent
        │           │     systemctl restart bkeagent
        │           │
        │           └── Post-Command:
        │                 chmod 755 /usr/local/bin/
        │                 chmod 755 /etc/systemd/system/
        │
        └── 4. pingAgent()
              └── PingBKEAgent() → Command CRD "Ping"
                    ├── 成功: 设置 NodeAgentPushedFlag + NodeAgentReadyFlag
                    └── 失败: 清除 NodeAgentPushedFlag
```

### 七、升级流程（代码调用链）

```
EnsureAgentUpgrade 阶段
  └── upgradeBKEAgentViaSSH()
        ├── 1. 获取所有集群节点
        ├── 2. 构建 ArtifactParams（base URL + 二进制名称）
        ├── 3. DiscoverArchs() — SSH 检测各节点架构（uname -m）
        ├── 4. PrepareStaging()
        │     ├── PrepareServiceFile() — HTTP 下载或模板渲染
        │     └── DownloadBinariesForArchs() — 按架构下载 bkeagent_linux_{arch}
        │
        ├── 5. SSHUpgrade()
        │     ├── Pre-Command:
        │     │     systemctl stop bkeagent
        │     │     cp bkeagent → bkeagent.bak.{timestamp}
        │     │
        │     ├── 文件上传:
        │     │     bkeagent_linux_{arch} → /usr/local/bin/
        │     │     bkeagent.service → /etc/systemd/system/
        │     │     echo {hostname} > /etc/openFuyao/bkeagent/node
        │     │
        │     └── Start-Command:
        │           mv bkeagent_linux_* → bkeagent
        │           chmod +x
        │           systemctl daemon-reload
        │           systemctl enable bkeagent
        │           systemctl restart bkeagent
        │
        └── 6. PingBKEAgentOnNodes() — 验证升级后健康状态
```

---

### 八、Agent 监听切换流程

```
EnsureAgentSwitch 阶段（DeployPhases 最后一个）
  └── 读取 annotation "bke.bocloud.com/bkeagent-listener"
        ├── "current" 或缺失 → 跳过（已监听当前集群）
        └── "bkecluster" → 创建 Command CRD "SwitchCluster"
              ├── kubeconfig = {namespace}/{clusterName}-kubeconfig
              ├── clusterName = {clusterName}
              └── Agent 收到后切换 kubeconfig 指向目标集群
```

### 九、Agent 关闭/清理流程

```
EnsureDeleteOrReset 阶段
  └── ShutDownAgent()
        └── 创建 Command CRD "Shutdown"（BuiltIn 类型）
              └── Agent 收到后自行停止
```

### 十、DaemonSet 安装路径（替代方案）

来源：`cmd/bkeagent-launcher/main.go`（338 行）

通过 DaemonSet 容器 + `nsenter` 逃逸到宿主机安装：

```
bkeagent-launcher DaemonSet
  └── nsenter -t 1 -m -u -i -n -p sh -c '{cmd}'
        ├── startPre():
        │     systemctl stop bkeagent
        │     cp ./bkeagent → /usr/local/bin/bkeagent
        │     渲染 bkeagent.service → /etc/systemd/system/
        │     保存 kubeconfig → /etc/openFuyao/bkeagent/config
        │     写入 hostname → /etc/openFuyao/bkeagent/node
        │
        ├── start():
        │     systemctl daemon-reload
        │     systemctl start bkeagent
        │     systemctl enable bkeagent
        │
        └── startPost():
              HTTP :3377/readyz → 检查 systemctl is-active bkeagent
```

### 十一、Agent 运行时配置

| 配置项 | 值 | 来源 |
|:---|:---|:---|
| 日志路径 | `/var/log/openFuyao/bkeagent.log` | `utils/log/agent_config.go` |
| 日志格式 | JSON | 同上 |
| 日志大小限制 | 100MB | 同上 |
| 日志备份数 | 30 | 同上 |
| 日志保留天数 | 14 天 | 同上 |
| 健康检查端口 | `--health-port`（从集群配置读取） | systemd 模板渲染 |
| NTP 服务器 | `--ntpserver`（从集群配置读取） | systemd 模板渲染 |
| 管理集群连接 | `--kubeconfig=/etc/openFuyao/bkeagent/config` | systemd 模板固定 |

### 十二、关联 CRD

| CRD | 用途 | 代码位置 |
|:---|:---|:---|
| `commands.bkeagent.bocloud.com` | Agent 命令下发（Ping/Switch/Shutdown/Shell/BuiltIn） | `api/bkeagent/v1beta1/command_types.go` |
| `BKENode` | 节点状态追踪（NodeAgentPushedFlag/NodeAgentReadyFlag） | `api/capbke/v1beta1/` |
| `BKECluster` | 集群状态（AgentStatus: available/total） | `api/capbke/v1beta1/bkecluster_status.go` |

### 十三、节点状态标志位

| 标志位 | 值 | 设置时机 | 清除时机 |
|:---|:---:|:---|:---|
| `NodeAgentPushedFlag` | bit 0 (=1) | SSH 推送成功后 | Ping 失败时 |
| `NodeAgentReadyFlag` | bit 1 (=2) | Ping 成功后 | — |

### 十四、目标节点文件清单汇总

| 路径 | 类型 | 用途 |
|:---|:---|:---|
| `/usr/local/bin/bkeagent` | 二进制 | Agent 主程序 |
| `/etc/systemd/system/bkeagent.service` | 服务文件 | Systemd 单元 |
| `/etc/openFuyao/bkeagent/config` | 配置 | 管理集群 kubeconfig |
| `/etc/openFuyao/bkeagent/node` | 配置 | 节点主机名 |
| `/etc/openFuyao/certs/trust-chain.crt` | 证书 | 镜像仓库 CA 信任链 |
| `/etc/openFuyao/certs/{ca-cert,ca-key}` | 证书 | 集群 CA（可选） |
| `/etc/openFuyao/certs/cert_config/*` | 配置 | 15 个 CSR 签名配置 |
| `/var/log/openFuyao/bkeagent.log` | 日志 | Agent 运行日志 |
