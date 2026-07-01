# bkeagent 安装完整清单

## bkeagent 安装完整清单

### 一、二进制文件

| 文件 | 目标路径 | 来源 | 权限 |
|------|---------|------|------|
| `bkeagent` | `/usr/local/bin/bkeagent` | Provider 镜像或 HTTP 仓库 | 0755 |
| `bkeagent` (自更新) | `/etc/openFuyao/bkeagent/bin/bkeagent` | HTTP 仓库自更新 | - |
| `bkeagent.bak.{timestamp}` | `/usr/local/bin/` | 升级时备份（最多保留 3 个） | - |

**代码位置**：
- `pkg/phaseframe/phases/ensure_bke_agent.go:511-513`
- `pkg/phaseframe/phaseutil/agentssh/push_upgrade.go:126,179-180`

### 二、Systemd 服务文件

| 文件 | 目标路径 | 模板来源 |
|------|---------|---------|
| `bkeagent.service` | `/etc/systemd/system/bkeagent.service` | `hack/bkeagent.service.tmpl` |

**服务文件内容**：
```ini
[Unit]
Description= bkeagent
After=network.target

[Service]
Environment="DEBUG=true"
ExecStart=/usr/local/bin/bkeagent --kubeconfig=/etc/openFuyao/bkeagent/config --health-port= --ntpserver=
KillMode=process
RestartSec=5
Restart=on-failure
SuccessExitStatus=0

[Install]
WantedBy=multi-user.target
```
**代码位置**：
- `pkg/phaseframe/phaseutil/bkeagent_service.go:44-57`

### 三、配置文件

| 文件 | 目标路径 | 内容 |
|------|---------|------|
| `config` | `/etc/openFuyao/bkeagent/config` | Kubeconfig（管理集群访问凭证） |
| `node` | `/etc/openFuyao/bkeagent/node` | 节点 hostname |

**Kubeconfig 结构**：
- Cluster Name: `management-cluster`
- Context Name: `bkeagent-context`
- AuthInfo Name: `bkeagent-cert-user`
- 有效期：100 年

**代码位置**：
- `pkg/phaseframe/phaseutil/localkubeconfig.go:254-286`

### 四、目录结构

| 目录 | 路径 | 权限 | 用途 |
|------|------|------|------|
| bkeagent 工作目录 | `/etc/openFuyao/bkeagent` | 0777 | 主工作目录 |
| 自更新二进制 | `/etc/openFuyao/bkeagent/bin` | - | 自更新二进制路径 |
| 自更新脚本 | `/etc/openFuyao/bkeagent/scripts` | - | 自更新脚本路径 |
| Launcher 目录 | `/etc/openFuyao/bkeagent/launcher` | - | Launcher 工作目录 |
| 证书目录 | `/etc/openFuyao/certs` | 0755 | TLS 证书存储 |
| 证书配置目录 | `/etc/openFuyao/certs/cert_config` | - | CSR 配置文件 |

**代码位置**：
- `utils/const.go:33-35`
- `pkg/phaseframe/phases/ensure_bke_agent.go:45,49,512`

### 五、TLS 证书文件

| 文件 | 目标路径 | 条件 |
|------|---------|------|
| `trust-chain.crt` | `/etc/openFuyao/certs/trust-chain.crt` | 始终安装（如存在） |
| `global-ca.crt` | `/etc/openFuyao/certs/global-ca.crt` | 仅当 cluster-api addon 存在 |
| `global-ca.key` | `/etc/openFuyao/certs/global-ca.key` | 仅当 cluster-api addon 存在 |

**代码位置**：
- `pkg/phaseframe/phases/ensure_bke_agent.go:325-361`
- `pkg/certs/config_const.go:77-81`

### 六、CSR 配置文件（17 个）

| 文件 | 常量名 |
|------|--------|
| `cluster-ca-policy.json` | `ConfigKeyClusterCAPolicy` |
| `cluster-ca-csr.json` | `ConfigKeyClusterCACSR` |
| `sign-policy.json` | `ConfigKeySignPolicy` |
| `apiserver-csr.json` | `ConfigKeyAPIServerCSR` |
| `apiserver-etcd-client-csr.json` | `ConfigKeyAPIServerEtcdClientCSR` |
| `front-proxy-client-csr.json` | `ConfigKeyFrontProxyClientCSR` |
| `apiserver-kubelet-client-csr.json` | `ConfigKeyAPIServerKubeletClientCSR` |
| `front-proxy-ca-csr.json` | `ConfigKeyFrontProxyCACSR` |
| `etcd-ca-csr.json` | `ConfigKeyEtcdCACSR` |
| `etcd-server-csr.json` | `ConfigKeyEtcdServerCSR` |
| `etcd-healthcheck-client-csr.json` | `ConfigKeyEtcdHealthcheckClientCSR` |
| `etcd-peer-csr.json` | `ConfigKeyEtcdPeerCSR` |
| `admin-kubeconfig-csr.json` | `ConfigKeyAdminKubeConfigCSR` |
| `kubelet-kubeconfig-csr.json` | `ConfigKeyKubeletKubeConfigCSR` |
| `controller-manager-csr.json` | `ConfigKeyControllerManagerCSR` |
| `scheduler-csr.json` | `ConfigKeySchedulerCSR` |
| `kube-proxy-csr.json` | `ConfigKeyKubeProxyCSR` |

**安装路径**：`/etc/openFuyao/certs/cert_config/`

**代码位置**：
- `pkg/certs/config_const.go:16-64`
- `pkg/phaseframe/phases/ensure_bke_agent.go:384-410`

### 七、日志文件

| 文件 | 路径 | 配置 |
|------|------|------|
| `bkeagent.log` | `/var/log/openFuyao/bkeagent.log` | MaxSize: 100MB, MaxBackups: 30, MaxAge: 14天 |
| `bkeagent-update.log` | `/var/log/openFuyao/bkeagent-update.log` | 自更新日志 |

**代码位置**：
- `utils/log/agent_config.go:22`
- `pkg/job/builtin/selfupdate/update.sh:14`

### 八、自更新脚本

| 文件 | 目标路径 | 来源 |
|------|---------|------|
| `update.sh` | `/etc/openFuyao/bkeagent/scripts/update.sh` | 嵌入二进制（`//go:embed`） |

**代码位置**：
- `pkg/job/builtin/selfupdate/selfupdate.go:40-43,98-103`
- `pkg/job/builtin/selfupdate/update.sh`

### 九、RBAC 资源（管理集群）

| 资源类型 | 名称 | 用途 |
|---------|------|------|
| ClusterRole | `bkeagent-readwrite` | 读写权限 |
| ClusterRole | `bkeagent-configmap-only` | ConfigMap 专用权限 |
| ClusterRole | `bkeagent-cluster-access` | 集群访问权限 |
| RoleBinding | `bkeagent` (per namespace) | 命名空间绑定 |
| ClusterRoleBinding | `bkeagent-cluster-access` | 集群级绑定 |

**代码位置**：
- `pkg/phaseframe/phaseutil/localkubeconfig.go:374-589`

### 十、安装前清理

```bash
chmod 777 /usr/local/bin/
chmod 777 /etc/systemd/system/
systemctl stop bkeagent || true
systemctl disable bkeagent || true
systemctl daemon-reload || true
rm -rf /usr/local/bin/bkeagent* || true
rm -f /etc/systemd/system/bkeagent.service || true
rm -rf /etc/openFuyao/bkeagent || true
```
**代码位置**：`pkg/phaseframe/phases/ensure_bke_agent.go:470-482`

### 十一、安装后命令

```bash
systemctl daemon-reload
systemctl enable bkeagent
systemctl restart bkeagent
chmod 755 /usr/local/bin/
chmod 755 /etc/systemd/system/
```
**代码位置**：
- `pkg/phaseframe/phases/ensure_bke_agent.go:517-519`
- `pkg/phaseframe/phaseutil/agentssh/push_upgrade.go:181-183`

### 十二、目标节点完整文件树

```
/usr/local/bin/
├── bkeagent                              # 主二进制
└── bkeagent.bak.{timestamp}              # 备份（最多3个）

/etc/systemd/system/
└── bkeagent.service                      # Systemd 服务

/etc/openFuyao/
├── bkeagent/
│   ├── config                            # Kubeconfig
│   ├── node                              # 节点 hostname
│   ├── bin/
│   │   └── bkeagent                      # 自更新二进制
│   ├── scripts/
│   │   └── update.sh                     # 自更新脚本
│   └── launcher/                         # Launcher 目录
└── certs/
    ├── trust-chain.crt                   # 信任链证书
    ├── global-ca.crt                     # 全局 CA（可选）
    ├── global-ca.key                     # 全局 CA 密钥（可选）
    └── cert_config/                      # 17 个 CSR 配置文件
        ├── cluster-ca-policy.json
        ├── cluster-ca-csr.json
        ├── sign-policy.json
        ├── apiserver-csr.json
        ├── apiserver-etcd-client-csr.json
        ├── front-proxy-client-csr.json
        ├── apiserver-kubelet-client-csr.json
        ├── front-proxy-ca-csr.json
        ├── etcd-ca-csr.json
        ├── etcd-server-csr.json
        ├── etcd-healthcheck-client-csr.json
        ├── etcd-peer-csr.json
        ├── admin-kubeconfig-csr.json
        ├── kubelet-kubeconfig-csr.json
        ├── controller-manager-csr.json
        ├── scheduler-csr.json
        └── kube-proxy-csr.json

/var/log/openFuyao/
├── bkeagent.log                          # 主日志
└── bkeagent-update.log                   # 更新日志
```

### 十三、关键源文件索引

