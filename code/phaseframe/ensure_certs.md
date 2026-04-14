# 详细的业务流程梳理
## EnsureCerts 业务流程梳理
### 一、整体定位
[ensure_certs.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_certs.go) 是 BKECluster 部署流程中的证书管理阶段，负责**生成 Kubernetes 集群所需的全部 TLS 证书、ServiceAccount 密钥和 KubeConfig**，并将它们以 Secret 形式存储在管理集群中。核心逻辑委托给 [BKEKubernetesCertGenerator](file:///d:/code/github/cluster-api-provider-bke/pkg/certs/generator.go)。
### 二、阶段定义
```
阶段名称: EnsureCerts
```
### 三、核心流程（Execute 方法）
```
Execute()
  │
  ├── 1. certsGenerator.LookUpOrGenerate()
  │     查找已有证书 → 不存在则生成
  │
  ├── 2. certsGenerator.NeedGenerate()
  │     检查是否仍有缺失证书
  │     ├── 仍有缺失 → 返回错误（触发重新调谐）
  │     └── 全部就绪 → 返回成功
  │
  └── 3. 返回 ctrl.Result{}
```
### 四、NeedExecute 判断逻辑
```
NeedExecute(old, new)
  │
  ├── 1. BasePhase.DefaultNeedExecute() → 基础判断
  │     不需要 → return false
  │
  ├── 2. certsGenerator.NeedGenerate()
  │     ├── 所有证书已存在 → return false（不需要执行）
  │     └── 有证书缺失 → 设置状态为 PhaseWaiting，return true
  │
  └── 3. return true
```
### 五、LookUpOrGenerate 详细流程
这是证书生成的核心方法，分为 **4 个步骤**：
```
LookUpOrGenerate()
  │
  ├── Step 1: setupGlobalCA()         — 设置全局 CA
  │
  ├── Step 2: prepareBkeCerts(false)  — 准备证书列表并注入 SANs
  │
  ├── Step 3: generateCertificates()  — 生成证书
  │
  └── Step 4: createCertificateSecrets() — 创建 Secret 资源
```
#### Step 1: setupGlobalCA — 设置全局 CA
```
setupGlobalCA()
  │
  ├── LoadGlobalCA()
  │     ├── 尝试从 K8s Secret 加载（kube-system/global-ca）
  │     │     ├── 找到且有效 → isUserCustomCA = true，使用用户提供的 CA
  │     │     └── 未找到 → 继续下一步
  │     │
  │     └── 尝试从本地文件加载
  │           ├── /etc/openFuyao/certs/global-ca.crt
  │           ├── /etc/openFuyao/certs/global-ca.key
  │           ├── 加载成功 → 上传为 Secret，isUserCustomCA = true
  │           └── 加载失败 → 使用自签名 CA（默认模式）
  │
  └── isUserCustomCA = true?
        └── YES → LoadConfigForCerts()  — 从 ConfigMap 加载用户自定义证书配置
```
**两种 CA 模式**：

| 模式 | 触发条件 | CA 来源 | 证书签名方式 |
|------|----------|---------|-------------|
| **自签名模式** | 未提供 Global CA | 自动生成 Root CA、Etcd CA、Front Proxy CA | 各 CA 自签名，下属证书由对应 CA 签发 |
| **用户自定义 CA 模式** | 提供了 Global CA Secret 或本地文件 | 使用 Global CA | Root CA、Etcd CA、Front Proxy CA 均由 Global CA 签发 |
#### Step 2: prepareBkeCerts — 准备证书列表并注入 SANs
```
prepareBkeCerts(isVerify)
  │
  ├── 1. 选择证书列表
  │     ├── isUserCustomCA = true → GetUserCustomCerts() + SetCertsCAName()
  │     │     （将 CA 证书的 CAName 设为 "global-ca"，IsCA 设为 true）
  │     ├── isVerify = true → GetCertsWithoutCA()（仅非 CA 证书）
  │     └── 默认 → GetDefaultCertList()（包含所有 CA 和非 CA 证书）
  │
  ├── 2. 附加 Admin KubeConfig 证书（如果需要）
  │
  └── 3. 为特定证书注入 AltNames（SANs）
        ├── apiserver → GetAPIServerCertAltNamesWithNodes()
        │     注入：节点 DNS、节点 IP、ControlPlaneEndpoint、Service CIDR DNS IP、额外 SANs
        ├── etcd-server → GetEtcdCertAltNamesWithNodes(isServer=true)
        │     注入：etcd 节点 IP、localhost、0.0.0.0、::1、节点 hostname
        ├── etcd-peer → GetEtcdCertAltNamesWithNodes(isServer=false)
        │     注入：同 etcd-server
        ├── controller-manager → GetMasterNodeAltNamesWithNodes()
        │     注入：master 节点 IP
        └── scheduler → GetMasterNodeAltNamesWithNodes()
              注入：master 节点 IP
```
**额外 AltNames 来源**（`getExtraAltNames()`）：
- `ControlPlaneEndpoint.Host`（负载均衡域名/IP）
- `CustomExtra` 中的 `loadBalanceIP`（外部负载均衡 IP）
#### Step 3: generateCertificates — 生成证书
```
generateCertificates()
  │
  ├── 1. 遍历 bkeCerts 列表
  │     对每个证书：
  │     ├── lookup(cert) → 检查 Secret 是否已存在
  │     │     ├── 已存在 → 跳过
  │     │     └── 不存在 → 需要生成
  │     │
  │     ├── cert.CAName != "" → generateCertAndKeyWithCA()
  │     │     由上级 CA 签发：
  │     │     ├── 从 caCertificatesContent 中找到 CA 证书和密钥
  │     │     ├── 解析 CA 证书和私钥
  │     │     └── pkiutil.NewCertAndKey() → 生成证书
  │     │
  │     └── cert.CAName == "" → generateCACertAndKey()
  │           自签名 CA：
  │           └── pkiutil.NewCertificateAuthority() → 生成 CA
  │
  ├── 2. 生成 SA 证书
  │     ├── lookup(saCert) → 检查是否已存在
  │     └── 不存在 → generateSAKeyAndPublicKey()
  │           生成 RSA 密钥对，公钥写入 tls.crt，私钥写入 tls.key
  │
  └── 3. 返回 needCreateSecret 标志
```
#### Step 4: createCertificateSecrets — 创建 Secret
```
createCertificateSecrets()
  │
  ├── 1. transferCACertificates()
  │     将 caCertificatesContent 合并到 certificatesContent（统一处理）
  │
  ├── 2. createCertSecrets()
  │     遍历 certificatesContent：
  │     ├── 跳过 GlobalCASecretName（全局 CA 不创建为集群 Secret）
  │     └── createSingleCertSecret()
  │           ├── Secret 名称: <clusterName>-<certName>
  │           ├── Secret 类型: BKESecretType
  │           ├── OwnerReference: 指向 BKECluster
  │           └── createOrUpdateSecret()
  │                 ├── Create → 成功
  │                 └── AlreadyExists → Delete + Create（覆盖）
  │
  └── 3. maybeCreateKubeConfig()
        ├── needCreateKubeConfig = false → 跳过
        └── needCreateKubeConfig = true → GenerateKubeConfig()
              ├── createInitialKubeConfig(endpoint)
              │     使用 CAPI 的 kubeconfig.CreateSecretWithOwner()
              │     生成 admin kubeconfig Secret（value 字段）
              │
              └── IsHACluster()?
                    ├── YES → handleHAKubeConfig()
                    │     ├── 获取刚创建的 kubeconfig Secret
                    │     ├── 将 endpoint 替换为域名: https://<MasterHADomain>:<port>
                    │     └── 写入 Secret 的 "ha" 字段
                    │     （"value" 字段供程序使用，"ha" 字段供节点使用）
                    │
                    └── NO → 完成
```
### 六、完整证书清单
EnsureCerts 阶段生成以下证书，每个证书存储为一个独立的 Secret：

| # | 证书名称 (Name) | 描述 (LongName) | 类型 | 签发 CA | Secret 名称 |
|---|----------------|----------------|------|---------|------------|
| 1 | `ca` | Root Certificate Authority | CA | 自签名/Global CA | `<cluster>-ca` |
| 2 | `apiserver` | certificate for serving the Kubernetes API | Server | `ca` | `<cluster>-apiserver` |
| 3 | `apiserver-kubelet-client` | certificate for the API server to connect to kubelet | Client | `ca` | `<cluster>-apiserver-kubelet-client` |
| 4 | `front-proxy-ca` | self-signed CA to provision identities for front proxy | CA | 自签名/Global CA | `<cluster>-front-proxy-ca` |
| 5 | `front-proxy-client` | certificate for the front proxy client | Client | `front-proxy-ca` | `<cluster>-front-proxy-client` |
| 6 | `etcd` | self-signed CA to provision identities for etcd | CA | 自签名/Global CA | `<cluster>-etcd` |
| 7 | `etcd-server` | certificate for serving etcd | Server+Client | `etcd-ca` | `<cluster>-etcd-server` |
| 8 | `etcd-peer` | certificate for etcd nodes to communicate | Server+Client | `etcd-ca` | `<cluster>-etcd-peer` |
| 9 | `etcd-healthcheck-client` | certificate for liveness probes to healthcheck etcd | Client | `etcd-ca` | `<cluster>-etcd-healthcheck-client` |
| 10 | `apiserver-etcd-client` | certificate for the apiserver to access etcd | Client | `etcd-ca` | `<cluster>-apiserver-etcd-client` |
| 11 | `sa` | certificate for the service account token issuer | RSA 密钥对 | 无 | `<cluster>-sa` |
| 12 | `kubeconfig` | admin kubeconfig used by the superuser/admin | KubeConfig | `ca` | `<cluster>-kubeconfig` |
### 七、证书签发关系图
```
自签名模式:
══════════

  Global CA (不存在)
       │
  ┌────┴────────────────────┐
  │                         │
  Root CA (自签名)    Front Proxy CA (自签名)    Etcd CA (自签名)
  ├── apiserver           ├── front-proxy-client      ├── etcd-server
  ├── apiserver-kubelet   │                           ├── etcd-peer
  │   -client             │                           ├── etcd-healthcheck
  └── kubeconfig (admin)  │                           │   -client
                          │                           └── apiserver-etcd
                          │                               -client
                          │
                    SA (RSA 密钥对，无 CA)


用户自定义 CA 模式:
═══════════════════

  Global CA (用户提供)
       │
  ┌────┴────────────────────┐
  │                         │
  Root CA (Global CA签发)  Front Proxy CA (Global CA签发)  Etcd CA (Global CA签发)
  ├── apiserver           ├── front-proxy-client           ├── etcd-server
  ├── apiserver-kubelet   │                                ├── etcd-peer
  │   -client             │                                ├── etcd-healthcheck
  └── kubeconfig (admin)  │                                │   -client
                          │                                └── apiserver-etcd
                          │                                    -client
                          │
                    SA (RSA 密钥对，无 CA)
```
### 八、HA 集群 KubeConfig 双字段设计
对于 HA 集群（`ControlPlaneEndpoint` 有效且 Host 不是节点 IP），kubeconfig Secret 包含两个字段：

| 字段 | Endpoint | 用途 |
|------|----------|------|
| `value` | `https://<ControlPlaneEndpoint>` | 程序内部使用（直连负载均衡） |
| `ha` | `https://bke-master-ha:<port>` | 节点使用（通过域名解析，配合 hosts 文件实现高可用） |

`bke-master-ha` 是固定域名（`constant.MasterHADomain`），需要在节点环境初始化时配置 hosts 解析。
### 九、整体流程图
```
┌──────────────────────────────────────────────────────────────┐
│                  EnsureCerts.Execute()                        │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌─ setupGlobalCA() ──────────────────────────────────────┐  │
│  │  尝试加载 Global CA                                      │  │
│  │  ├── K8s Secret (kube-system/global-ca)                 │  │
│  │  └── 本地文件 (/etc/openFuyao/certs/global-ca.*)        │  │
│  │  找到 → isUserCustomCA=true, 加载自定义证书配置          │  │
│  │  未找到 → 使用自签名模式                                 │  │
│  └─────────────────────────────────────────────────────────┘  │
│                           ↓                                   │
│  ┌─ prepareBkeCerts() ────────────────────────────────────┐  │
│  │  选择证书列表 (10 + SA + KubeConfig)                     │  │
│  │  注入 SANs:                                              │  │
│  │  ├── apiserver ← 节点IP/DNS + LB + ServiceCIDR          │  │
│  │  ├── etcd-server/peer ← etcd节点IP + localhost          │  │
│  │  └── controller-manager/scheduler ← master节点IP        │  │
│  └─────────────────────────────────────────────────────────┘  │
│                           ↓                                   │
│  ┌─ generateCertificates() ───────────────────────────────┐  │
│  │  遍历证书列表:                                           │  │
│  │  ├── lookup() → Secret 已存在? 跳过                     │  │
│  │  ├── CA 证书 → generateCACertAndKey()                   │  │
│  │  ├── 非CA证书 → generateCertAndKeyWithCA()              │  │
│  │  └── SA → generateSAKeyAndPublicKey()                   │  │
│  └─────────────────────────────────────────────────────────┘  │
│                           ↓                                   │
│  ┌─ createCertificateSecrets() ───────────────────────────┐  │
│  │  ├── transferCACertificates() → 合并 CA 到统一 map       │  │
│  │  ├── createCertSecrets() → 逐个创建 Secret              │  │
│  │  │   Secret: <cluster>-<certName>                       │  │
│  │  │   OwnerRef: BKECluster                               │  │
│  │  └── maybeCreateKubeConfig()                            │  │
│  │      ├── 创建 admin kubeconfig Secret                   │  │
│  │      └── HA集群 → 追加 "ha" 字段（域名endpoint）        │  │
│  └─────────────────────────────────────────────────────────┘  │
│                           ↓                                   │
│  ┌─ NeedGenerate() ───────────────────────────────────────┐  │
│  │  检查所有证书是否都已存在                                 │  │
│  │  ├── 有缺失 → 返回错误（触发重新调谐）                   │  │
│  │  └── 全部就绪 → 完成                                    │  │
│  └─────────────────────────────────────────────────────────┘  │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```
### 十、设计要点总结
1. **Lookup-Or-Generate 模式**：先查找已有证书，不存在才生成，支持幂等执行和增量补全
2. **双 CA 模式**：支持自签名 CA（默认）和用户自定义 Global CA，通过 `isUserCustomCA` 标志切换签发链
3. **节点感知的 SANs 注入**：根据 BKENode 列表动态计算 apiserver、etcd 等证书的 AltNames，确保证书覆盖所有节点 IP
4. **Secret 所有权**：所有证书 Secret 设置 OwnerReference 指向 BKECluster，集群删除时自动级联清理
5. **HA KubeConfig 双字段**：`value` 字段供程序直连使用，`ha` 字段使用固定域名供节点使用，配合 hosts 解析实现高可用
6. **幂等创建**：Secret 已存在时采用 Delete+Create 策略覆盖，确保内容最新
7. **KubeConfig 重试机制**：HA 集群查找 kubeconfig Secret 时有 3 次重试，处理 HA 字段竞态条件
        
