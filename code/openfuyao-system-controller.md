        
# cluster-api-provider-bke如何拉起openfuyao-system-controller组件
## 一、cluster-api-provider-bke如何拉起openfuyao-system-controller组件
### 1. 整体流程架构
```
┌─────────────────────────────────────────────────────────────────┐
│              openfuyao-system-controller 部署流程                │
└─────────────────────────────────────────────────────────────────┘

用户创建BKECluster CR
        │
        ▼
┌──────────────────────────┐
│ BKEClusterReconciler     │
│ 监听BKECluster CR变更     │
└────────┬─────────────────┘
         │
         ▼
┌──────────────────────────┐
│ Phase Flow Engine        │
│ 确定需要执行的Phase       │
└────────┬─────────────────┘
         │
         ▼
┌──────────────────────────┐
│ EnsureAddonDeploy Phase  │
│ 执行Addon部署阶段         │
└────────┬─────────────────┘
         │
         ▼
┌──────────────────────────┐
│ 识别openfuyao-system-    │
│ controller addon         │
└────────┬─────────────────┘
         │
         ├─► 前置操作：addControlPlaneLabels()
         │   └─► 为Master节点添加control-plane标签
         │
         ├─► 前置操作：distributePatchCM()
         │   └─► 下发Patch ConfigMap到目标集群
         │
         ▼
┌──────────────────────────┐
│ InstallAddon()           │
│ 安装Addon                │
└────────┬─────────────────┘
         │
         ├─► 判断Addon类型
         │   ├─► yaml类型：installYamlAddon()
         │   └─► chart类型：installChartAddon() ◄── openfuyao-system-controller
         │
         ▼
┌──────────────────────────┐
│ Helm Install             │
│ 使用Helm安装Chart         │
└────────┬─────────────────┘
         │
         ▼
┌──────────────────────────┐
│ 后置操作：生成默认用户    │
│ username/password         │
└──────────────────────────┘
```
### 2. 关键代码实现
#### 2.1 Addon定义
**位置**：[guide.md:142](file:///d:/code/github/cluster-api-provider-bke/guide.md#L142)
```go
func (op *Options) createSystemControllerAddon() confv1beta1.Product {
    return confv1beta1.Product{
        Name:    "openfuyao-system-controller",
        Version: "latest",
        Type:    "chart",  // 关键：定义为chart类型
        Param: map[string]string{
            "helmRepo":   "https://helm.openfuyao.cn/_core",
            "tagVersion": "latest",
        },
    }
}
```
**关键字段说明**：
- `Name`: "openfuyao-system-controller"
- `Type`: "chart" - 表示这是一个Helm Chart类型的Addon
- `helmRepo`: Helm仓库地址
- `tagVersion`: 版本标签
#### 2.2 Phase执行流程
**位置**：[ensure_addon_deploy.go:75-130](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_addon_deploy.go#L75-L130)
```go
type EnsureAddonDeploy struct {
    phaseframe.BasePhase
    addons              []*bkeaddon.AddonTransfer
    targetClusterClient kube.RemoteKubeClient
    remoteClient        *kubernetes.Clientset
    remoteDynamicClient dynamic.Interface
    addonRecorders      []*kube.AddonRecorder
}

func (e *EnsureAddonDeploy) Execute() (ctrl.Result, error) {
    // 1. 创建远程集群客户端
    targetClusterClient, err := kube.NewRemoteClientByBKECluster(e.Ctx.Context, e.Ctx.Client, e.Ctx.BKECluster)
    if err != nil {
        return ctrl.Result{}, err
    }
    e.targetClusterClient = targetClusterClient
    
    // 2. 执行Addon协调
    if err = e.reconcileAddon(); err != nil {
        return ctrl.Result{}, err
    }
    return ctrl.Result{}, nil
}
```
#### 2.3 Addon协调逻辑
**位置**：[ensure_addon_deploy.go:150-298](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_addon_deploy.go#L150-L298)
```go
func (e *EnsureAddonDeploy) reconcileAddon() error {
    // 1. 验证和准备数据
    result := e.validateAndPrepare(ValidateAndPrepareParams{
        Ctx: e.Ctx,
    })
    if !result.Continue {
        return nil
    }
    
    // 2. 遍历处理每个Addon
    for _, addonT := range result.AddonsT {
        // 前置自定义操作
        if addonT.Operate == bkeaddon.CreateAddon {
            if err := e.addonBeforeCreateCustomOperate(addonT.Addon); err != nil {
                return err
            }
        }
        
        // 处理单个Addon
        processResult := e.processAddon(ProcessAddonParams{
            AddonT:              addonT,
            BKECluster:          result.BKECluster,
            TargetClusterClient: e.targetClusterClient,
            Client:              result.Client,
            Ctx:                 e.Ctx,
            Log:                 result.Log,
        })
        
        // 更新Addon状态
        if err := e.updateAddonStatus(UpdateAddonStatusParams{...}); err != nil {
            return err
        }
    }
    
    return nil
}
```
#### 2.4 openfuyao-system-controller特殊处理
**位置**：[ensure_addon_deploy.go:378-379](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_addon_deploy.go#L378-L379)
```go
func (e *EnsureAddonDeploy) addonBeforeCreateCustomOperate(addon *confv1beta1.Product) error {
    switch addon.Name {
    case constant.OpenFuyaoSystemController:
        return e.handleOpenFuyaoSystemController()
    // ... 其他addon处理
    }
}

func (e *EnsureAddonDeploy) handleOpenFuyaoSystemController() error {
    // 1. 添加控制平面标签
    if err := e.addControlPlaneLabels(); err != nil {
        return err
    }
    
    // 2. 下发Patch ConfigMap
    if err := e.distributePatchCM(); err != nil {
        return err
    }
    
    return nil
}
```
**关键操作**：
1. **添加控制平面标签**：
```go
func (e *EnsureAddonDeploy) addControlPlaneLabels() error {
    // 获取所有Master节点
    bkeNodes := bkenode.Nodes(allNodes).Master()
    
    // 为每个Master节点添加control-plane标签
    for _, node := range remoteNodes.Items {
        for _, bkeNode := range bkeNodes {
            if node.GetName() == bkeNode.Hostname {
                labelhelper.SetMasterRoleLabel(&node)
                _, err := e.remoteClient.CoreV1().Nodes().Update(ctx, &node, metav1.UpdateOptions{})
            }
        }
    }
}
```
2. **下发Patch ConfigMap**：
```go
func (e *EnsureAddonDeploy) distributePatchCM() error {
    // 1. 从管理集群获取patch配置
    patchCM := &corev1.ConfigMap{}
    c.Get(ctx, client.ObjectKey{Namespace: "openfuyao-patch", Name: patchCMKey}, patchCM)
    
    // 2. 确保目标集群存在openfuyao-system-controller命名空间
    nsName := constant.OpenFuyaoSystemController
    clSet.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
    
    // 3. 创建或更新patch configmap
    remoteCM := &corev1.ConfigMap{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "patch-config",
            Namespace: nsName,
        },
        Data: map[string]string{"patch-data": data},
    }
    clSet.CoreV1().ConfigMaps(nsName).Create(ctx, remoteCM, metav1.CreateOptions{})
}
```
#### 2.5 Helm安装执行
**位置**：[addon.go:131-150](file:///d:/code/github/cluster-api-provider-bke/pkg/kube/addon.go#L131-L150)
```go
func (c *Client) InstallAddon(bkeCluster *bkev1beta1.BKECluster, addonT *bkeaddon.AddonTransfer, 
    addonRecorder *AddonRecorder, localClient client.Client, bkeNodes bkenode.Nodes) error {
    
    addon := addonT.Addon.DeepCopy()
    bkeConfig := bkeCluster.Spec.ClusterConfig
    cfg := bkeinit.BkeConfig(*bkeConfig)

    // 判断Addon类型
    if addon.Type == "chart" {
        // openfuyao-system-controller走这里
        return c.installChartAddon(addon, addonT.Operate, bkeCluster.Namespace, cfg, localClient)
    }
    return c.installYamlAddon(addon, addonT, bkeCluster, cfg, addonRecorder, bkeNodes)
}
```
**位置**：[chart.go:46-100](file:///d:/code/github/cluster-api-provider-bke/pkg/kube/chart.go#L46-L100)
```go
func (c *Client) installChartAddon(addon *confv1beta1.Product, addonOperate bkeaddon.AddonOperate,
    bkeClusterNS string, cfg bkeinit.BkeConfig, localClient client.Client) error {
    
    c.Log.Infof("starting %s chart addon %s", addonOperate, addon.Name)

    var err error
    switch addonOperate {
    case bkeaddon.CreateAddon:
        err = c.handleCreateChartOperation(addon, cfg, bkeClusterNS, localClient)
    case bkeaddon.UpdateAddon:
        err = c.handleUpgradeChartOperation(addon, cfg, bkeClusterNS, localClient)
    case bkeaddon.UpgradeAddon:
        err = c.handleUpgradeChartOperation(addon, cfg, bkeClusterNS, localClient)
    case bkeaddon.RemoveAddon:
        err = c.handleRemoveChartOperation(addon)
    }

    return err
}

func (c *Client) handleCreateChartOperation(addon *confv1beta1.Product, cfg bkeinit.BkeConfig, 
    bkeClusterNS string, localClient client.Client) error {
    
    c.Log.Info("starting install chart addon ", addon.Name)
    
    // 1. 初始化Helm Action配置
    actionConfig, err := c.initActionConfig(addon.Namespace)
    if err != nil {
        return err
    }
    
    // 2. 获取Chart Values
    values, err := c.getChartValues(addon, bkeClusterNS, localClient)
    if err != nil {
        return err
    }
    
    // 3. 获取Chart包
    chartFile, err := c.fetchChartPackage(addon, cfg, bkeClusterNS, localClient)
    if err != nil {
        return err
    }
    
    // 4. 执行Helm Install
    install := action.NewInstall(actionConfig)
    install.ReleaseName = releaseName
    install.Namespace = addon.Namespace
    install.Timeout = c.getChartTimeout(addon)
    install.Wait = addon.Block
    install.WaitForJobs = true
    
    _, err = install.Run(chartFile, values)
    if err != nil {
        return err
    }
    
    c.Log.Info("install chart ", addon.Name, " success")
    return nil
}
```
#### 2.6 后置操作：生成默认用户
**位置**：[ensure_addon_deploy.go:570-610](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_addon_deploy.go#L570-L610)
```go
func (e *EnsureAddonDeploy) addonAfterCreateCustomOperate(addon *confv1beta1.Product, bkeCluster *bkev1beta1.BKECluster) {
    switch addon.Name {
    case constant.OpenFuyaoSystemController:
        // 创建默认的用户名和密码
        cfg := phaseutil.UserInfoConfig{
            PasswdLength:  DefaultPasswordLength,  // 12
            SaltLength:    DefaultSaltLength,      // 16
            Iterations:    DefaultIterations,      // 100000
            KeyLength:     DefaultKeyLength,       // 64
            EncryptMethod: sha256.New,
        }

        username, passwd, err := phaseutil.GenerateDefaultUserInfo(e.remoteDynamicClient, cfg)
        if err != nil {
            e.Ctx.Log.Error("NotCreateDefaultUser", err.Error())
            return
        }
        
        // 打印登录信息
        msg := `
The website of the openFuyao is as follows:
    https://%s:%s

You can login to the openFuyao using the following username and password:
    username: %s
    password: %s
`
        e.Ctx.Log.Info("openFuyaoSystemReady", fmt.Sprintf(msg, 
            e.Ctx.BKECluster.Spec.ControlPlaneEndpoint.Host,
            constant.OpenFuyaoSystemPort,  // 31616
            username, passwd))
    }
}
```
## 二、openfuyao-system-controller如何进行组件的安装部署
### 1. 组件安装架构
```
┌─────────────────────────────────────────────────────────────────┐
│         openfuyao-system-controller 组件安装架构                 │
└─────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────┐
│                   Helm Chart Package                          │
│  https://helm.openfuyao.cn/_core                             │
│                                                               │
│  openfuyao-system-controller/                                │
│  ├── Chart.yaml                                              │
│  ├── values.yaml                                             │
│  └── templates/                                              │
│      ├── install-job.yaml        ◄── 安装Job                 │
│      ├── installer-service.yaml  ◄── 安装服务                │
│      ├── console-service.yaml    ◄── 控制台服务              │
│      ├── console-website.yaml    ◄── 控制台前端              │
│      ├── monitoring-service.yaml ◄── 监控服务                │
│      └── ...                                                  │
└──────────────────────────────────────────────────────────────┘
                         │
                         │ Helm Install
                         ▼
┌──────────────────────────────────────────────────────────────┐
│              目标集群 (Target Cluster)                        │
│                                                               │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  openfuyao-system-controller namespace                 │ │
│  │                                                         │ │
│  │  ┌──────────────────────────────────────────────────┐ │ │
│  │  │  install-job (Job)                                │ │ │
│  │  │  - 执行install.sh脚本                             │ │ │
│  │  │  - 安装管理面组件                                 │ │ │
│  │  └──────────────────────────────────────────────────┘ │ │
│  │                                                         │ │
│  │  ┌──────────────────────────────────────────────────┐ │ │
│  │  │  installer-service (Deployment)                   │ │ │
│  │  │  - 集群安装服务后端                               │ │ │
│  │  │  - 提供集群管理API                                │ │ │
│  │  └──────────────────────────────────────────────────┘ │ │
│  │                                                         │ │
│  │  ┌──────────────────────────────────────────────────┐ │ │
│  │  │  console-service (Deployment)                     │ │ │
│  │  │  - 控制台服务后端                                 │ │ │
│  │  │  - 认证、路由、代理                               │ │ │
│  │  └──────────────────────────────────────────────────┘ │ │
│  │                                                         │ │
│  │  ┌──────────────────────────────────────────────────┐ │ │
│  │  │  console-website (Deployment)                     │ │ │
│  │  │  - 控制台前端                                     │ │ │
│  │  │  - 用户界面                                       │ │ │
│  │  └──────────────────────────────────────────────────┘ │ │
│  │                                                         │ │
│  │  ┌──────────────────────────────────────────────────┐ │ │
│  │  │  monitoring-service (Deployment)                  │ │ │
│  │  │  - 监控服务                                       │ │ │
│  │  │  - Prometheus、AlertManager等                     │ │ │
│  │  └──────────────────────────────────────────────────┘ │ │
│  │                                                         │ │
│  └────────────────────────────────────────────────────────┘ │
│                                                               │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  openfuyao-system namespace                           │ │
│  │  - kube-prometheus                                     │ │
│  │  - ingress-nginx                                       │ │
│  │  - metrics-server                                      │ │
│  │  - oauth-webhook                                       │ │
│  └────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
```
### 2. 安装流程详解
#### 2.1 Helm Chart安装
**Chart配置来源**：
```go
// 从Product定义获取
addon := &confv1beta1.Product{
    Name:    "openfuyao-system-controller",
    Version: "latest",
    Type:    "chart",
    Param: map[string]string{
        "helmRepo":   "https://helm.openfuyao.cn/_core",
        "tagVersion": "latest",
    },
}
```
**Helm安装参数**：
```go
// 1. 获取Chart Values
values, err := c.getChartValues(addon, bkeClusterNS, localClient)
// 从ConfigMap读取values.yaml配置

// 2. 获取Chart包
chartFile, err := c.fetchChartPackage(addon, cfg, bkeClusterNS, localClient)
// 从Helm仓库下载Chart包

// 3. 执行安装
install := action.NewInstall(actionConfig)
install.ReleaseName = "openfuyao-system-controller"
install.Namespace = "openfuyao-system-controller"
install.Timeout = 30 * time.Minute
install.Wait = true
install.WaitForJobs = true

_, err = install.Run(chartFile, values)
```
#### 2.2 安装Job执行
**install-job.yaml**（推测结构）：
```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: openfuyao-system-controller-install
  namespace: openfuyao-system-controller
spec:
  template:
    spec:
      containers:
      - name: installer
        image: cr.openfuyao.cn/openfuyao/openfuyao-system-controller:latest
        command: ["/bin/sh", "-c"]
        args:
        - |
          # 执行安装脚本
          /scripts/install.sh
        env:
        - name: TARGET_NAMESPACE
          value: "openfuyao-system"
        - name: PATCH_CONFIG
          valueFrom:
            configMapKeyRef:
              name: patch-config
              key: patch-data
        volumeMounts:
        - name: resource
          mountPath: /resource
        - name: scripts
          mountPath: /scripts
      volumes:
      - name: resource
        configMap:
          name: openfuyao-resources
      - name: scripts
        configMap:
          name: install-scripts
      restartPolicy: OnFailure
```
#### 2.3 install.sh脚本执行流程
**安装脚本逻辑**（基于faq.md分析）：
```bash
#!/bin/bash

# install.sh - openFuyao管理面安装脚本

set -e

# 1. 环境检查
check_environment() {
    echo "Checking environment..."
    kubectl version --client
    helm version
}

# 2. 创建命名空间
create_namespaces() {
    echo "Creating namespaces..."
    kubectl create namespace openfuyao-system --dry-run=client -o yaml | kubectl apply -f -
    kubectl create namespace openfuyao-system-controller --dry-run=client -o yaml | kubectl apply -f -
}

# 3. 安装证书
install_certificates() {
    echo "Installing certificates..."
    # 创建TLS证书Secret
    kubectl create secret tls openfuyao-tls \
        --cert=/certs/tls.crt \
        --key=/certs/tls.key \
        -n openfuyao-system
}

# 4. 安装Ingress NGINX
install_ingress_nginx() {
    echo "Installing Ingress NGINX..."
    kubectl apply -f /resource/ingress-nginx/ingress-nginx.yaml
}

# 5. 安装Metrics Server
install_metrics_server() {
    echo "Installing Metrics Server..."
    kubectl apply -f /resource/metrics-server/metrics-server.yaml
}

# 6. 安装Kube Prometheus
install_kube_prometheus() {
    echo "Installing Kube Prometheus..."
    kubectl apply -f /resource/kube-prometheus/
}

# 7. 安装OAuth Webhook
install_oauth_webhook() {
    echo "Installing OAuth Webhook..."
    kubectl apply -f /resource/oauth-webhook/oauth-webhook.yaml
}

# 8. 安装installer-service
install_installer_service() {
    echo "Installing installer-service..."
    helm upgrade --install installer-service \
        https://helm.openfuyao.cn/_core/installer-service-${TAG_VERSION}.tgz \
        -n openfuyao-system-controller \
        -f /config/installer-values.yaml
}

# 9. 安装console-service
install_console_service() {
    echo "Installing console-service..."
    helm upgrade --install console-service \
        https://helm.openfuyao.cn/_core/console-service-${TAG_VERSION}.tgz \
        -n openfuyao-system-controller \
        -f /config/console-values.yaml
}

# 10. 安装console-website
install_console_website() {
    echo "Installing console-website..."
    helm upgrade --install console-website \
        https://helm.openfuyao.cn/_core/console-website-${TAG_VERSION}.tgz \
        -n openfuyao-system-controller \
        -f /config/website-values.yaml
}

# 11. 安装monitoring-service
install_monitoring_service() {
    echo "Installing monitoring-service..."
    helm upgrade --install monitoring-service \
        https://helm.openfuyao.cn/_core/monitoring-service-${TAG_VERSION}.tgz \
        -n openfuyao-system-controller
}

# 主安装流程
main() {
    echo "Starting openFuyao system installation..."
    
    check_environment
    create_namespaces
    install_certificates
    install_ingress_nginx
    install_metrics_server
    install_kube_prometheus
    install_oauth_webhook
    install_installer_service
    install_console_service
    install_console_website
    install_monitoring_service
    
    echo "openFuyao system installation completed!"
}

main "$@"
```
### 3. 组件依赖关系
```
┌──────────────────────────────────────────────────────────────┐
│                   组件安装依赖关系                            │
└──────────────────────────────────────────────────────────────┘
基础设施层:
    ├── Ingress NGINX (入口控制器)
    ├── Metrics Server (指标服务)
    └── Cert Manager (证书管理)

监控层:
    └── Kube Prometheus
        ├── Prometheus Operator
        ├── Prometheus
        ├── AlertManager
        ├── Node Exporter
        └── Kube State Metrics

认证层:
    └── OAuth Webhook (认证Webhook)

应用层:
    ├── installer-service (安装服务)
    │   └── 依赖: Ingress NGINX, Metrics Server
    │
    ├── console-service (控制台服务)
    │   └── 依赖: OAuth Webhook, Ingress NGINX
    │
    ├── console-website (控制台前端)
    │   └── 依赖: console-service, Ingress NGINX
    │
    └── monitoring-service (监控服务)
        └── 依赖: Kube Prometheus
```
### 4. 关键配置
#### 4.1 Patch ConfigMap
**作用**：存储版本特定的配置补丁
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: patch-config
  namespace: openfuyao-system-controller
data:
  patch-data: |
    # 版本特定的配置
    imageRegistry: cr.openfuyao.cn
    helmRepo: https://helm.openfuyao.cn/_core
    version: v1.0.0
    components:
      installer-service: v1.0.0
      console-service: v1.0.0
      console-website: v1.0.0
```
#### 4.2 Values配置
**installer-values.yaml**（示例）：
```yaml
image:
  repository: cr.openfuyao.cn/openfuyao/installer-service
  tag: v1.0.0

service:
  type: ClusterIP
  port: 8080

ingress:
  enabled: true
  host: installer.openfuyao.local

resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi
```
### 5. 安装验证
#### 5.1 健康检查
**位置**：[health.go:158-159](file:///d:/code/github/cluster-api-provider-bke/pkg/kube/health.go#L158-L159)
```go
{
    Namespace: "openfuyao-system-controller",
    Prefixes:  []string{"openfuyao-system-controller-"},
}
```
**检查逻辑**：
```go
func (c *Client) CheckAddonHealth(addon string) error {
    // 检查Pod状态
    pods, err := c.CoreV1().Pods("openfuyao-system-controller").List(ctx, metav1.ListOptions{
        LabelSelector: "app=openfuyao-system-controller",
    })
    
    // 验证所有Pod都处于Running状态
    for _, pod := range pods.Items {
        if pod.Status.Phase != corev1.PodRunning {
            return fmt.Errorf("pod %s is not running", pod.Name)
        }
    }
    
    return nil
}
```
#### 5.2 登录信息输出
安装完成后，会在日志中输出登录信息：
```
The website of the openFuyao is as follows:
    https://<cluster-endpoint>:31616

You can login to the openFuyao using the following username and password:
    username: admin
    password: <generated-password>
```
## 三、总结
### 1. cluster-api-provider-bke拉起openfuyao-system-controller的关键步骤
1. **Phase Flow触发**：EnsureAddonDeploy阶段识别到openfuyao-system-controller addon
2. **前置准备**：
   - 为Master节点添加control-plane标签
   - 下发Patch ConfigMap到目标集群
3. **Helm安装**：
   - 从Helm仓库下载Chart包
   - 读取values.yaml配置
   - 执行Helm Install
4. **后置操作**：
   - 生成默认用户名和密码
   - 输出登录信息
### 2. openfuyao-system-controller组件安装的关键机制
1. **Helm Chart包**：包含所有管理面组件的安装定义
2. **Install Job**：执行install.sh脚本，按序安装各组件
3. **组件分层**：
   - 基础设施层：Ingress、Metrics Server
   - 监控层：Kube Prometheus
   - 认证层：OAuth Webhook
   - 应用层：installer-service、console-service等
4. **配置管理**：通过Patch ConfigMap和values.yaml进行配置
### 3. 核心价值
- **自动化部署**：通过Helm Chart实现一键安装
- **声明式管理**：基于BKECluster CR定义期望状态
- **可观测性**：提供完善的日志和状态输出
- **安全性**：自动生成用户凭证，支持TLS加密
        