| 源文件 | 用途 |
|--------|------|
| `pkg/phaseframe/phases/ensure_bke_agent.go` | 初始安装 Phase |
| `pkg/phaseframe/phases/ensure_agent_upgrade.go` | 升级 Phase |
| `pkg/phaseframe/phaseutil/bkeagent_service.go` | 服务文件渲染 |
| `pkg/phaseframe/phaseutil/agent.go` | Agent 推送/检测工具 |
| `pkg/phaseframe/phaseutil/agentssh/push_upgrade.go` | SSH 升级实现 |
| `pkg/phaseframe/phaseutil/agentssh/artifacts.go` | 制品下载/暂存 |
| `pkg/phaseframe/phaseutil/localkubeconfig.go` | Kubeconfig 生成 & RBAC |
| `hack/bkeagent.service.tmpl` | 服务文件模板 |
| `pkg/job/builtin/selfupdate/selfupdate.go` | 自更新插件 |
| `pkg/certs/config_const.go` | 证书路径常量 |
| `utils/const.go` | 通用常量（路径） |
| `utils/log/agent_config.go` | 日志配置 |

### 十四、对设计文档的影响

当前设计文档中 bkeagent 的 ComponentVersion YAML 示例只包含：
- 1 个 artifact（`bkeagent`）
- 简单的 installScript/uninstallScript

但实际安装包含：
- **1 个二进制** + **1 个服务文件** + **2 个配置文件** + **17 个 CSR 配置** + **1 个自更新脚本** + **多个目录** + **日志文件**

**关键差异**：
1. CSR 配置文件（17 个）在设计文档中未体现
2. 自更新脚本 `update.sh` 未体现
3. Launcher 目录未体现
4. 日志目录未体现


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


# bkeagent 更新计划：

## 更新计划

### 1. 更新 bkeagent ComponentVersion YAML（11.4.2 节）

**当前设计**：
```yaml
artifacts:
  - name: bkeagent
    url: "..."
    installPath: "/usr/local/bin"

configTemplates:
  - name: bkeagent.conf
  - name: tls.crt
  - name: tls.key
  - name: ca.crt
  - name: kubeconfig
```

**实际安装**：
- **二进制**：1 个（bkeagent）
- **服务文件**：1 个（bkeagent.service）
- **配置文件**：2 个（config/kubeconfig, node）
- **CSR 配置**：17 个（证书签名请求配置）
- **自更新脚本**：1 个（update.sh）
- **目录**：多个（/etc/openFuyao/bkeagent, /etc/openFuyao/certs 等）

### 2. 需要补充的内容

#### 2.1 configTemplates 扩展

```yaml
configTemplates:
  # 基础配置
  - name: bkeagent.conf
    path: "/etc/openFuyao/bkeagent/config"
    content: |
      cluster_name: {{clusterName}}
      api_server: {{apiServer}}
      ...

  - name: node
    path: "/etc/openFuyao/bkeagent/node"
    content: "{{nodeHostname}}"

  # 服务文件
  - name: bkeagent.service
    path: "/etc/systemd/system/bkeagent.service"
    content: |
      [Unit]
      Description=BKE Agent
      ...

  # TLS 证书（条件安装）
  - name: trust-chain.crt
    path: "/etc/openFuyao/certs/trust-chain.crt"
    secretRef:
      name: bkeagent-tls
      namespace: "{{clusterNamespace}}"
      key: trust-chain.crt

  # CSR 配置文件（17 个）
  - name: cluster-ca-policy.json
    path: "/etc/openFuyao/certs/cert_config/cluster-ca-policy.json"
    content: |
      {...}
  # ... 其他 16 个 CSR 配置
```

#### 2.2 installScript 扩展

```yaml
installScript: |
  #!/bin/bash
  set -e

  # 1. 创建目录
  mkdir -p /etc/openFuyao/bkeagent
  mkdir -p /etc/openFuyao/bkeagent/bin
  mkdir -p /etc/openFuyao/bkeagent/scripts
  mkdir -p /etc/openFuyao/certs
  mkdir -p /etc/openFuyao/certs/cert_config
  mkdir -p /var/log/openFuyao

  # 2. 停止旧服务
  systemctl stop bkeagent || true

  # 3. 备份旧版本
  {{if .isUpgrade}}
  cp /usr/local/bin/bkeagent /usr/local/bin/bkeagent.bak.$(date +%s)
  {{end}}

  # 4. 安装二进制
  install -m 0755 {{artifact.bkeagent.path}} /usr/local/bin/bkeagent

  # 5. 安装配置文件（由 ConfigRenderer 自动上传）
  # - bkeagent.conf → /etc/openFuyao/bkeagent/config
  # - node → /etc/openFuyao/bkeagent/node
  # - bkeagent.service → /etc/systemd/system/bkeagent.service
  # - TLS 证书 → /etc/openFuyao/certs/
  # - CSR 配置 → /etc/openFuyao/certs/cert_config/

  # 6. 安装自更新脚本
  # update.sh 由 bkeagent 二进制自动部署到 /etc/openFuyao/bkeagent/scripts/

  # 7. 启动服务
  systemctl daemon-reload
  systemctl enable bkeagent
  systemctl start bkeagent

  # 8. 验证
  sleep 2
  systemctl is-active bkeagent
```

#### 2.3 uninstallScript 扩展

```yaml
uninstallScript: |
  #!/bin/bash
  systemctl stop bkeagent || true
  systemctl disable bkeagent || true
  
  # 删除二进制
  rm -f /usr/local/bin/bkeagent
  rm -f /usr/local/bin/bkeagent.bak.*
  
  # 删除服务文件
  rm -f /etc/systemd/system/bkeagent.service
  
  # 删除工作目录
  rm -rf /etc/openFuyao/bkeagent
  
  # 删除证书目录（可选，保留证书以便重新安装）
  # rm -rf /etc/openFuyao/certs
  
  # 删除日志
  rm -f /var/log/openFuyao/bkeagent.log
  rm -f /var/log/openFuyao/bkeagent-update.log
  
  systemctl daemon-reload
```

### 3. 需要澄清的问题

1. **CSR 配置文件**：这 17 个文件是每次安装都需要，还是只在首次安装时生成？
   - 如果是首次安装，应该放在 `installScript` 中
   - 如果是每次升级，应该放在 `configTemplates` 中
2. **自更新脚本**：`update.sh` 是由 bkeagent 二进制自动部署，还是需要手动安装？
3. **Launcher 目录**：`/etc/openFuyao/bkeagent/launcher` 是否需要手动创建？
4. **RBAC 资源**：这些是在管理集群创建的，不属于目标节点安装，不需要在 ComponentVersion 中定义

### 4. 设计文档更新步骤

1. 更新 11.4.2 节的 bkeagent YAML 示例
2. 扩展 configTemplates 列表
3. 更新 installScript/uninstallScript
4. 添加必要的注释说明

# 根据代码分析，这 17 个 CSR 配置文件的作用如下：

## 用途

这些文件是 **CFSSL（CloudFlare PKI 工具包）配置模板**，用于定义 Kubernetes PKI 证书的参数：

| 文件 | 用途 |
|------|------|
| `cluster-ca-policy.json` | 集群根 CA 签名策略 |
| `cluster-ca-csr.json` | 集群根 CA 证书 CSR |
| `sign-policy.json` | 通用签名策略 |
| `apiserver-csr.json` | API Server 服务端证书 |
| `apiserver-etcd-client-csr.json` | API Server 访问 etcd 的客户端证书 |
| `front-proxy-client-csr.json` | Front Proxy 客户端证书 |
| `apiserver-kubelet-client-csr.json` | API Server 访问 kubelet 的客户端证书 |
| `front-proxy-ca-csr.json` | Front Proxy CA 证书 |
| `etcd-ca-csr.json` | etcd CA 证书 |
| `etcd-server-csr.json` | etcd 服务端证书 |
| `etcd-healthcheck-client-csr.json` | etcd 健康检查客户端证书 |
| `etcd-peer-csr.json` | etcd 节点间通信证书 |
| `admin-kubeconfig-csr.json` | admin kubeconfig 证书 |
| `kubelet-kubeconfig-csr.json` | kubelet kubeconfig 证书 |
| `controller-manager-csr.json` | controller-manager 证书 |
| `scheduler-csr.json` | scheduler 证书 |
| `kube-proxy-csr.json` | kube-proxy 证书 |

## 是否动态生成？

**静态 JSON 文件，但包含模板变量**：
1. **文件本身是静态的**：这些 JSON 文件在安装时由 bkeagent 从 Provider 节点复制到目标节点的 `/etc/openFuyao/certs/cert_config/` 目录
2. **部分字段包含模板变量**：例如 `hosts` 字段可以包含 `{{ClusterName}}`、`{{AdvertiseAddress}}` 等变量，在证书生成时由 `CertConfigLoader.processTemplateHosts()` 动态替换
3. **加载方式**（`pkg/certs/config.go`）：
   - **优先从 ConfigMap 加载**：`kube-system/cluster-cert-config`
   - **回退到本地文件**：`/etc/openFuyao/certs/cert_config/`
   - 如果都不存在，使用默认证书逻辑

## 消费者

| 消费者 | 位置 | 作用 |
|--------|------|------|
| 管理平面控制器 | `pkg/certs/config.go` | 加载配置，应用到证书生成 |
| 目标节点 bkeagent | `utils/bkeagent/pkiutil/kubeconfig.go` | 生成本地证书 |

## 总结
这些文件是**可定制的证书配置模板**，允许用户自定义 Kubernetes 集群的 PKI 证书参数（如 CN、O、OU、有效期、密钥大小等）。文件本身是静态 JSON，但支持模板变量在运行时动态替换。

# 根据代码分析，bkeagent 的配置文件如下：

## 核心配置文件

### 1. Kubeconfig（管理集群访问凭证）

| 属性 | 值 |
|------|-----|
| **文件路径** | `/etc/openFuyao/bkeagent/config` |
| **用途** | bkeagent 访问管理集群 API Server 的凭证，用于监听 Command CRD、上报状态、执行节点操作 |

