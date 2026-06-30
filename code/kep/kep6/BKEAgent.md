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
