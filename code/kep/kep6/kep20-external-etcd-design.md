# 支持外接 etcd 的设计提案

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-20 |
| **标题** | 支持外接 etcd（External Etcd）的设计提案 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-09-14 |
| **依赖** | KEP-5 (ClusterVersion/ReleaseImage/UpgradePath)、KEP-6 (声明式组件安装/升级/回滚框架)、KEP-12 (备份与恢复) |

---

## 目录

1. [概述](#1-概述)
2. [动机与目标](#2-动机与目标)
3. [现状分析](#3-现状分析)
4. [总体设计方案](#4-总体设计方案)
5. [API 设计](#5-api-设计)
6. [控制器/校验层设计](#6-控制器校验层设计)
7. [Agent 渲染与安装设计](#7-agent-渲染与安装设计)
8. [证书设计](#8-证书设计)
9. [升级与备份设计](#9-升级与备份设计)
10. [声明式组件框架的集成](#10-声明式组件框架的集成)
11. [兼容性与边界](#11-兼容性与边界)
12. [工作量评估与里程碑](#12-工作量评估与里程碑)
13. [风险与缓解](#13-风险与缓解)
14. [毕业标准](#14-毕业标准)
15. [替代方案](#15-替代方案)

---

## 1. 概述

### 1.1 背景

当前 BKE（openFuyao）在安装/升级集群时，etcd 以**静态 Pod**（Static Pod）形态内嵌在每个带 `etcd` 角色的控制平面节点上，由 Agent 渲染 `etcd.yaml.tmpl` 并借助成员管理逻辑完成集群 bootstrap / 扩容。etcd 与 `kube-apiserver` 的证书、数据目录、成员关系均由 BKE 全生命周期托管：

- **安装**：etcd 由 `EnsureMasterInit` 中 kubeadm 初始化路径隐式创建（见 `code/kep/kep7/kep10-install-dag-with-phaseflow-fallback.md` §5.2 与 `pkg/job/builtin/kubeadm/kubeadm.go`）。
- **渲染**：`utils/bkeagent/mfutil/render.go` 的 `renderEtcd` / `handleEtcdMembership` 生成 etcd 静态 Pod 并处理成员关系。
- **证书**：`utils/bkeagent/pkiutil/bkecertlist.go` 为 etcd 生成 CA / server / peer / healthcheck / apiserver-etcd-client 证书。
- **端点**：`kube-apiserver.yaml.tmpl` 通过 `etcdServers` 模板函数从**带 etcd 角色节点**推导 `--etcd-servers`，证书路径硬编码为本地 `/etc/kubernetes/pki/etcd/...`。

用户场景中，存在希望复用一套**外部托管 etcd**（如独立的 etcd 集群、云厂商托管 etcd、或存量生产 etcd）而不由 BKE 部署/运维 etcd 的需求。本 KEP 提出在保留内嵌 etcd 能力的基础上，新增**外接 etcd**声明式配置能力。

### 1.2 关键结论

- 复用 Kubernetes Cluster API / kubeadm 的 `EtcdExternal`（`endpoints` / `caFile` / `certFile` / `keyFile`）语义，在 `BKECluster.Spec.ClusterConfig.Cluster.ControlPlane.Etcd.External` 新增外接配置。
- 当配置外接 etcd 时：不渲染 etcd 静态 Pod、不生成 etcd 相关证书、不执行成员管理、不为 apiserver 推导节点端点，而改用用户提供的 `endpoints` 与证书文件。
- 复用现有 `GetComponentListWithOutEtcd()`、`GetCertsWithoutEtcd()`、`MinExternalEtcdVersion` 等已有雏形能力，补齐端到端接线。
- 外接 etcd 的备份/升级职责**转移给用户**，BKE 仅校验连通性与版本下限，不再执行 `etcdctl` 快照与成员变更。

---

## 2. 动机与目标

### 2.1 目标（Goals）

| 编号 | 目标 |
|------|------|
| G1 | 用户可通过 `Etcd.External` 声明一套外接 etcd，BKE 安装出的控制平面直接对接该 etcd，不部署内嵌 etcd |
| G2 | 外接 etcd 的证书/端点配置声明式可校验，非法配置在 Reconcile 前被 webhook/validation 拦截 |
| G3 | 与现有内嵌 etcd 能力互斥清晰、向后兼容：未配置 `External` 时行为不变 |
| G4 | 升级/备份/健康检查流程对外接 etcd 正确处理：跳过本应由 BKE 托管 etcd 才执行的动作 |
| G5 | 与声明式组件框架（KEP-5/6）版本来源机制兼容，外接 etcd 不参与 etcd 组件版本计算 |

### 2.2 非目标（Non-Goals）

| 编号 | 非目标 |
|------|--------|
| N1 | 不负责对外接 etcd 本身的部署、扩容、TLS 证书签发（这些由用户/外部系统负责） |
| N2 | 不实现外接 etcd 的自动备份/恢复编排（仅支持用户侧连接到外部 etcd 手动快照） |
| N3 | 不支持在已运行的内嵌 etcd 集群上通过声明式切换到外接 etcd（迁移属于后续单独 KEP） |
| N4 | 不改变现有 bocloud 集群“接管”路径中 `prepareExternalEtcdConfig` 的 fake 证书语义 |

---

## 3. 现状分析

### 3.1 内嵌 etcd 的端到端链路

```
用户声明 BKENode(Role 含 "etcd") / BKEConfig.Cluster.ControlPlane.Etcd
        │
        ▼
[Controller] validation.ValidateNodesFields/ValidateNodesRole
        │   (要求至少存在一个 etcd 节点，NoEtcdNodeError)
        ▼
[Agent] mfutil.GenerateManifestYaml
        │   manifest.go:67  flag = 节点是否带 EtcdNodeRole
        ▼
[Agent] render.go:
        │   renderEtcd → handleEtcdMembership → renderEtcdYaml (etcd.yaml.tmpl)
        │   apiServerFuncMap.etcdServers → 从 bkeNodes.Etcd() 推导端点
        │   kube-apiserver.yaml.tmpl 硬编码 etcd CA/cert/key 路径
        ▼
[Agent] pkiutil 生成 etcd 全量证书 (GetEtcdCerts)
        ▼
[升级] kubeadm.upgradeEtcd / EnsureEtcdUpgrade / needUpgradeEtcd
        ▼
[备份] kubeadm.prepareUpgrade → backupEtcd (etcdctl snapshot)
```

### 3.2 已具备的“外接 etd”雏形

代码库中已存在若干面向外接 etcd 的构件，但**未端到端接通**：

| 构件 | 位置 | 现状 |
|------|------|------|
| `GetComponentListWithOutEtcd()` | `utils/bkeagent/mfutil/componentlist.go:77` | 已提供不含 etcd 的组件列表，但未接入渲染入口 |
| `GetCertsWithoutEtcd()` | `utils/bkeagent/pkiutil/bkecertlist.go:165` | 已提供不含 etcd 的证书集合，用于 `pkg/job/builtin/collect`，未接入安装证书生成 |
| `MinExternalEtcdVersion` | `utils/bkeagent/pkiutil/consts_sca.go:298` | 常量已定义，未用于任何校验 |
| `prepareExternalEtcdConfig` / `bke-cluster.tmpl` 的 `etcd.external` | `pkg/phaseframe/phases/ensure_cluster_api_obj.go:141`、`common/cluster/initialize/tmpl/bke-cluster.tmpl:56` | 仅用于 bocloud 接管场景，且填充 fake 证书 |

### 3.3 需要补齐的缺口

1. `Etcd` 结构体缺少外接配置字段（`bkecluster_spec.go:382`）。
2. `apiServerFuncMap.etcdServers` 仅能从节点推导端点，且 apiserver 模板证书路径硬编码。
3. `manifest.go` 渲染入口仅按节点角色决定是否渲染 etcd，无外接分支。
4. `handleEtcdMembership` / `addNodeToExistingEtcdCluster` 无外接短路。
5. 升级/备份路径（`kubeadm.go`）无外接短路。
6. `validation.go` 无外接配置校验，且 `NoEtcdNodeError` 与外接模式冲突。

---

## 4. 总体设计方案

### 4.1 设计原则

1. **声明式**：外接 etcd 完全通过 `BKECluster.Spec` 描述，复用 Cluster API `EtcdExternal` 语义。
2. **互斥清晰**：`External` 与内嵌 etcd 在语义上互斥，webhook 强校验。
3. **先加后删、向后兼容**：新增字段与分支，默认路径（内嵌）零变化。
4. **职责边界**：BKE 只负责把控制平面“接上”外部 etcd，不负责外部 etcd 的运维。

### 4.2 数据流（外接模式）

```
用户声明 BKEConfig.Cluster.ControlPlane.Etcd.External:
        endpoints: [...], caFile, certFile, keyFile (SecretRef 可选)
        │
        ▼
[Webhook/Validation] 校验互斥性、端点合法性、版本下限、证书文件可得性
        │
        ▼
[Controller] 生成集群 API 对象 / 下发 Command 时携带 External 语义
        │
        ▼
[Agent] mfutil:
        │   GenerateManifestYaml → 使用 GetComponentListWithOutEtcd()
        │   apiServerFuncMap.etcdServers → 返回 External.Endpoints（逗号连接）
        │   apiserver 模板 → --etcd-cafile/certfile/keyfile 指向 External 证书路径
        │   （跳过 renderEtcd / handleEtcdMembership）
        ▼
[Agent] pkiutil → 使用 GetCertsWithoutEtcd()（不生成 etcd CA/server/peer/healthcheck/api-client）
        ▼
[升级/备份] 跳过 etcd 升级与快照；健康检查仅校验 apiserver↔etcd 连通性
```

### 4.3 内嵌 vs 外接 对比

| 维度 | 内嵌 etcd（现状） | 外接 etcd（提案） |
|------|------------------|------------------|
| etcd 静态 Pod | 渲染 | 不渲染 |
| etcd 成员管理 | handleEtcdMembership | 跳过 |
| etcd 证书 | 生成 CA/server/peer/api-client/healthcheck | 不生成（用户提供 ca/cert/key） |
| apiserver `--etcd-servers` | 从 etcd 角色节点推导 | 从 `External.Endpoints` |
| apiserver etcd TLS | 本地 `/etc/kubernetes/pki/etcd/...` | `External.caFile/certFile/keyFile` |
| 版本来源 | `Cluster.EtcdVersion` / ReleaseImage bundle | 不参与（N5） |
| 升级 | `EnsureEtcdUpgrade` / `upgradeEtcd` | 跳过 |
| 备份 | `etcdctl snapshot save` | 责任转移用户 |

---

## 5. API 设计

### 5.1 新增 `EtcdExternal` 类型

在 `api/bkecommon/v1beta1/bkecluster_spec.go` 的 `Etcd` 结构体（当前位于 `bkecluster_spec.go:381`）中新增 `External` 字段，并新增 `EtcdExternal` 类型：

```go
// Etcd defines the configuration settings for the etcd distributed key-value store.
type Etcd struct {
	// +optional
	ControlPlaneComponent `json:",omitempty"`
	// DataDir specifies the directory path where etcd will store its data.
	// If not specified, defaults to "/var/lib/openFuyao/etcd".
	// +optional
	DataDir string `json:"dataDir,omitempty"`
	// ServerCertSANs ...
	// +optional
	ServerCertSANs []string `json:"serverCertSANs,omitempty"`
	// PeerCertSANs ...
	// +optional
	PeerCertSANs []string `json:"peerCertSANs,omitempty"`

	// External defines how to connect to an external etcd cluster.
	// When set, BKE will not deploy or manage etcd; it will configure
	// kube-apiserver to talk to the external etcd cluster instead.
	// It is mutually exclusive with the embedded etcd (nodes with "etcd" role).
	// +optional
	External *EtcdExternal `json:"external,omitempty"`
}

// EtcdExternal describes how to connect to an external etcd cluster.
// The field semantics are aligned with kubeadm / Cluster API EtcdExternal.
type EtcdExternal struct {
	// Endpoints of the external etcd cluster, e.g.
	// ["https://etcd-0.example.com:2379", "https://etcd-1.example.com:2379"].
	// +required
	Endpoints []string `json:"endpoints"`

	// CAFile is the path (on the control-plane host) to the CA certificate
	// used to verify the etcd server certificate.
	// +optional
	CAFile string `json:"caFile,omitempty"`

	// CertFile is the path (on the control-plane host) to the client
	// certificate used by kube-apiserver to authenticate to etcd.
	// +optional
	CertFile string `json:"certFile,omitempty"`

	// KeyFile is the path (on the control-plane host) to the client
	// private key matching CertFile.
	// +optional
	KeyFile string `json:"keyFile,omitempty"`

	// SecretRef optionally references a Secret whose entries (default keys
	// "ca.crt" / "cert.crt" / "key.key") will be written to the host paths
	// above before kube-apiserver starts. Empty means the files are expected
	// to already exist at CAFile/CertFile/KeyFile.
	// +optional
	SecretRef *EtcdExternalSecretRef `json:"secretRef,omitempty"`
}

// EtcdExternalSecretRef references a Secret containing external etcd TLS material.
type EtcdExternalSecretRef struct {
	// Name of the Secret.
	// +required
	Name string `json:"name"`
	// Namespace of the Secret. Defaults to the BKECluster namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// CaKey is the secret key for the CA certificate. Defaults to "ca.crt".
	// +optional
	CaKey string `json:"caKey,omitempty"`
	// CertKey is the secret key for the client certificate. Defaults to "cert.crt".
	// +optional
	CertKey string `json:"certKey,omitempty"`
	// KeyKey is the secret key for the client private key. Defaults to "key.key".
	// +optional
	KeyKey string `json:"keyKey,omitempty"`
}
```

### 5.2 字段语义与默认值

| 字段 | 必需 | 默认 | 说明 |
|------|------|------|------|
| `Endpoints` | 是 | — | 至少一个 `https://host:port` 端点；端口默认 2379 |
| `CAFile` | 视 TLS 而定 | 空时使用内嵌 etcd 的 `/etc/kubernetes/pki/etcd/ca.crt` 不适用 | 外部 etcd 启用 mTLS 时必填 |
| `CertFile` / `KeyFile` | 视 TLS 而定 | — | 外部 etcd 启用客户端证书认证时必填 |
| `SecretRef` | 否 | 空 | 空表示证书文件已提前就位于宿主路径 |

### 5.3 与 Cluster API 的语义对齐

`EtcdExternal` 与 `sigs.k8s.io/cluster-api` `EtcdExternal`、kubeadm `Etcd.External` 字段对齐，差异点：

| 维度 | Cluster API | 本提案 |
|------|-------------|--------|
| endpoints | `[]string` | `[]string` 一致 |
| CA/cert/key | 同名字段 | 同名字段 |
| Secret 投递 | 无（依赖 bootstrap 数据） | 新增 `SecretRef`，解决控制平面节点证书文件预置问题（BKE 通过 Agent 推送，无 kubeadm bootstrap secret 通道） |

---

## 6. 控制器/校验层设计

### 6.1 校验规则

在 `common/cluster/validation/validation.go` 新增 `ValidateEtcdExternal` 并在 `ValidateControlPlaneComponents` / `ValidateCluster` 中调用：

| 规则 | 校验内容 | 失败处理 |
|------|---------|---------|
| R1 | `External != nil` 时，节点不得携带 `etcd` 角色（与内嵌互斥） | 拒绝，提示互斥 |
| R2 | `Endpoints` 非空，每个端点经 `url.Parse` 且 `scheme` 为 `http/https`、`host:port` 合法 | 拒绝 |
| R3 | 若 `External` 启用 mTLS（存在任一 `CAFile/CertFile/KeyFile`）则三者必须齐全 | 拒绝 |
| R4 | 若提供 `SecretRef`，校验其引用格式合法；否则要求 `CAFile/CertFile/KeyFile` 路径非空 | 拒绝 |
| R5 | 外部 etcd 版本下限（对接时可选探测）不低于 `MinExternalEtcdVersion`（3.2.18） | 告警（探测失败时） |
| R6 | `External != nil` 时跳过 `NoEtcdNodeError` 校验逻辑 | 见 6.2 |

### 6.2 放松“至少一个 etcd 节点”约束

`validation.go:212` 附近当前逻辑：

```go
etcdNodes := nodes.Etcd()
if etcdNodes.Length() == 0 {
    return NoEtcdNodeError()
}
```

改为：仅当 `BkeConfig.Cluster.ControlPlane.Etcd.External == nil` 时执行上述 `NoEtcdNodeError` 校验；外接模式下允许 etcd 角色节点为空。`ValidateNodesRole` / `ValidateNodesFields` 的调用点需透传 `BKEConfig`（当前签名仅接收 `node.Nodes`），需扩展签名或在调用前按 6.1 R1 单独校验互斥性。

### 6.3 Webhook 接入

`webhooks/capbke/` 下对 `BKECluster` 增加 `validate` 逻辑，在**对象创建/更新**时触发 6.1 校验，确保非法外接配置无法进入 Reconcile 主循环。校验失败直接返回聚合错误并拒绝写入。

---

## 7. Agent 渲染与安装设计

### 7.1 静态 Pod 列表选择

`utils/bkeagent/mfutil/manifest.go:55` 的 `GenerateManifestYaml` 现按 `EtcdNodeRole` 决定是否渲染 etcd。新增外接判断：

```go
useExternalEtcd := boot.BkeConfig.Cluster.ControlPlane.Etcd != nil &&
                   boot.BkeConfig.Cluster.ControlPlane.Etcd.External != nil

components := GetDefaultComponentList()      // 含 etcd
if useExternalEtcd {
    components = GetComponentListWithOutEtcd() // 不含 etcd（已存在）
}
```

`GetComponentListWithOutEtcd()` 已存在（`componentlist.go:77`），此处只需在多处调用 `GetDefaultComponentList()` 的位置（安装/升级/回滚渲染入口）统一改为上述分支。

### 7.2 apiserver 模板改造成可覆盖的 etcd TLS

当前 `kube-apiserver.yaml.tmpl` 硬编码：

```
- --etcd-cafile=/etc/kubernetes/pki/etcd/ca.crt
- --etcd-certfile=/etc/kubernetes/pki/apiserver-etcd-client.crt
- --etcd-keyfile=/etc/kubernetes/pki/apiserver-etcd-client.key
- --etcd-servers={{ . | etcdServers }}
```

改造为模板函数：

```
- --etcd-cafile={{ . | etcdCAFile }}
- --etcd-certfile={{ . | etcdCertFile }}
- --etcd-keyfile={{ . | etcdKeyFile }}
- --etcd-servers={{ . | etcdServers }}
```

在 `apiServerFuncMap()`（`render.go:727`）中新增/修改：

```go
"etcdServers": func(cfg *BootScope) string {
    if ext := externalEtcd(cfg); ext != nil {
        return strings.Join(ext.Endpoints, ",")
    }
    // ... 现有从节点推导逻辑
},
"etcdCAFile": func(cfg *BootScope) string {
    if ext := externalEtcd(cfg); ext != nil {
        return ext.CAFile
    }
    return fmt.Sprintf("%s/etcd/ca.crt", cfg.BkeConfig.Cluster.CertificatesDir)
},
"etcdCertFile": ... // 默认 apiserver-etcd-client.crt，外接走 ext.CertFile
"etcdKeyFile":  ... // 默认 apiserver-etcd-client.key，外接走 ext.KeyFile
```

其中 `externalEtcd(cfg)` 为辅助函数，安全返回 `*EtcdExternal`（空指针保护）。

### 7.3 证书文件预置

当 `SecretRef != nil`：Agent 在渲染 apiserver 清单前，将 Secret 中的 `ca.crt`/`cert.crt`/`key.key` 写入 `External.CAFile/CertFile/KeyFile` 指定的宿主路径（默认落盘到 `/etc/kubernetes/pki/etcd-external/`）。当 `SecretRef == nil`：校验文件已存在（`os.Stat`），否则安装失败并给出明确错误。

### 7.4 跳过成员管理

`render.go` 的 `renderEtcd` → `handleEtcdMembership` 仅在内嵌模式下执行。外接模式下 `renderEtcd` 不再被调用（因组件列表已剔除 etcd），故天然短路；同时 `handleEtcdMembership` / `addNodeToExistingEtcdCluster` 增补防御性检查：`externalEtcd(cfg) != nil` 时直接返回 `nil`。

---

## 8. 证书设计

### 8.1 内嵌模式（不变）

`GetCertsWithoutCA` / `GetCACerts` / `GetEtcdCerts` 生成全套证书。

### 8.2 外接模式

安装证书生成处（`pkg/job/builtin/*`、`EnsureCerts` 相关路径）改用已存在的 `GetCertsWithoutEtcd()`（`bkecertlist.go:165`），仅生成：

- `RootCA`、`APIServer`、`KubeletClient`、`ServiceAccount`、`FrontProxyCA`、`FrontProxyClient`

不再生成 `EtcdCA` / `EtcdServer` / `EtcdPeer` / `EtcdHealthcheck` / `EtcdAPIClient`（对应 `bkecertlist.go:177` 的 `GetEtcdCerts`）。

> 说明：`GetCertsWithoutEtcd()` 目前已被 `pkg/job/builtin/collect` 使用，本 KEP 仅需把同一选择器接入安装证书生成路径。

---

## 9. 升级与备份设计

### 9.1 版本来源

外接模式下 `Cluster.EtcdVersion` / ReleaseImage bundle 中的 `etcd` 组件版本**不再参与**控制平面版本计算与 etcd 升级（即 `etcdImageTagFromBootScope` 不会命中，因为不渲染 etcd 清单）。对外接 etcd，其版本由用户保证，BKE 可选记录冗余字段 `Status.EtcdExternalEndpoint` 用于可观测，不强制。

### 9.2 升级

`pkg/job/builtin/kubeadm/kubeadm.go`：

- `upgradeEtcd`、`needUpgradeEtcd`、`getBeforeUpgradeComponentPodHash`（etcd 维度）——外接模式下跳过。
- `upgradeControlPlane` 的组件逐项升级中，etcd 组件通过 `mfutil.GetControlPlaneComponents()` 返回列表控制，外接时剔除 etcd（配合 7.1 组件列表分支）。

`EnsureEtcdUpgrade` Phase（`pkg/phaseframe/phases/ensure_etcd_upgrade.go`）外接模式下 `NeedExecute` 返回 false。

### 9.3 备份

`prepareUpgrade` → `backupEtcd` 外接模式下跳过。ClusterBackup（KEP-12）中的 etcd 快照维度外接模式下置为“不支持”，并在 `Status` 中标注 `EtcdBackupSkipped`（因不在 BKE 职责范围）。

### 9.4 健康检查 / 连通性探测

新增轻量级探测：外接模式下，安装完成后由 Agent 或 Controller 使用 `External.Endpoints + CAFile/CertFile/KeyFile` 建立 `clientv3` 客户端执行 `get /` 或 `maintenance.Status`，校验连通性与 etcd 版本下限（复用 `utils/bkeagent/etcd` 包）。失败时通过 Condition 上报 `EtcdExternalUnreachable`。

---

## 10. 声明式组件框架的集成

### 10.1 Catalog / Component 视角

KEP-6 的组件 Catalog 将 etcd 视为“由 `EnsureMasterInit` 嵌入创建”的组件（无独立 handler，见 `kep10-install-dag-with-phaseflow-fallback.md` §5.2）。外接模式下：

- Catalog 中 `etcd` 组件标记为 `ExternalManaged`，`SkipCompleted` 生效；
- `ComponentVersion.Spec.NodeFilter`（KEP-19）不再对 etcd 节点过滤（无 etcd 节点）；
- 版本来源链路（ReleaseImage → bundle → Command `etcdVersion` 参数，见 `kubeadm.go:436` `applyCommandEtcdVersion`）外接模式下短路，即控制器不再下发 `etcdVersion` 参数。

### 10.2 Command 参数

`Kubeadm` 插件参数（`kubeadm.go:109` 的 `etcdVersion`）保持不变；仅当内嵌 etcd 时才下发。新增可选参数 `etcdExternal=true` 以在 Agent 侧开关外接路径（由控制器根据 `Etcd.External` 注入），避免 Agent 侧重复解析完整配置。

---

## 11. 兼容性与边界

### 11.1 向后兼容

- 未配置 `External` 字段即为 `nil`，所有新分支短路，现有内嵌 etcd 行为零变化。
- CRD schema 仅新增可选字段，旧对象反序列化不受影响（字段缺省 `nil`）。
- 新增 webhook 校验仅对配置了 `External` 的对象生效。

### 11.2 约束与限制

| 约束 | 说明 |
|------|------|
| 不支持在线迁移 | 内嵌 ↔ 外接切换需重建控制平面（N3） |
| TLS 文件为宿主路径 | 外接证书需位于控制平面节点可访问路径（BKE 通过 Agent 投递或要求预置） |
| 外接 etcd 版本下限 | kubeadm 支持外接 etcd ≥ 3.2.18（`MinExternalEtcdVersion`） |
| 备份职责转移 | BKE 不对外接 etcd 做快照/恢复编排 |

---

## 12. 工作量评估与里程碑

### 12.1 任务分解

| 阶段 | 任务 | 产出 | 预估（人日） |
|------|------|------|------------|
| 1 | API 扩展 | `EtcdExternal` / `EtcdExternalSecretRef` 类型、deepcopy、CRD YAML | 1 |
| 2 | 校验 + Webhook | `ValidateEtcdExternal`、互斥校验、`NoEtcdNodeError` 放松、webhook 接线 | 2 |
| 3 | Agent 渲染 | `manifest.go` 组件列表分支、`apiServerFuncMap` 覆盖、模板改造、成员管理短路 | 3 |
| 4 | 证书 + Secret 投递 | `GetCertsWithoutEtcd` 接入安装路径、SecretRef 落盘 | 2 |
| 5 | 升级/备份/健康检查 | `kubeadm.go` 短路、KEP-12 备份标注、连通性探测 | 2 |
| 6 | 声明式框架集成 | 控制器不下发 `etcdVersion`、`etcdExternal` 参数注入 | 1 |
| 7 | 测试与文档 | 单元/集成测试、样例、KEP 收尾 | 3 |
| **合计** | | | **14** |

### 12.2 里程碑

1. **M1**：API + 校验（阶段 1-2），可被 webhook 拦截非法外接配置。
2. **M2**：Agent 渲染闭环（阶段 3-4），外接 etcd 可安装出对接外部的控制平面。
3. **M3**：升级/备份/健康检查 + 声明式集成（阶段 5-6）。
4. **M4**：测试补齐与毕业（阶段 7）。

---

## 13. 风险与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| 外接 etcd 端点不可达 | 控制平面无法启动 | 安装后立即做连通性探测并上报 `EtcdExternalUnreachable` Condition |
| 证书路径/权限错误 | apiserver 无法通过 etcd mTLS | 渲染前 `os.Stat` 校验 + SecretRef 统一投递 + 明确错误信息 |
| 内嵌/外接语义混淆 | 出现既渲染 etcd 又对接外部的情况 | Webhook 强互斥校验 R1 |
| 外接 etcd 版本过低 | apiserver 协议不兼容 | 探测版本下限告警（R5） |
| 用户误以为 BKE 会备份外接 etcd | 数据丢失预期落差 | 备份 `Status` 明确标注 `EtcdBackupSkipped`，文档说明职责边界 |
| 存量 `NoEtcdNodeError` 校验误伤 | 外接集群无法创建 | 6.2 精准放松校验逻辑 |

---

## 14. 毕业标准

| 级别 | 标准 |
|------|------|
| **Alpha** | 外接 etcd 可安装出可用控制平面；webhook 拦截非法配置；默认内嵌路径无回归 |
| **Beta** | 支持 Secret 证书投递；升级/备份短路正确；连通性与版本下限探测完备；单元/集成测试覆盖率达标 |
| **GA** | 与 KEP-5/6 声明式框架全集成；样例与 docs 完善；支持多端点高可用外接 etcd 的端到端验证 |

---

## 15. 替代方案

### 15.1 方案 B：复用 bocloud 的 `prepareExternalEtcdConfig` fake 语义直接扩展

将现有 `EnsureClusterAPIObj` 路径中 fake 证书替换为真实外接配置，仅走 Cluster API 的 `KubeadmControlPlane.clusterConfiguration.etcd.external`。

- **优点**：改动面小，复用已有模板通道。
- **缺点**：该路径专用于 bocloud“接管”场景，控制平面实际由 KCP（Cluster API）而非 BKE Agent 静态 Pod 路径渲染，无法覆盖 BKE 原生的 `mfutil` 静态 Pod 安装主链路；语义混淆，维护成本高。**故不采纳**。

### 15.2 方案 C：外接 etcd 作为独立 CRD（`ExternalEtcd`）

新增 `ExternalEtcd` CRD，通过引用关系注入。

- **优点**：可幂等复用、独立 RBAC/生命周期。
- **缺点**：当前 BKE 全部沿 `ClusterConfig` 内联配置，独立 CRD 引入跨资源协调与别名解析复杂度，短期 ROI 低。可作为后续演进，当前内联字段更贴合 kubeadm/Cluster API 约定。

### 15.3 结论

采用**主方案（5-14 章）**，内联 `Etcd.External` + 端到端接线，兼顾语义对齐、改动可控与后续可演进。

---

## 附：关键代码引用

| 关注点 | 位置 |
|--------|------|
| `Etcd` 类型定义 | `api/bkecommon/v1beta1/bkecluster_spec.go:381` |
| `Cluster` 中 `EtcdVersion` | `api/bkecommon/v1beta1/bkecluster_spec.go:146` |
| 渲染入口 / etcd 节点过滤 | `utils/bkeagent/mfutil/manifest.go:55-81` |
| etcd 渲染与成员管理 | `utils/bkeagent/mfutil/render.go:267-383` |
| apiserver 模板函数 | `utils/bkeagent/mfutil/render.go:727-772` |
| apiserver 静态 Pod 模板 | `utils/bkeagent/mfutil/tmpl/k8s/kube-apiserver.yaml.tmpl:18-21` |
| etcd 静态 Pod 模板 | `utils/bkeagent/mfutil/tmpl/k8s/etcd.yaml.tmpl` |
| 组件列表（含/不含 etcd） | `utils/bkeagent/mfutil/componentlist.go:66-83` |
| 证书集合（含/不含 etcd） | `utils/bkeagent/pkiutil/bkecertlist.go:164-185` |
| 最小外接 etcd 版本 | `utils/bkeagent/pkiutil/consts_sca.go:297-298` |
| etcd 升级 / 备份 | `pkg/job/builtin/kubeadm/kubeadm.go:255-395` |
| etcd 版本参数覆盖 | `pkg/job/builtin/kubeadm/kubeadm.go:434-446` |
| 旧 bocloud 外接链路 | `pkg/phaseframe/phases/ensure_cluster_api_obj.go:141-163` |
| 集群 API 对象模板（etcd.external） | `common/cluster/initialize/tmpl/bke-cluster.tmpl:56-67` |
| 校验入口 | `common/cluster/validation/validation.go:71-220` |