**内容结构**：
- Cluster Name: `management-cluster`
- Context Name: `bkeagent-context`
- AuthInfo Name: `bkeagent-cert-user`
- 认证方式：X.509 客户端证书（100 年有效期）
- **两种变体**：
  1. **最小权限 kubeconfig**（首选）：通过 `GenerateLowPrivilegeKubeConfig()` 生成，RBAC 限制为：
     - `bkeagent-readwrite` ClusterRole（secrets get, configmaps CRUD）
     - `bkeagent-configmap-only` ClusterRole（configmaps 只读）
     - `bkeagent-cluster-access` ClusterRole（BKE CRDs 读取, Commands CRUD）
  2. **完整本地 kubeconfig**（回退）：从 Secret 读取，用于 cluster-api addon 存在时

**生成来源**：

| 生成者 | 文件 | 机制 |
|--------|------|------|
| EnsureBKEAgent（初始部署） | `pkg/phaseframe/phases/ensure_bke_agent.go:516` | SSH 命令：`echo -e {kubeconfig} > /etc/openFuyao/bkeagent/config` |
| PushAgent（旧版推送） | `pkg/phaseframe/phaseutil/agent.go:307` | SSH 命令 |
| BKEAgent Launcher（容器部署） | `cmd/bkeagent-launcher/main.go:186-198` | 通过 `nsenter` 复制到宿主机 |

**读取者**：
- `cmd/bkeagent/main.go:104`：`ctrl.GetConfigOrDie()` 从 `--kubeconfig` 标志读取
- `utils/bkeagent/cluster/clusterutil.go:31-43`：`kubeclient.NewClient(kubeconfig)`

### 2. 节点标识文件

| 属性 | 值 |
|------|-----|
| **文件路径** | `/etc/openFuyao/bkeagent/node` |
| **用途** | 包含节点 hostname，bkeagent 用于在管理集群中标识自己 |

**内容结构**：纯文本，单行 hostname（如 `node-01`）

**生成来源**：

| 生成者 | 文件 | 机制 |
|--------|------|------|
| EnsureBKEAgent | `pkg/phaseframe/phases/ensure_bke_agent.go` | 通过 ping 响应获取 hostname |
| BKEAgent Launcher | `cmd/bkeagent-launcher/main.go:200-207` | `prepareNodeFile()` 通过 `nsenter` 写入 |
| Agent SSH Upgrade | `pkg/phaseframe/phaseutil/agentssh/push_upgrade.go:131` | `echo {hostname} > /etc/openFuyao/bkeagent/node` |
| bkeagent 自身（自动创建） | `utils/utils.go:108-114` | 如果文件不存在，写入 `os.Hostname()` |

**读取者**：
- `utils.HostName()`（`utils/utils.go:99-131`）：读取文件内容作为节点名
- `cmd/bkeagent/main.go:120-130`：`CommandReconciler` 使用此 hostname 作为 `NodeName`

### 3. Systemd 服务文件

| 属性 | 值 |
|------|-----|
| **文件路径** | `/etc/systemd/system/bkeagent.service` |
| **用途** | 定义 bkeagent 进程的启动、重启和管理方式 |

**内容结构**（模板）：
```ini
[Unit]
Description= bkeagent
After=network.target

[Service]
Environment="DEBUG=true"
ExecStart=/usr/local/bin/bkeagent --kubeconfig=/etc/openFuyao/bkeagent/config --health-port={{.healthPort}} --ntpserver={{.ntpServer}}
KillMode=process
RestartSec=5
Restart=on-failure
SuccessExitStatus=0

[Install]
WantedBy=multi-user.target
```
**生成来源**：

| 生成者 | 文件 | 机制 |
|--------|------|------|
| EnsureBKEAgent | `pkg/phaseframe/phases/ensure_bke_agent.go:234-249` | 从 Provider 容器读取 `/bkeagent.service.tmpl`，渲染后 SSH 上传 |
| RenderBKEAgentServiceFile | `pkg/phaseframe/phaseutil/bkeagent_service.go:52-57` | 替换 `--ntpserver=` 和 `--health-port=` |
| Agent SSH Upgrade | `pkg/phaseframe/phaseutil/agentssh/push_upgrade.go:174-177` | 优先从 HTTP 仓库下载，回退到模板渲染 |
| BKEAgent Launcher | `cmd/bkeagent-launcher/main.go:159-184` | 渲染嵌入模板，通过 `nsenter` 复制 |

## 附加文件（TLS 证书和 CSR 配置）

虽然这些不是 bkeagent 自身的配置，但 EnsureBKEAgent 阶段也会上传到节点：

| 文件 | 路径 | 用途 |
|------|------|------|
| 信任链证书 | `/etc/openFuyao/certs/trust-chain.crt` | 集群 CA 证书链 |
| 全局 CA 证书 | `/etc/openFuyao/certs/global-ca.crt` | 仅当 cluster-api addon 存在 |
| 全局 CA 密钥 | `/etc/openFuyao/certs/global-ca.key` | 仅当 cluster-api addon 存在 |
| 17 个 CSR 配置文件 | `/etc/openFuyao/certs/cert_config/` | 各集群组件的证书签名请求配置 |

这些文件由 `EnsureBKEAgent.prepareFileUploadList()`（`pkg/phaseframe/phases/ensure_bke_agent.go:320-411`）生成/上传。

## 数据流总结

```
管理集群 (Provider / capbke controller)
    |
    |-- EnsureBKEAgent 阶段 (SSH 推送)
    |       |
    |       |-- GetLeastPrivilegeKubeConfig() 或 GetLocalKubeConfig()
    |       |       从 K8s Secret 读取 kubeconfig
    |       |
    |       |-- RenderBKEAgentServiceFile()
    |       |       从 Provider 容器读取 /bkeagent.service.tmpl
    |       |       替换 --ntpserver= 和 --health-port=
    |       |
    |       |-- SSH 到每个节点:
    |       |       echo {kubeconfig} > /etc/openFuyao/bkeagent/config
    |       |       cp bkeagent.service  -> /etc/systemd/system/
    |       |       cp bkeagent binary   -> /usr/local/bin/
    |       |       systemctl daemon-reload && enable && restart
    |
    v
目标节点:
    /etc/openFuyao/bkeagent/config    <-- kubeconfig（管理集群访问）
    /etc/openFuyao/bkeagent/node      <-- hostname（节点标识）
    /etc/systemd/system/bkeagent.service  <-- systemd 服务（启动配置）
    /usr/local/bin/bkeagent           <-- 二进制
```

# func (e *EnsureBKEAgent) loadLocalKubeConfig() 中的配置是否可以在bkeagent的ComponentVersion中表达出来？phaseutil.GetLeastPrivilegeKubeConfig()与phaseutil.GetLocalKubeConfig()的逻辑及phaseutil.CreateBKEAgentRBACWithLocalKubeConfig()逻辑

## Analysis: bkeagent kubeconfig 配置能否在 ComponentVersion 中表达

### 1. 当前 `loadLocalKubeConfig()` 逻辑总结

```
loadLocalKubeConfig()
├── 检查 cluster-api addon 是否存在
├── 不存在:
│   ├── 尝试 GetLeastPrivilegeKubeConfig()
│   │   → 读 Secret kube-system/leastprivilegekubeconfig
│   │   → 返回预生成的低权限 kubeconfig (用户: bkeagent-cert-user)
│   ├── 成功 → GetLocalKubeConfig() + CreateBKEAgentRBACWithLocalKubeConfig()
│   │   → 用 admin kubeconfig 创建 RBAC (ClusterRole + RoleBinding)
│   └── 失败 → 回退到 GetLocalKubeConfig() (admin kubeconfig)
└── 存在:
    └── 直接用 GetLocalKubeConfig() (admin kubeconfig)
```
最终 kubeconfig 通过 SSH 写入每个节点的 `/etc/openFuyao/bkeagent/config`。

### 2. 三个函数的具体逻辑

| 函数 | 输入 | 输出 | 核心逻辑 |
|------|------|------|---------|
| `GetLocalKubeConfig()` | K8s client | `[]byte` | 读 Secret `kube-system/localkubeconfig` 的 `"config"` key，返回 admin kubeconfig |
| `GetLeastPrivilegeKubeConfig()` | K8s client | `[]byte` | 读 Secret `kube-system/leastprivilegekubeconfig` 的 `"config"` key，返回低权限 kubeconfig |
| `CreateBKEAgentRBACWithLocalKubeConfig()` | admin kubeconfig + BKECluster | error | 用 admin kubeconfig 创建高权限 client，然后创建 3 个 ClusterRole + RoleBinding/ClusterRoleBinding |

**Kubeconfig 内容特征**：使用**嵌入式证书数据**（`CertificateAuthorityData`、`ClientCertificateData`、`ClientKeyData`），不是文件路径引用。

**RBAC 资源清单**：

| 资源 | 名称 | 权限 |
|------|------|------|
| ClusterRole | `bkeagent-readwrite` | secrets get; configmaps get/list/watch/create/update |
| ClusterRole | `bkeagent-configmap-only` | configmaps get/list/watch |
| ClusterRole | `bkeagent-cluster-access` | BKE CRDs (bkeclusters/bkenodes/containerdconfigs/kubeletconfigs) read; commands CRUD |
| RoleBinding | `bkeagent` (per namespace) | → `bkeagent-readwrite` on `cluster-system`, `user-system`, `bkeCluster.Namespace` |
| RoleBinding | `bkeagent` (configmap-only) | → `bkeagent-configmap-only` on `cluster-system`, `user-system` |
| ClusterRoleBinding | `bkeagent-cluster-access` | → `bkeagent-cluster-access` cluster-wide |

### 3. 当前 KEP-6 设计能否表达？

**当前 12.4.2 bkeagent ComponentVersion 设计中的 kubeconfig 部分**：
```yaml
configTemplates:
  - name: kubeconfig
    path: "/etc/openFuyao/bkeagent/config"
    kubeconfigTemplate:
      clusterName: "management-cluster"
      apiServer: "{{apiServer}}"
      caCertPath: "/etc/openFuyao/certs/global-ca.crt"
      clientCertPath: "/etc/openFuyao/certs/bkeagent-client.crt"
      clientKeyPath: "/etc/openFuyao/certs/bkeagent-client.key"
```
**存在以下差距**：

| 差距 | 当前代码行为 | KEP-6 设计 | 影响 |
|------|-------------|-----------|------|
| **证书嵌入方式** | kubeconfig 内嵌 base64 证书数据 (`CertificateAuthorityData`) | kubeconfig 引用文件路径 (`caCertPath`) | bkeagent 启动时需要文件存在，但当前代码不单独推送证书文件 |
| **kubeconfig 来源** | 从 Secret 读取预生成的 kubeconfig | 动态生成 kubeconfig | 预生成的 kubeconfig 包含特定用户身份 (`bkeagent-cert-user`)，动态生成需要确保证书来源一致 |
| **条件选择逻辑** | cluster-api addon 存在时用 admin，不存在时用 least-privilege | 无条件逻辑 | 需要决定用哪种 kubeconfig |
| **RBAC 创建** | `CreateBKEAgentRBACWithLocalKubeConfig()` 硬编码在 Phase 中 | 未设计 | RBAC 资源需要在 bkeagent 安装前创建 |

### 4. 建议方案

**可以表达，但需要拆分关注点 + 补充设计**：

#### 4a. RBAC 资源 → 独立 YAML 组件

RBAC 资源 (ClusterRole/RoleBinding/ClusterRoleBinding) 是声明式 K8s 资源，应建模为独立 YAML 组件：
```yaml
# bke-manifests/bkeagent-rbac/v1.0.0/component.yaml
spec:
  name: bkeagent-rbac
  type: yaml
  version: v1.0.0
  yaml:
    manifests:
      - url: "https://release-repo/bkeagent-rbac/v1.0.0/rbac.yaml"
  # 无健康检查 (RBAC 创建即生效)
```
DAG 依赖：`bkeagent-rbac` → `bkeagent` (RBAC 先于 bkeagent 安装)

#### 4b. Kubeconfig → secretRef 模式

保留预生成的 kubeconfig Secret，用 `secretRef` 直接引用：
```yaml
configTemplates:
  - name: kubeconfig
    path: "/etc/openFuyao/bkeagent/config"
    mode: "0600"
    owner: "root:root"
    secretRef:
      name: "bkeagent-kubeconfig"       # 预生成的 kubeconfig Secret
      namespace: "{{clusterNamespace}}"
      key: "config"
```
这保持了与当前代码的等价性——kubeconfig 内容不变，只是从 Phase 硬编码改为声明式引用。

#### 4c. 条件选择逻辑 → 控制器层

cluster-api addon 的条件判断不应放入 ComponentVersion，而由控制器层处理：
- 控制器在构建 TemplateContext 时判断 addon 类型
- 通过 `{{.Variables.kubeconfigSecretName}}` 变量控制 secretRef 引用哪个 Secret
- 或者：DAG 构建时根据 addon 类型选择不同的 bkeagent ComponentVersion

#### 4d. 需要补充的设计点

当前 KEP-6 需要补充：
1. **bkeagent-rbac YAML 组件**的 ComponentVersion 定义
2. **kubeconfig Secret 的生成时机**：当前由集群初始化流程创建，需明确在新架构中由哪个组件/Phase 负责
3. **secretRef 支持变量渲染**：当前设计中 `secretRef.namespace` 支持模板变量，但 `secretRef.name` 也需要支持（用于条件选择不同 Secret）
4. **bkeagent ComponentVersion 中 kubeconfig 的模板方式选择**：是用 `kubeconfigTemplate`（文件路径引用）还是 `secretRef`（嵌入数据）？建议用 `secretRef` 保持等价性

# func (e *EnsureBKEAgent) loadLocalKubeConfig() 中为什么设计如此复杂，从代码库逻辑分析是否可以简化？

## 分析：`loadLocalKubeConfig()` 复杂度来源与简化可行性

### 1. 当前逻辑决策树

```
loadLocalKubeConfig()
│
├── hasClusterAPI == true (cluster-api addon 存在)
│   └── GetLocalKubeConfig()  ← 直接用 admin kubeconfig
│
└── hasClusterAPI == false (cluster-api addon 不存在)
    ├── 尝试 GetLeastPrivilegeKubeConfig()
    │   ├── 成功 → GetLocalKubeConfig() + CreateBKEAgentRBAC()
    │   │          ← 用低权限 kubeconfig + 创建 RBAC
    │   └── 失败 → GetLocalKubeConfig()
    │              ← 回退到 admin kubeconfig
```

### 2. 关键发现：存在逻辑矛盾

经过追踪代码库，发现一个关键事实：

| Secret | 创建位置 | 创建时机 | 创建者 |
|--------|---------|---------|--------|
| `kube-system/localkubeconfig` | **remote cluster** (目标集群) | `EnsureAddonDeploy.handleClusterAPI()` | `createClusterAPILocalkubeconfigSecret()` |
| `kube-system/leastprivilegekubeconfig` | **remote cluster** (目标集群) | `EnsureAddonDeploy.handleClusterAPI()` | `createClusterAPILeastPrivilegeKubeConfigSecret()` |

两个 Secret 都只在 `EnsureAddonDeploy` 部署 `cluster-api` addon 时创建在**目标集群**。

但 `loadLocalKubeConfig()` 通过 `e.Ctx.Untie()` 获取的 `c client.Client` 是**管理集群**的 controller-runtime client。这意味着：
- `GetLocalKubeConfig(ctx, c)` 读的是管理集群的 `kube-system/localkubeconfig`
- `GetLeastPrivilegeKubeConfig(ctx, c)` 读的是管理集群的 `kube-system/leastprivilegekubeconfig`

**矛盾点**：`leastprivilegekubeconfig` 只在 cluster-api addon 部署时创建（在目标集群），但 `loadLocalKubeConfig()` 在 `hasClusterAPI == false` 时尝试读取它。如果 Secret 只存在于目标集群，那管理集群上永远读不到，这个分支**必然失败**，总是回退到 admin kubeconfig。

进一步看 `hasClusterAPI == true` 分支：cluster-api 存在时直接用 admin kubeconfig，跳过了 least-privilege 尝试。但 `leastprivilegekubeconfig` 恰恰只在 cluster-api 部署时才被创建。

**结论**：`hasClusterAPI` 的条件判断逻辑可能是历史遗留问题，实际运行中大概率总是走 admin kubeconfig 路径。

### 3. 复杂度来源拆解

| 复杂度来源 | 代码行数 | 是否必要 |
|-----------|---------|---------|
| `hasClusterAPI` 条件判断 | ~15 行 | **不必要** — 逻辑矛盾，实际总是走 admin |
| `GetLeastPrivilegeKubeConfig` 尝试 | ~10 行 | **不必要** — 在管理集群读不到，总是失败 |
| fallback 逻辑 | ~10 行 | **不必要** — 因为上面的尝试总是失败 |
| `CreateBKEAgentRBACWithLocalKubeConfig` | ~50 行 (独立函数) | **需要保留** — 但应改为声明式 |
| **核心有效逻辑** | **1 行** | `GetLocalKubeConfig()` |

### 4. 简化方案

#### 4a. 当前代码可简化为

```go
func (e *EnsureBKEAgent) loadLocalKubeConfig() error {
    ctx, c, _, _, log := e.Ctx.Untie()
    
    localKubeConfig, err := phaseutil.GetLocalKubeConfig(ctx, c)
    if err != nil {
        log.Error(constant.BKEAgentNotReadyReason, "Failed to get local kubeconfig, err: %v", err)
        return errors.Wrap(err, "failed to get local kubeconfig")
    }
    
    e.localKubeConfig = localKubeConfig
    return nil
}
```
**理由**：
- `hasClusterAPI` 分支实际效果等同于直接用 `GetLocalKubeConfig()`
- `GetLeastPrivilegeKubeConfig()` 在管理集群读不到，总是失败后 fallback 到 `GetLocalKubeConfig()`
- 去掉条件判断后，所有路径都归结为 `GetLocalKubeConfig()`

#### 4b. RBAC 创建应改为声明式

`CreateBKEAgentRBACWithLocalKubeConfig()` 创建的 RBAC 资源是固定的：

| 资源 | 名称 | 权限 |
|------|------|------|
| ClusterRole | `bkeagent-readwrite` | secrets get; configmaps get/list/watch/create/update |
| ClusterRole | `bkeagent-configmap-only` | configmaps get/list/watch |
| ClusterRole | `bkeagent-cluster-access` | BKE CRDs read; commands CRUD |
| RoleBinding | `bkeagent` (per namespace) | 绑定到 `cluster-system`, `user-system`, `bkeCluster.Namespace` |
| ClusterRoleBinding | `bkeagent-cluster-access` | 集群级绑定 |

这些资源完全可以在 bkeagent 的 ComponentVersion 中定义为 YAML 类型组件，或作为 `resources[]` 内联声明。

#### 4c. KEP-6 中的表达

在 KEP-6 新架构中：
1. **kubeconfig** → 用 `secretRef` 直接引用 `kube-system/localkubeconfig`：
```yaml
configTemplates:
  - name: kubeconfig
    path: "/etc/openFuyao/bkeagent/config"
    mode: "0600"
    secretRef:
      name: localkubeconfig
      namespace: kube-system
      key: config
```
2. **RBAC 资源** → 独立 YAML 组件或 bkeagent 的 `resources[]`：
```yaml
# 方式一：bkeagent ComponentVersion 的 resources[] 中声明
resources:
  - kind: ClusterRole
    apiVersion: rbac.authorization.k8s.io/v1
    name: bkeagent-readwrite
    manifest: |
      ...ClusterRole YAML...
  - kind: ClusterRoleBinding
    ...

# 方式二：独立 bkeagent-rbac YAML 组件 (推荐)
# DAG 依赖：bkeagent-rbac → bkeagent
```
3. **`CreateBKEAgentRBACWithLocalKubeConfig()` 函数** → 不再需要，RBAC 由 YAML 组件的 `ApplyComponent()` 声明式创建

### 5. 总结

| 维度 | 当前 | 简化后 |
|------|------|--------|
| kubeconfig 获取 | 3 分支 + fallback | `GetLocalKubeConfig()` 一行 |
| RBAC 创建 | 硬编码 ~50 行 Go 代码 | 声明式 YAML 资源 |
| `hasClusterAPI` 判断 | 需要遍历 addons 列表 | 删除（逻辑矛盾，无实际效果） |
| 代码行数 | ~50 行 (loadLocalKubeConfig) | ~10 行 |
| KEP-6 表达 | 未涉及 | `secretRef` + YAML 组件 |

**建议**：在 KEP-6 文档中补充此简化分析，明确 bkeagent 的 kubeconfig 使用 `secretRef` 模式引用预生成的 Secret，RBAC 资源改为声明式 YAML 组件。需要确认是否更新文档？

# bkeagent 的 kubeconfig使用管理集群的admin kubeconfig，如何配置ComponentVersion
## 分析：bkeagent kubeconfig 在 ComponentVersion 中的配置

### 1. 关键事实：`kube-system/localkubeconfig` Secret 的来源

经过全面搜索代码库，确认：

| 事实 | 说明 |
|------|------|
| **创建位置** | 管理集群 |
| **创建者** | **非本代码库创建** — 是外部预置的前提条件（由 `bke init` CLI 或集群管理员手动创建） |
| **内容** | 管理集群的 admin kubeconfig（等价于 `/etc/rancher/k3s/k3s.yaml`） |
| **格式** | 嵌入式证书数据（`certificate-authority-data`、`client-certificate-data`、`client-key-data`） |

代码库中唯一创建 `localkubeconfig` Secret 的地方是 `ensure_addon_deploy.go`，但它创建在**目标集群**（通过 `remoteClient`），不是管理集群。

### 2. bkeagent 如何使用这个 kubeconfig

```
Controller (SSH push)
  │
  │  echo -e %q > /etc/openFuyao/bkeagent/config
  │  (写入管理集群 admin kubeconfig 内容)
  │
  ▼
Node: /etc/openFuyao/bkeagent/config
  │
  ▼
systemd: ExecStart=/usr/local/bin/bkeagent --kubeconfig=/etc/openFuyao/bkeagent/config
  │
  ▼
controller-runtime: ctrl.GetConfigOrDie() 消费 --kubeconfig flag
  │
  ▼
bkeagent → 管理集群 API Server (完整 admin 权限)
```
bkeagent 需要管理集群 admin 权限来：
- Watch BKE CRDs (bkeclusters, bkenodes, containerdconfigs, kubeletconfigs)
- CRUD commands (bkeagent.bocloud.com)
- 读写 configmaps/secrets (cluster-system, user-system 命名空间)

### 3. `loadLocalKubeConfig()` 复杂度分析 — 可简化

当前代码有三条路径，但存在逻辑矛盾：
```
loadLocalKubeConfig()
├── hasClusterAPI == true
│   └── GetLocalKubeConfig()                    ← admin kubeconfig
│
└── hasClusterAPI == false
    ├── GetLeastPrivilegeKubeConfig()
    │   ├── 成功 → CreateBKEAgentRBAC()          ← 低权限 kubeconfig + 创建 RBAC
    │   └── 失败 → GetLocalKubeConfig()          ← admin kubeconfig (fallback)
```
**矛盾点**：`leastprivilegekubeconfig` Secret 只在 `EnsureAddonDeploy.handleClusterAPI()` 中创建，且创建在**目标集群**（`remoteClient`）。而 `GetLeastPrivilegeKubeConfig()` 通过管理集群 client 读取。管理集群上不存在此 Secret，因此**必然失败**，总是 fallback 到 admin kubeconfig。

**结论**：所有路径实际都归结为 `GetLocalKubeConfig()`，整个函数可简化为：
```go
func (e *EnsureBKEAgent) loadLocalKubeConfig() error {
    ctx, c, _, _, log := e.Ctx.Untie()
    localKubeConfig, err := phaseutil.GetLocalKubeConfig(ctx, c)
    if err != nil {
        return errors.Wrap(err, "failed to get local kubeconfig")
    }
    e.localKubeConfig = localKubeConfig
    return nil
}
```

### 4. ComponentVersion 配置方案

#### 4a. kubeconfig → `secretRef` 模式

当前 KEP-6 12.4.2 设计使用 `kubeconfigTemplate`（文件路径引用），但实际 kubeconfig 是嵌入式证书数据。应改为 `secretRef`：
```yaml
# bke-manifests/bkeagent/v2.6.0/component.yaml
configTemplates:
  - name: kubeconfig
    path: "/etc/openFuyao/bkeagent/config"
    mode: "0600"
    owner: "root:root"
    secretRef:
      name: localkubeconfig          # 管理集群 kube-system/localkubeconfig Secret
      namespace: kube-system         # 固定值，不支持模板变量
      key: config                    # Secret data key
```
**数据流**：
```
管理集群 Secret kube-system/localkubeconfig
  │
  │  ConfigRenderer.renderSecretTemplate()
  │  → client.Get(ctx, {kube-system, localkubeconfig}, secret)
  │  → secret.Data["config"]
  │
  ▼
BinaryInstaller.executeInstall()
  │
  │  ssh.Upload(ctx, nodeIP, kubeconfigBytes, "/etc/openFuyao/bkeagent/config")
  │
  ▼
Node: /etc/openFuyao/bkeagent/config (admin kubeconfig)
```
这与当前代码 `GetLocalKubeConfig()` + `echo -e > /etc/openFuyao/bkeagent/config` 完全等价。

#### 4b. RBAC 资源 → 声明式 YAML

当前 `CreateBKEAgentRBACWithLocalKubeConfig()` 硬编码创建 3 个 ClusterRole + RoleBinding/ClusterRoleBinding。在新架构中应改为声明式：

**方案一：bkeagent ComponentVersion 的 `resources[]` 内联声明**
```yaml
spec:
  name: bkeagent
  type: binary
  version: v2.6.0
  resources:
    - kind: ClusterRole
      apiVersion: rbac.authorization.k8s.io/v1
      name: bkeagent-readwrite
      manifest: |
        rules:
        - apiGroups: [""]
          resources: [secrets]
          verbs: [get]
        - apiGroups: [""]
          resources: [configmaps]
          verbs: [get, list, watch, create, update]
    - kind: ClusterRole
      apiVersion: rbac.authorization.k8s.io/v1
      name: bkeagent-configmap-only
      manifest: |
        rules:
        - apiGroups: [""]
          resources: [configmaps]
          verbs: [get, list, watch]
    - kind: ClusterRole
      apiVersion: rbac.authorization.k8s.io/v1
      name: bkeagent-cluster-access
      manifest: |
        rules:
        - apiGroups: ["bke.bocloud.com"]
          resources: [bkeclusters, bkenodes, containerdconfigs, kubeletconfigs]
          verbs: [get, list, watch]
        - apiGroups: ["bkeagent.bocloud.com"]
          resources: [commands, commands/finalizers]
          verbs: [get, list, watch, update, patch, delete]
        - apiGroups: ["bkeagent.bocloud.com"]
          resources: [commands/status]
          verbs: [get, patch, update]
    - kind: ClusterRoleBinding
      apiVersion: rbac.authorization.k8s.io/v1
      name: bkeagent-cluster-access
      manifest: |
        roleRef:
          apiGroup: rbac.authorization.k8s.io
          kind: ClusterRole
          name: bkeagent-cluster-access
        subjects:
        - kind: User
          name: bkeagent-cert-user
          apiGroup: rbac.authorization.k8s.io
    # ... RoleBindings for cluster-system, user-system, bkeCluster.Namespace
```

**方案二（推荐）：独立 bkeagent-rbac YAML 组件**
```yaml
# bke-manifests/bkeagent-rbac/v1.0.0/component.yaml
spec:
  name: bkeagent-rbac
  type: yaml
  version: v1.0.0
  yaml:
    manifests:
      - url: "https://release-repo/bkeagent-rbac/v1.0.0/rbac.yaml"
  # 无健康检查
```
DAG 依赖：`bkeagent-rbac` → `bkeagent`（RBAC 先于 bkeagent 安装）

#### 4c. 与当前代码的等价性对比

| 维度 | 当前代码 | KEP-6 新设计 |
|------|---------|-------------|
| kubeconfig 来源 | `GetLocalKubeConfig()` 读 Secret | `secretRef` 读 Secret |
| kubeconfig 推送 | `echo -e %q > /etc/openFuyao/bkeagent/config` | `ssh.Upload()` 到同一路径 |
| RBAC 创建 | `CreateBKEAgentRBACWithLocalKubeConfig()` 硬编码 | YAML 组件声明式 Apply |
| 条件分支 | `hasClusterAPI` 三路分发 | 无分支（简化为一条路径） |
| Secret 依赖 | 隐式（假设 Secret 存在） | 显式（`secretRef` 声明依赖） |

### 5. 需要确认的设计决策

1. **RBAC 放置位置**：`resources[]` 内联 vs 独立 YAML 组件？推荐独立组件，职责更清晰
2. **`secretRef.name` 是否需要模板变量**：当前硬编码 `localkubeconfig`，如果不同集群环境使用不同 Secret 名，需要支持 `name: "{{.Variables.kubeconfigSecretName}}"`
3. **安全性**：admin kubeconfig 推送到每个节点，任何 bkeagent 被攻破 = 完整集群访问。是否需要修复原来的 least-privilege 设计（在管理集群创建低权限 kubeconfig Secret）？

# kubeconfig 修改计划

## 修改计划

### 修改范围

需要修改 3 处，均在 12.4 节（bkeagent 重构详细设计）内。`kubeconfigTemplate` 功能本身保留在 CRD 和 ConfigRenderer 中（供其他组件使用），仅 bkeagent 改用 `secretRef`。

### 修改 1：Section 12.4.2 — ComponentVersion YAML 定义

**位置**：lines 6518-6528

**当前内容**：
```yaml
      # Kubeconfig（管理集群访问凭证）
      - name: kubeconfig
        path: "/etc/openFuyao/bkeagent/config"
        mode: "0600"
        owner: "root:root"
        kubeconfigTemplate:
          clusterName: "management-cluster"
          apiServer: "{{apiServer}}"
          caCertPath: "/etc/openFuyao/certs/global-ca.crt"
          clientCertPath: "/etc/openFuyao/certs/bkeagent-client.crt"
          clientKeyPath: "/etc/openFuyao/certs/bkeagent-client.key"
```

**修改为**：
```yaml
      # Kubeconfig（管理集群 admin kubeconfig — 从管理集群 Secret 直接获取）
      # 等价于当前 loadLocalKubeConfig() → GetLocalKubeConfig()
      # Secret kube-system/localkubeconfig 由 bke init 预置，内容为嵌入式证书数据
      - name: kubeconfig
        path: "/etc/openFuyao/bkeagent/config"
        mode: "0600"
        owner: "root:root"
        secretRef:
          name: localkubeconfig
          namespace: kube-system
          key: config
```
**修改理由**：
- 当前代码 `GetLocalKubeConfig()` 直接读取管理集群 Secret `kube-system/localkubeconfig`，内容为嵌入式证书数据（`certificate-authority-data`/`client-certificate-data`/`client-key-data`）
- `kubeconfigTemplate` 模式生成的是文件路径引用式 kubeconfig（`certificate-authority: /path/to/ca.crt`），与实际行为不等价
- `secretRef` 模式直接读取 Secret 内容，与当前代码完全等价

### 修改 2：Section 12.4.3 — 字段映射表

**位置**：line 6734

**当前内容**：
```
| kubeconfig 动态生成（硬编码路径） | `binary.configTemplates[4].kubeconfigTemplate` | kubeconfig 模板声明式化 |
```

**修改为**：
```
| kubeconfig 从 Secret 获取（`GetLocalKubeConfig()`） | `binary.configTemplates[4].secretRef`（引用 `kube-system/localkubeconfig`） | kubeconfig 获取声明式化 |
```

### 修改 3：Section 12.4.4 — 行为等价性验证点

**位置**：line 6749

**当前内容**：
```
| kubeconfig 内容 | Go 代码动态生成 | `kubeconfigTemplate` 渲染 | `diff` 对比两份输出 |
```

**修改为**：
```
| kubeconfig 内容 | `GetLocalKubeConfig()` 读取 Secret | `secretRef` 读取同一 Secret（`kube-system/localkubeconfig`） | `diff` 对比两份输出（内容一致） |
```

### 不需要修改的部分

| 位置 | 原因 |
|------|------|
| CRD 类型定义 `KubeconfigTemplateSpec`（line 554） | 功能保留，供其他组件使用 |
| CRD YAML schema `kubeconfigTemplate`（line 1067） | 功能保留 |
| ConfigRenderer Mode 3 设计（line 2215） | 功能保留 |
| containerd ComponentVersion（无 kubeconfig） | 不涉及 |
| Section 12.5 迁移验证清单（line 6773） | "bkeagent kubeconfig" 验证项仍然有效（diff 对比），无需修改 |

### 数据流等价性

```
当前代码:
  GetLocalKubeConfig()
  → client.Get(kube-system/localkubeconfig)
  → secret.Data["config"]  (嵌入式证书数据)
  → echo -e %q > /etc/openFuyao/bkeagent/config

新设计:
  ConfigRenderer.renderSecretTemplate()
  → client.Get(kube-system/localkubeconfig)
  → secret.Data["config"]  (嵌入式证书数据)
  → ssh.Upload() → /etc/openFuyao/bkeagent/config

结果完全一致。
```

# func (e *EnsureBKEAgent) getNeedPushNodes() 在bkeagent的ComponentVersion中表达出来？

## 分析：`getNeedPushNodes()` 能否在 ComponentVersion 中表达

### 1. `getNeedPushNodes()` 完整逻辑

```
getNeedPushNodes()
│
├── 1. 获取 BKENodes (从管理集群 API)
│
├── 2. 过滤节点 (GetNeedPushAgentNodesWithBKENodes)
│   ├── 排除 NodeFailedFlag (bit 7)     — 已失败的节点
│   ├── 排除 NodeDeletingFlag (bit 6)   — 正在删除的节点
│   ├── 排除 NeedSkip == true           — 需要跳过的节点
│   ├── 排除 NodeAgentPushedFlag (bit 0) — agent 已推送的节点 (幂等)
│   └── 排除 Appointment 节点           — 预约添加的节点
│
├── 3. 设置节点状态 → NodeInitializing
│
└── 4. SyncStatusUntilComplete (等待状态同步)
```
核心是**节点级过滤**——决定哪些节点需要推送 agent。

### 2. 这不是 ComponentVersion 的职责

`getNeedPushNodes()` 的逻辑分为两类关注点：

| 关注点 | 逻辑 | 属于 ComponentVersion？ |
|--------|------|----------------------|
| **幂等性** | `!NodeAgentPushedFlag` — 已推送的节点跳过 | **否** — 这是运行时状态，不是组件配置 |
| **错误处理** | 排除 Failed/Deleting/Skipped 节点 | **否** — 这是节点生命周期状态 |
| **调度约束** | 排除 Appointment 节点 | **否** — 这是集群级调度策略 |

ComponentVersion 描述的是**"怎么安装"**（制品、脚本、配置），不是**"在哪些节点上安装"**（运行时节点状态过滤）。

### 3. KEP-6 新架构中的对应关系

在 KEP-6 的 `BinaryComponentExecutor` 设计中，`getNeedPushNodes()` 的职责被分散到三个层次：

```
当前代码                              KEP-6 新架构
─────────────                        ─────────────────────
NeedExecute()                        VersionContext.NeedsUpgrade("bkeagent")
  → HasNodesNeedingPhase()             → 组件级: 是否需要执行？
  → 组件级判断

getNeedPushNodes()                   NodeProvider.GetNodes()
  → 过滤 Failed/Deleting/Skipped       → 获取节点列表
  → 过滤 NodeAgentPushedFlag           → ??? (需要补充设计)
  → 节点级判断

pushAgent()                          BinaryInstaller.Install()
  → SSH 推送                           → 单节点安装
  → 设置 NodeAgentPushedFlag           → StatusWriter.MarkSuccess()
```

### 4. 关键缺口：节点级幂等性

当前 `NodeAgentPushedFlag` 提供了**节点级幂等性**——已推送的节点不会重复推送。KEP-6 设计中：

| 层级 | 当前机制 | KEP-6 机制 | 是否覆盖 |
|------|---------|-----------|---------|
| **组件级** | `HasNodesNeedingPhase(flag)` | `VersionContext.NeedsUpgrade(name)` | ✅ 等价 |
| **节点级幂等** | `!NodeAgentPushedFlag` | `BKECluster.Status.ComponentStatuses` (组件级) | ❌ **不等价** — 缺少 per-node 粒度 |
| **节点状态过滤** | 排除 Failed/Deleting/Skipped | `NodeProvider` 未设计此过滤 | ❌ **缺失** |

**问题**：KEP-6 的 `VersionContext` 是组件级的（`Current["bkeagent"] = "v2.6.0"`），无法表达"node1 已有 v2.6.0，node2 还是 v2.5.0"这种 per-node 状态。

### 5. 解决方案

节点级过滤属于 **Executor 层** 或 **NodeProvider 层** 的职责，不应放入 ComponentVersion。具体方案：

#### 方案 A：NodeProvider 负责过滤（推荐）

```go
// NodeProvider 扩展：增加节点状态过滤
type NodeProvider interface {
    GetNodes(ctx context.Context, cluster *bkev1beta1.BKECluster) ([]Node, error)
    GetNodesByRole(ctx context.Context, cluster *bkev1beta1.BKECluster, role string) ([]Node, error)
    
    // 新增：获取需要执行操作的节点 (排除 Failed/Deleting/Skipped/已完成)
    GetNeedExecuteNodes(ctx context.Context, cluster *bkev1beta1.BKECluster, componentName string) ([]Node, error)
}
```
`GetNeedExecuteNodes` 内部：
- 排除 `NodeFailedFlag` / `NodeDeletingFlag` / `NeedSkip` 节点（等价于当前 `filterNodes` 的硬排除）
- 排除 per-node 已完成节点（等价于当前 `!NodeAgentPushedFlag`）
- 排除 Appointment 节点

**判断 per-node 是否已完成**的方式：
- 复用现有 `BKENode.Status.StateCode` 位标记（向后兼容）
- 或：新增 `BKECluster.Status.NodeComponentStatuses[nodeIP][componentName]` 字段（更通用）

#### 方案 B：BinaryComponentExecutor 内部过滤

```go
func (e *BinaryComponentExecutor) ExecuteComponent(...) error {
    // 获取所有节点
    allNodes, _ := execCtx.NodeProvider.GetNodes(ctx, execCtx.Cluster)
    
    // Executor 内部过滤 (等价于 getNeedPushNodes)
    var targetNodes []Node
    for _, node := range allNodes {
        if e.shouldSkipNode(node, execCtx, cv) {
            continue
        }
        targetNodes = append(targetNodes, node)
    }
    
    // 按策略执行
    return e.executeRolling(ctx, targetNodes, cv, strategy, execCtx)
}

func (e *BinaryComponentExecutor) shouldSkipNode(node Node, execCtx *ExecutionContext, cv *ComponentVersion) bool {
    // 1. 排除 Failed/Deleting/Skipped (从 BKENode.Status 读取)
    // 2. 排除已完成 (从 per-node component status 读取)
    // 3. 排除 Appointment 节点
}
```

#### 方案 C：依赖 installScript 幂等性（最简单）

不做节点级过滤，对所有节点执行 `BinaryInstaller.Install()`。依赖 `installScript` 的幂等性：
- 已安装的节点：installScript 重新执行，但结果不变（`systemctl restart` 后版本相同）
- `VersionContext.NeedsUpgrade()` 在组件级判断，如果所有节点都已是目标版本，整个组件跳过

**优点**：实现最简单，无需 per-node 状态跟踪
**缺点**：会对已安装节点执行不必要的 SSH 操作（停止服务→重新安装→启动），有短暂服务中断

### 6. 建议

| 方案 | 适用场景 | 推荐度 |
|------|---------|--------|
| **A: NodeProvider 过滤** | 需要精确 per-node 控制，保留现有 BKENode 状态模型 | 推荐 |
| **B: Executor 内部过滤** | 过滤逻辑与组件类型相关（如 Binary 需要 per-node，Helm 不需要） | 可选 |
| **C: installScript 幂等** | 简单场景，可接受对已安装节点的冗余操作 | 不推荐（有服务中断风险） |

**核心结论**：`getNeedPushNodes()` 的逻辑**不应放入 ComponentVersion**，而应由 `NodeProvider` 或 `BinaryComponentExecutor` 承担。KEP-6 文档需要补充 per-node 状态跟踪的设计。

# 检查代码中的所有二进制组件，它们的节点过滤是不是都是一样的逻辑？还是各个组件都不相同。getNeedPushNodes() 直接内置到BinaryInstaller是否存在扩展性问题？如何更新BKENode的状态？
## 分析结果

### 1. 各二进制组件的节点过滤逻辑对比

代码库中只有 **2 个 Phase** 做真正的 SSH 二进制推送：

| Phase | 文件 | 推送方式 | 节点过滤逻辑 |
|-------|------|---------|-------------|
| **EnsureBKEAgent** | `ensure_bke_agent.go` | 直接 SSH (`bkessh.MultiCli`) | `GetNeedPushAgentNodesWithBKENodes` — 按 `!NodeAgentPushedFlag` 过滤 |
| **EnsureAgentUpgrade** | `ensure_agent_upgrade.go` | 直接 SSH (`agentssh.SSHUpgrade`) | **无过滤** — 升级所有节点 |

其他操作节点的 Phase（EnsureNodesEnv、EnsureContainerdUpgrade、EnsureMasterUpgrade 等）都通过 **BKEAgent 中转**（创建 Command CRD，Agent 拾取执行），不是直接 SSH 推送。

**各 Phase 的节点过滤逻辑各不相同**：

| Phase | 过滤函数 | 过滤条件 | 排除条件 |
|-------|---------|---------|---------|
| EnsureBKEAgent | `GetNeedPushAgentNodesWithBKENodes` | `!NodeAgentPushedFlag` | Failed/Deleting/Skipped/Appointment |
| EnsureAgentUpgrade | **无** | 全部节点 | 仅检查 `nodes.Length() == 0` |
| EnsureNodesEnv | `GetNeedInitEnvNodesWithBKENodes` | `!NodeEnvFlag` + `NodeAgentReadyFlag` | Failed/Deleting/Skipped/Appointment |
| EnsureContainerdUpgrade | `GetNeedUpgradeNodesWithBKENodes` | `OpenFuyaoVersion` 版本比较 | Failed |
| EnsureMasterUpgrade | `GetNeedUpgradeK8sNodes` + `.Master()` | `KubernetesVersion` 版本比较 + `NodeAgentReadyFlag` | Failed |
| EnsureWorkerUpgrade | `GetNeedUpgradeK8sNodes` + `.Worker()` | `KubernetesVersion` 版本比较 + `NodeAgentReadyFlag` | Failed |
| EnsureEtcdUpgrade | `filterUpgradeableNodes` + `.Etcd()` | `EtcdVersion` 比较 + `NodeAgentReadyFlag` | Failed |
| EnsureNodesPostProcess | `GetNeedPostProcessNodesWithBKENodes` | `NodeBootFlag && !NodePostProcessFlag` | Failed/Deleting/Skipped/Appointment |

**结论**：每个 Phase 的过滤逻辑都不一样——有的按 flag 位标记，有的按版本比较，有的按角色过滤。唯一的共性是 `filterNodes()` 中对 Failed/Deleting/Skipped 的硬排除。

### 2. `getNeedPushNodes()` 内置到 BinaryInstaller 的扩展性问题

**存在严重扩展性问题**，原因如下：

#### 问题 1：过滤逻辑因组件而异

当前 `getNeedPushNodes()` 硬编码了 `!NodeAgentPushedFlag` 过滤。但 KEP-6 新架构中 BinaryInstaller 要服务所有 binary 类型组件（containerd、bkeagent 等），每个组件的"已完成"判断不同：

| 组件 | 当前幂等标记 | 判断方式 |
|------|------------|---------|
| bkeagent (安装) | `NodeAgentPushedFlag` | 位标记 |
| bkeagent (升级) | **无过滤** | 全部节点都执行 |
| containerd | `OpenFuyaoVersion` 比较 | 版本比较 |

如果 BinaryInstaller 内置了 `getNeedPushNodes()`，就无法适应不同组件的过滤需求。

#### 问题 2：BKENode 状态模型与 KEP-6 不兼容

当前状态更新依赖 `BKENode` CRD 的 `StateCode` 位标记，这是一个 **per-node per-phase** 的模型。KEP-6 的 `BinaryComponentExecutor` 是通用执行器，不应绑定到特定的位标记模型。

#### 问题 3：状态更新时序复杂

当前 `getNeedPushNodes()` 不仅过滤节点，还做了三件事：
1. 设置节点状态为 `NodeInitializing`（before push）
2. 推送成功后设置 `NodeAgentPushedFlag`（after push）
3. 调用 `SyncStatusUntilComplete` 持久化状态

这些是 **编排层** 的职责，不应下沉到 **安装层**（BinaryInstaller）。

#### 建议的架构分层

```
当前代码                              KEP-6 新架构
─────────────                        ─────────────────────
getNeedPushNodes()                   BinaryComponentExecutor.ExecuteComponent()
  ├─ 过滤节点                          ├─ 获取节点 (NodeProvider)
  ├─ 设置 NodeInitializing             ├─ 过滤节点 (NodeFilter 接口)
  └─ SyncStatus                        ├─ 设置节点状态 (StatusUpdater)
                                       └─ 按策略执行
pushAgent()                          BinaryInstaller.Install()
  ├─ SSH 推送                          ├─ 下载制品
  ├─ 设置 NodeAgentPushedFlag          ├─ 渲染脚本/配置
  └─ 更新失败状态                       ├─ SSH 执行
                                       └─ 健康检查
```

**关键设计**：节点过滤和状态更新应由 **Executor 层** 负责，通过接口注入，BinaryInstaller 只负责单节点安装。
```go
// NodeFilter 节点过滤接口 — 各组件可自定义过滤逻辑
type NodeFilter interface {
    FilterNodes(ctx context.Context, nodes []Node, cv *ComponentVersion) ([]Node, error)
}

// NodeStatusUpdater 节点状态更新接口 — 解耦 BKENode CRD 细节
type NodeStatusUpdater interface {
    MarkPending(ctx context.Context, nodeIP string, component string) error
    MarkSuccess(ctx context.Context, nodeIP string, component string, version string) error
    MarkFailed(ctx context.Context, nodeIP string, component string, err error) error
}
```
这样：
- **bkeagent 安装**：`NodeFilter` 实现检查 `!NodeAgentPushedFlag`
- **bkeagent 升级**：`NodeFilter` 实现返回全部节点（不过滤）
- **containerd 安装**：`NodeFilter` 实现检查 `OpenFuyaoVersion`
- BinaryInstaller 不感知这些差异，只接收过滤后的节点列表

### 3. BKENode 状态更新机制分析

当前状态更新有 **三层机制**，非常复杂：

#### 第一层：内存操作（BKENodes wrapper）

```go
// api/capbke/v1beta1/bkenode_types.go
nodes.SetNodeStateWithMessage(ip, state, msg)   // 设置 State + Message + NeedRecord 脏标记
nodes.MarkNodeStateFlag(ip, flag)                // 设置 StateCode 位
nodes.GetModifiedNodes()                          // 获取有脏标记的节点
nodes.ClearRecordFlags()                          // 清除脏标记
```
通过 `NodeStateNeedRecord`（bit 8）作为脏标记，追踪哪些节点需要持久化。

#### 第二层：API 持久化（NodeFetcher）

```go
// utils/capbke/nodeutil/fetcher.go
nf.SetNodeStateWithMessage(ctx, ns, cluster, ip, state, msg)  // 单字段更新
nf.MarkNodeStateFlag(ctx, ns, cluster, ip, flag)               // 位标记设置
nf.UpdateNodeStatusByIP(ctx, ns, cluster, ip, func(status))   // 多字段原子更新（带冲突重试）
```
所有方法都使用 `retry.RetryOnConflict` 处理乐观并发。

#### 第三层：集群级同步（SyncStatusUntilComplete）

```go
// pkg/mergecluster/bkecluster.go
mergecluster.SyncStatusUntilComplete(client, bkeCluster)
```
带 2 分钟超时的重试循环，将内存中的 BKECluster.Status 变更持久化到 API Server。成功后调用 `UpdateModifiedBKENodes` 清除 BKENode 的 `NeedRecord` 脏标记。

#### 状态更新时序（以 EnsureBKEAgent 为例）

```
getNeedPushNodes()
  │
  ├─ SetNodeStateWithMessage(ip, NodeInitializing, "Pushing bkeagent")  ← 第一层
  ├─ SyncStatusUntilComplete()                                           ← 第三层
  │
pushAgent()
  │
  ├─ 成功节点: MarkNodeStateFlag(NodeAgentPushedFlag)                    ← 第二层
  │
  ├─ 失败节点: UpdateNodeStatusByIP(func(status) {                       ← 第二层
  │     State = NodeInitFailed
  │     Message = "Failed push..."
  │     NeedSkip = true  (worker 节点)
  │   })
  │
  ├─ pingAgent()
  │   ├─ 成功: UpdateNodeStatusByIP(func(status) {                       ← 第二层
  │   │     StateCode |= NodeAgentPushedFlag | NodeAgentReadyFlag
  │   │     Message = "BKEAgent is ready"
  │   │   })
  │   └─ 失败: UpdateNodeStatusByIP(func(status) {                       ← 第二层
  │         State = NodeInitFailed
  │         StateCode &= ^NodeAgentPushedFlag  (清除推送标记)
  │         NeedSkip = true
  │       })
```

#### KEP-6 新架构中的状态更新设计

KEP-6 需要解决的核心问题：**当前 StateCode 位标记模型是 per-phase 的，而新架构需要 per-component 的状态跟踪**。

| 维度 | 当前设计 | KEP-6 设计 |
|------|---------|-----------|
| 状态存储 | `BKENode.Status.StateCode` (位标记) | `BKECluster.Status.ComponentStatuses[name]` (组件级) + BKENode 位标记 (兼容) |
| 幂等判断 | `StateCode & NodeAgentPushedFlag` | `ComponentStatuses["bkeagent"].Version == target` |
| per-node 粒度 | ✅ 有（每个 BKENode 独立 StateCode） | ❓ 需要设计 per-node per-component 状态 |
| 持久化机制 | 三层（内存→API→集群同步） | 需简化 |

**per-node per-component 状态** 是当前的设计缺口。当前 `ComponentStatuses` 是组件级的（不区分节点），无法表达"node1 已安装 containerd v1.7.18，node2 还是 v1.7.15"。

**建议方案**：在 `BKECluster.Status` 中新增 per-node per-component 状态：
```go
type BKEClusterStatus struct {
    // ... 现有字段 ...
    
    // per-node per-component 安装状态
    // key: nodeIP, value: 该节点上各组件的安装状态
    NodeComponentStatuses map[string]map[string]NodeComponentStatus `json:"nodeComponentStatuses,omitempty"`
}

type NodeComponentStatus struct {
    Version string `json:"version"`           // 已安装版本
    Phase   string `json:"phase"`             // Installed / Failed / Installing
    Message string `json:"message,omitempty"` // 错误信息
}
```
这样 `BinaryComponentExecutor` 可以通过 `NodeComponentStatuses[nodeIP]["containerd"].Version` 判断每个节点的幂等性，无需依赖 BKENode 的 StateCode 位标记。

### 总结

| 问题 | 结论 |
|------|------|
| 各组件过滤逻辑是否相同？ | **不同** — 每个 Phase 有独立的过滤函数和判断条件 |
| `getNeedPushNodes()` 内置到 BinaryInstaller 是否有扩展性问题？ | **是** — 过滤逻辑因组件而异，状态更新是编排层职责 |
| 如何更新 BKENode 状态？ | 当前三层机制复杂，KEP-6 需要新增 per-node per-component 状态模型 |

# `/etc/openFuyao/bkeagent/node` 文件详解(来自BKENode)

## `/etc/openFuyao/bkeagent/node` 文件详解

### 文件路径
- **完整路径**: `/etc/openFuyao/bkeagent/node`
- **Workspace 常量**: `/etc/openFuyao/bkeagent` (定义于 `utils/const.go:33`)

### 文件内容
文件只包含一个值：**节点的 hostname**（例如：`node-01`）

### 核心作用

#### 1. **节点身份标识**
bkeagent 使用此文件中的 hostname 作为自己在 Kubernetes 集群中的**唯一标识**。这个标识用于：
- **Command 匹配**: bkeagent 通过 `NodeName` 判断哪些 Command 资源应该由自己执行
- **Kubelet 配置**: 作为 kubelet 的 `--hostname-override` 参数
- **节点状态上报**: 在 BKECluster.Status 中标识节点状态

#### 2. **Command 资源匹配机制**

在 `controllers/bkeagent/command_controller.go` 中：
```go
// CommandReconciler 使用 NodeName 匹配 Command
type CommandReconciler struct {
    NodeName  string  // 从 /etc/openFuyao/bkeagent/node 读取
    NodeIP    string
    // ...
}

// 判断是否应该处理某个 Command
func (r *CommandReconciler) shouldReconcileCommand(o *agentv1beta1.Command, eventType string) bool {
    // 方式 1: 直接匹配 NodeName
    if o.Spec.NodeName == r.NodeName {
        return true
    }
    // 方式 2: 通过 NodeSelector 匹配
    return r.nodeMatchNodeSelector(o.Spec.NodeSelector)
}
```
**工作流程**：
1. 控制器创建 Command 资源，指定 `spec.nodeName: "node-01"`
2. 所有节点的 bkeagent 都会 watch 到该 Command
3. 只有 `NodeName == "node-01"` 的 bkeagent 会处理该 Command
4. 其他节点的 bkeagent 会忽略该 Command

#### 3. **Kubelet 节点标识**

在 `pkg/job/builtin/kubeadm/kubelet/command.go:270`：
```go
func (k *RunKubeletCommand) getKubeletArgs() []string {
    hostNameOverride := fmt.Sprintf("--hostname-override=%s", utils.HostName())
    // ...
}
```
kubelet 使用此 hostname 作为节点名称注册到 Kubernetes API Server。

### 读取逻辑

**文件**: `utils/utils.go:99-131`
```go
func HostName() string {
    // 1. 获取系统 hostname
    hostName, err := os.Hostname()
    
    // 2. 检查 node 文件是否存在
    nodeFilePath := filepath.Join(Workspace, "node")
    if !Exists(nodeFilePath) {
        // 不存在则创建并写入系统 hostname
        os.WriteFile(nodeFilePath, []byte(hostName), RwRR)
        return hostName
    }
    
    // 3. 读取 node 文件
    b, err := os.ReadFile(nodeFilePath)
    bkeNodeName := strings.TrimSpace(strings.Replace(string(b), "\n", "", -1))
    
    // 4. 如果文件为空，写入系统 hostname
    if bkeNodeName == "" {
        os.WriteFile(nodeFilePath, []byte(hostName), RwRR)
        return hostName
    }
    
    // 5. 返回 node 文件中的 hostname
    return bkeNodeName
}
```
**优先级**：
1. 如果 `/etc/openFuyao/bkeagent/node` 存在且非空 → 使用文件中的 hostname
2. 如果文件不存在或为空 → 使用系统 hostname 并写入文件

### 写入时机

#### 1. **首次安装** (EnsureBKEAgent Phase)
**文件**: `pkg/phaseframe/phaseutil/ssh.go:68-80`

```go
func HostCustomCmdFunc(host *bkessh.Host) bkessh.Command {
    c := []string{
        fmt.Sprintf("echo %s > /etc/openFuyao/bkeagent/node", host.Extra["hostname"]),
    }
    // ...
}
```

#### 2. **升级** (EnsureAgentUpgrade Phase)
**文件**: `pkg/phaseframe/phaseutil/agentssh/push_upgrade.go:123-135`

```go
func upgradeHostFileFunc(stagingDir string) func(host *bkessh.Host) bkessh.Command {
    return func(host *bkessh.Host) bkessh.Command {
        return bkessh.Command{
            Cmds: bkessh.Commands{
                fmt.Sprintf("echo %s > /etc/openFuyao/bkeagent/node", host.Extra["hostname"]),
            },
        }
    }
}
```

#### 3. **bkeagent 自动创建**
如果文件不存在，bkeagent 启动时会自动创建并写入系统 hostname。

### 为什么需要这个文件？

#### 问题场景
在某些云环境中，节点的**系统 hostname** 可能与**期望的节点名称**不一致：
- 云厂商可能使用内部命名规则
- DHCP 分配的 hostname 可能不符合规范
- 节点重启后 hostname 可能变化

#### 解决方案
通过 `/etc/openFuyao/bkeagent/node` 文件：
1. **持久化节点标识**: 即使系统 hostname 变化，bkeagent 仍使用文件中的固定标识
2. **控制器指定**: 控制器可以在安装时指定正确的 hostname，而不是依赖系统默认值
3. **一致性保证**: 确保 bkeagent、kubelet、Kubernetes Node 对象使用相同的节点名称

### 使用场景汇总

| 使用位置 | 用途 |
|---------|------|
| `cmd/bkeagent/main.go:120` | 初始化 CommandReconciler 的 NodeName |
| `controllers/bkeagent/command_controller.go:380` | 匹配 Command 资源 |
| `pkg/job/builtin/kubeadm/kubelet/command.go:270` | kubelet `--hostname-override` 参数 |
| `pkg/job/builtin/kubeadm/env/init.go:510` | 节点环境初始化 |
| `pkg/job/builtin/kubeadm/env/check.go:326` | 节点状态检查 |
| `pkg/job/builtin/ping/main.go:37` | Ping 响应中返回节点标识 |
| `utils/bkeagent/pkiutil/bkecertlist.go:224` | 证书生成时的节点名称 |

### 总结

`/etc/openFuyao/bkeagent/node` 是 bkeagent 的**核心身份文件**，它：
- 存储节点的持久化标识（hostname）
- 用于 Command 资源的精确匹配
- 确保 kubelet 和 Kubernetes 使用一致的节点名称
- 解决云环境中 hostname 不一致的问题
- 提供节点身份的持久化保证（即使系统 hostname 变化）
- 
