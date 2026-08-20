# KEP-7: bkeadm 客户端限流优化

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-7 |
| **标题** | bkeadm Kubernetes 客户端限流参数优化 |
| **状态** | `provisional` |
| **类型** | Optimization |
| **作者** | openFuyao Team |
| **创建日期** | 2026-08-20 |
| **依赖** | bkeadm 组件、client-go |

## 1. 摘要

本提案优化 bkeadm 组件的 Kubernetes 客户端限流配置，解决 "Submit cluster-api yaml to the cluster" 阶段因 client-side throttling 导致的 8 分 37 秒等待问题。通过在 `NewKubernetesClient` 函数中设置 `QPS=50` 和 `Burst=100`，并复用 `RESTMapper` 减少 Discovery API 调用，预计将该阶段耗时从 8m37s 降至约 20 秒，整体集群安装时间提升约 40%。

## 2. 动机

### 2.1 现状痛点

| 问题 | 现状 | 影响 |
|------|------|------|
| **客户端限流未配置** | bkeadm 使用 client-go 默认值（QPS=5, Burst=10） | 66 节点集群安装时产生 8m37s 的 API 等待 |
| **RESTMapper 重复创建** | 每个 YAML 文件处理都重新创建 RESTMapper | 66 个 BKENode 文件触发 66 次 Discovery API 调用 |
| **性能瓶颈明显** | "Submit cluster-api yaml" 阶段占总耗时 40.5% | 严重影响大规模集群安装效率 |

### 2.2 目标

1. 将 bkeadm 的 Kubernetes 客户端 QPS 从默认值 5 提升至 50。
2. 将 Burst 从默认值 10 提升至 100。
3. 复用 RESTMapper，减少 Discovery API 调用次数。
4. 将 "Submit cluster-api yaml" 阶段耗时从 8m37s 降至约 20 秒。

### 2.3 非目标

1. 不修改 capbke 组件的客户端配置（已优化至 QPS=50, Burst=100）。
2. 不修改 installer-service 的客户端配置（已配置 QPS=1e6, Burst=1e6）。
3. 不引入新的客户端配置管理框架。

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
|------|------|
| 客户端初始化 | 修改 `NewKubernetesClient` 函数，添加 QPS/Burst 配置 |
| RESTMapper 复用 | 将 RESTMapper 提升为 Client 结构体成员，避免重复创建 |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **向后兼容** | 不改变 API 接口，仅优化内部实现 |
| **低风险** | 仅修改客户端初始化逻辑，不影响业务逻辑 |

## 4. 问题分析

### 4.1 性能日志分析

基于 `bke-cluster-create2.log` 的分析（66 节点集群安装）：

| 阶段 | 开始时间 | 结束时间 | 耗时 | 占比 |
|------|---------|---------|------|------|
| **Submit cluster-api yaml + API 限流** | 18:11:21 | 18:19:58 | **8m37s** | **40.5%** |
| BKEAgent 推送 (66 节点) | 18:20:01 | 18:21:42 | 1m41s | 7.9% |
| 节点环境初始化 (66 节点) | 18:22:09 | 18:25:09 | 3m0s | 14.1% |
| 其他阶段 | - | - | ~8m | 37.5% |
| **总计** | 18:11:21 | 18:32:36 | **21m15s** | 100% |

### 4.2 限流日志证据

```
18:11:21 - Submit cluster-api yaml to the cluster
18:11:23 - Waited for 1.1973968s due to client-side throttling, not priority and fairness
18:11:33 - Waited for 2.79670416s due to client-side throttling, not priority and fairness
18:11:43 - Waited for 4.19752324s due to client-side throttling, not priority and fairness
18:11:54 - Waited for 5.79745005s due to client-side throttling, not priority and fairness
18:12:04 - Waited for 7.39765425s due to client-side throttling, not priority and fairness
...（重复约 50 次）
18:19:58 - Submit the configuration to the cluster (1 BKECluster + 66 BKENodes)
```

### 4.3 根因分析

**问题代码**：`bkeadm/pkg/executor/k8s/k8s.go` 第 69-104 行

```go
func NewKubernetesClient(kubeConfig string) (KubernetesClient, error) {
    var config *rest.Config
    var err error

    if kubeConfig == "" {
        if home := homedir.HomeDir(); home != "" {
            kubeConfig = filepath.Join(home, ".kube", "config")
        }
    }
    if utils.Exists(kubeConfig) {
        config, err = clientcmd.BuildConfigFromFlags("", kubeConfig)
    }
    if err != nil {
        return nil, err
    }
    if config == nil {
        return nil, errors.New("The kube config configuration file does not exist. ")
    }

    // ❌ 未设置 QPS/Burst，使用 client-go 默认值（QPS=5, Burst=10）
    clientSet, err := kubernetes.NewForConfig(config)
    if err != nil {
        log.Infof("Failed to initialize kubernetes clientset")
        return nil, err
    }

    dynamicClient, err := dynamic.NewForConfig(config)
    if err != nil {
        log.Infof("Failed to initialize kubernetes dynamic client")
        return nil, err
    }

    return &Client{
        ClientSet:     clientSet,
        DynamicClient: dynamicClient,
    }, nil
}
```

**对比 capbke 的优化实现**：`cluster-api-provider-bke/pkg/kube/client_factory.go`

```go
func ApplyThrottlingConfig(cfg *rest.Config) *rest.Config {
    if cfg == nil {
        return nil
    }
    cfg.QPS = config.ClientQPS      // 50
    cfg.Burst = config.ClientBurst  // 100
    return cfg
}

func newKubernetesClientForConfig(cfg *rest.Config) (*kubernetes.Clientset, error) {
    return kubernetes.NewForConfig(ApplyThrottlingConfig(cfg))
}
```

### 4.4 RESTMapper 重复创建问题

**问题代码**：`bkeadm/pkg/executor/k8s/k8s.go` 第 150-199 行

```go
func (c *Client) processYamlResources(filepath string, handler yamlResourceHandler) error {
    f, err := os.Open(filepath)
    if err != nil {
        return err
    }
    defer f.Close()

    decoder := yamlutil.NewYAMLOrJSONDecoder(f, yamlDecoderBufferSize)
    
    // ❌ 每次调用都重新创建 RESTMapper，触发多次 Discovery API 调用
    dc := c.ClientSet.Discovery()
    restMapperRes, err := restmapper.GetAPIGroupResources(dc)
    if err != nil {
        return err
    }
    restMapper := restmapper.NewDiscoveryRESTMapper(restMapperRes)

    for {
        var rawObj runtime.RawExtension
        if err = decoder.Decode(&rawObj); err != nil {
            if err == io.EOF {
                break
            }
            return err
        }
        // ... 处理资源
    }
    return nil
}
```

**影响**：66 个 BKENode 文件 = 66 次 `processYamlResources` 调用 = 66 次 Discovery API 调用。

## 5. 提案设计

### 5.1 改动 1：新增配置层 — 集中化 QPS/Burst 配置

**文件**：`bkeadm/pkg/config/config.go`（新文件）

参考 capbke 的实现（`cluster-api-provider-bke/utils/capbke/config/config.go`），为 bkeadm 新增独立的配置模块，支持多级配置优先级。

**配置优先级**：

```txt
命令行标志 > 环境变量 > 配置文件 > 默认值

示例：
1. 命令行：--client-qps=100 --client-burst=200
2. 环境变量：KUBE_CLIENT_QPS=80 KUBE_CLIENT_BURST=160
3. 配置文件：/etc/bke/client-config.yaml (qps: 60, burst: 120)
4. 默认值：QPS=50, Burst=100
```

**代码实现**：

```go
// bkeadm/pkg/config/config.go

package config

import (
    "flag"
    "os"
    "strconv"

    "gopkg.openfuyao.cn/bkeadm/utils/log"
    "gopkg.in/yaml.v3"
)

var (
    // ClientQPS 是 Kubernetes 客户端的 QPS
    // 默认值: 50，可以通过命令行标志、环境变量或配置文件覆盖
    ClientQPS float32

    // ClientBurst 是 Kubernetes 客户端的突发大小
    // 默认值: 100，可以通过命令行标志、环境变量或配置文件覆盖
    ClientBurst int

    // ClientConfigFile 是客户端配置文件路径
    // 默认值: /etc/bke/client-config.yaml
    ClientConfigFile string
)

const (
    // DefaultClientQPS 是 Kubernetes 客户端的默认 QPS
    DefaultClientQPS = 50
    // DefaultClientBurst 是 Kubernetes 客户端的默认突发大小
    DefaultClientBurst = 100
    // DefaultClientConfigFile 是默认配置文件路径
    DefaultClientConfigFile = "/etc/bke/client-config.yaml"
)

// ClientConfig 客户端配置文件结构
type ClientConfig struct {
    QPS   float32 `yaml:"qps"`
    Burst int     `yaml:"burst"`
}

// RegisterFlags 注册命令行标志
func RegisterFlags() {
    flag.Float32Var(&ClientQPS, "client-qps", 0,
        "Kubernetes 客户端的 QPS。优先级：命令行 > 环境变量 > 配置文件 > 默认值(50)")
    flag.IntVar(&ClientBurst, "client-burst", 0,
        "Kubernetes 客户端的突发大小。优先级：命令行 > 环境变量 > 配置文件 > 默认值(100)")
    flag.StringVar(&ClientConfigFile, "client-config-file", DefaultClientConfigFile,
        "客户端配置文件路径。默认: /etc/bke/client-config.yaml")
}

// ResolveClientConfig 解析配置，按优先级确定最终值
// 调用时机：命令行标志解析完成后
func ResolveClientConfig() {
    // 1. 首先加载配置文件（最低优先级）
    loadClientConfigFile()

    // 2. 环境变量覆盖配置文件
    if qps := os.Getenv("KUBE_CLIENT_QPS"); qps != "" {
        if v, err := strconv.ParseFloat(qps, 32); err == nil {
            ClientQPS = float32(v)
        }
    }
    if burst := os.Getenv("KUBE_CLIENT_BURST"); burst != "" {
        if v, err := strconv.Atoi(burst); err == nil {
            ClientBurst = v
        }
    }

    // 3. 如果仍未设置，使用默认值
    if ClientQPS == 0 {
        ClientQPS = DefaultClientQPS
    }
    if ClientBurst == 0 {
        ClientBurst = DefaultClientBurst
    }

    log.Infof("resolved Kubernetes client throttling config: qps=%v, burst=%d, configFile=%s",
        ClientQPS, ClientBurst, ClientConfigFile)
}

// loadClientConfigFile 从配置文件加载 QPS/Burst 配置
func loadClientConfigFile() {
    if ClientConfigFile == "" {
        ClientConfigFile = DefaultClientConfigFile
    }

    data, err := os.ReadFile(ClientConfigFile)
    if err != nil {
        // 配置文件不存在或读取失败，使用默认值
        return
    }

    var config ClientConfig
    if err := yaml.Unmarshal(data, &config); err != nil {
        log.Warnf("failed to parse client config file %s: %v, using defaults", ClientConfigFile, err)
        return
    }

    if config.QPS > 0 {
        ClientQPS = config.QPS
    }
    if config.Burst > 0 {
        ClientBurst = config.Burst
    }
}
```

**配置文件格式**：`/etc/bke/client-config.yaml`

```yaml
# BKE 客户端配置
# 通过 ConfigMap 挂载到 Pod

# Kubernetes 客户端限流配置
qps: 50
burst: 100
```

**ConfigMap 定义**：

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: bke-client-config
  namespace: bke-system
data:
  client-config.yaml: |
    # BKE 客户端配置
    qps: 50
    burst: 100
```

**Deployment 挂载配置**：

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: bke-controller-manager
  namespace: bke-system
spec:
  template:
    spec:
      containers:
      - name: manager
        args:
        - --client-config-file=/etc/bke/client-config.yaml
        volumeMounts:
        - name: client-config
          mountPath: /etc/bke
          readOnly: true
      volumes:
      - name: client-config
        configMap:
          name: bke-client-config
```

### 5.2 改动 2：NewKubernetesClient 使用可配置 QPS/Burst

**文件**：`bkeadm/pkg/executor/k8s/k8s.go`

**修改位置**：第 86-87 行之间（`config` 获取成功后、创建 client 之前）

```go
// 当前代码（第 84-88 行）：
if config == nil {
    return nil, errors.New("The kube config configuration file does not exist. ")
}

clientSet, err := kubernetes.NewForConfig(config)

// 修改为：
if config == nil {
    return nil, errors.New("The kube config configuration file does not exist. ")
}

// 设置客户端限流参数，避免 API 调用被 client-side throttling 阻塞
// 使用集中化配置，支持命令行标志、环境变量、配置文件覆盖
config.QPS = bkeconfig.ClientQPS
config.Burst = bkeconfig.ClientBurst

clientSet, err := kubernetes.NewForConfig(config)
```

### 5.3 改动 3：Client 结构体添加 restMapper 成员

**文件**：`bkeadm/pkg/executor/k8s/k8s.go`

**修改位置**：第 59-62 行

```go
// 当前代码：
type Client struct {
    ClientSet     *kubernetes.Clientset
    DynamicClient dynamic.Interface
}

// 修改为：
type Client struct {
    ClientSet     *kubernetes.Clientset
    DynamicClient dynamic.Interface
    restMapper    meta.RESTMapper  // 缓存 RESTMapper，避免重复 Discovery
}
```

### 5.4 改动 4：NewKubernetesClient 初始化 restMapper

**文件**：`bkeadm/pkg/executor/k8s/k8s.go`

**修改位置**：第 100-103 行

```go
// 当前代码：
return &Client{
    ClientSet:     clientSet,
    DynamicClient: dynamicClient,
}, nil

// 修改为：
restMapperRes, err := restmapper.GetAPIGroupResources(clientSet.Discovery())
if err != nil {
    return nil, err
}

return &Client{
    ClientSet:     clientSet,
    DynamicClient: dynamicClient,
    restMapper:    restmapper.NewDiscoveryRESTMapper(restMapperRes),
}, nil
```

### 5.5 改动 5：processYamlResources 复用 restMapper

**文件**：`bkeadm/pkg/executor/k8s/k8s.go`

**修改位置**：第 162-168 行

```go
// 当前代码：
dc := c.ClientSet.Discovery()
restMapperRes, err := restmapper.GetAPIGroupResources(dc)
if err != nil {
    return err
}
restMapper := restmapper.NewDiscoveryRESTMapper(restMapperRes)

// 修改为：
restMapper := c.restMapper
```

### 5.6 改动 6：main.go 集成配置解析

**文件**：`bkeadm/main.go`

**修改位置**：`main()` 函数中，在 `cmd.Execute()` 之前

```go
// 当前代码：
func main() {
    if version.Version == "" {
        version.GitCommitID = gitCommitId
        version.Version = ver
        version.Architecture = architecture
        version.Timestamp = timestamp
    }
    cmd.Execute()
}

// 修改为：
func main() {
    if version.Version == "" {
        version.GitCommitID = gitCommitId
        version.Version = ver
        version.Architecture = architecture
        version.Timestamp = timestamp
    }

    // 新增：注册并解析客户端限流配置
    bkeconfig.RegisterFlags()
    flag.Parse()
    bkeconfig.ResolveClientConfig()

    cmd.Execute()
}
```

### 5.7 完整修改后代码

```go
// bkeadm/pkg/executor/k8s/k8s.go

import (
    bkeconfig "gopkg.openfuyao.cn/bkeadm/pkg/config"
    // ... 其他导入
)

type Client struct {
    ClientSet     *kubernetes.Clientset
    DynamicClient dynamic.Interface
    restMapper    meta.RESTMapper  // 新增：缓存 RESTMapper
}

func NewKubernetesClient(kubeConfig string) (KubernetesClient, error) {
    var config *rest.Config
    var err error

    if kubeConfig == "" {
        if home := homedir.HomeDir(); home != "" {
            kubeConfig = filepath.Join(home, ".kube", "config")
        }
    }
    if utils.Exists(kubeConfig) {
        config, err = clientcmd.BuildConfigFromFlags("", kubeConfig)
    }
    if err != nil {
        return nil, err
    }
    if config == nil {
        return nil, errors.New("The kube config configuration file does not exist. ")
    }

    // 新增：使用集中化配置设置客户端限流参数
    config.QPS = bkeconfig.ClientQPS
    config.Burst = bkeconfig.ClientBurst

    clientSet, err := kubernetes.NewForConfig(config)
    if err != nil {
        log.Infof("Failed to initialize kubernetes clientset")
        return nil, err
    }

    dynamicClient, err := dynamic.NewForConfig(config)
    if err != nil {
        log.Infof("Failed to initialize kubernetes dynamic client")
        return nil, err
    }

    // 新增：初始化并缓存 RESTMapper
    restMapperRes, err := restmapper.GetAPIGroupResources(clientSet.Discovery())
    if err != nil {
        return nil, err
    }

    return &Client{
        ClientSet:     clientSet,
        DynamicClient: dynamicClient,
        restMapper:    restmapper.NewDiscoveryRESTMapper(restMapperRes),
    }, nil
}

func (c *Client) processYamlResources(filepath string, handler yamlResourceHandler) error {
    f, err := os.Open(filepath)
    if err != nil {
        return err
    }
    defer func() {
        if err := f.Close(); err != nil {
            log.Warnf("failed to close file %s: %v", filepath, err)
        }
    }()

    decoder := yamlutil.NewYAMLOrJSONDecoder(f, yamlDecoderBufferSize)
    
    // 修改：复用缓存的 RESTMapper
    restMapper := c.restMapper

    for {
        var rawObj runtime.RawExtension
        if err = decoder.Decode(&rawObj); err != nil {
            if err == io.EOF {
                break
            }
            return err
        }

        obj, gvk, err := unstructured.UnstructuredJSONScheme.Decode(rawObj.Raw, nil, nil)
        if err != nil {
            return err
        }
        mapping, err := restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
        if err != nil {
            return err
        }

        unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
        if err != nil {
            return err
        }
        unstruct := &unstructured.Unstructured{Object: unstructuredObj}

        if err = handler(unstruct, mapping); err != nil {
            return err
        }
    }
    return nil
}
```

## 6. 优化效果预估

| 优化项 | 减少的 API 等待 | 预估节省时间 |
|--------|---------------|-------------|
| QPS/Burst 提升（5→50） | 67 个资源创建请求的限流等待从 ~13s/请求 降至 ~0.02s/请求 | **~8 分钟** |
| RESTMapper 复用 | 减少 65 次 Discovery API 调用（66→1） | **~30 秒** |
| **合计** | | **~8.5 分钟** |

**优化后预期**：
- "Submit cluster-api yaml" 阶段从 **8m37s** 降至 **~20s**
- 整体集群安装时间从 **21m15s** 降至 **~12m30s**
- 性能提升约 **40%**

## 7. 测试计划

### 7.1 单元测试

| 测试场景 | 验证内容 |
|---------|---------|
| NewKubernetesClient 初始化 | 验证 QPS/Burst 已从配置读取并设置 |
| RESTMapper 复用 | 验证多次 processYamlResources 调用只触发 1 次 Discovery |
| 配置优先级 - 默认值 | 验证无配置时使用默认值 QPS=50, Burst=100 |
| 配置优先级 - 环境变量 | 验证环境变量覆盖默认值 |
| 配置优先级 - 配置文件 | 验证配置文件覆盖默认值 |
| 配置优先级 - 命令行标志 | 验证命令行标志覆盖其他配置 |
| 配置文件不存在 | 验证配置文件不存在时使用默认值，无报错 |
| 配置文件格式错误 | 验证配置文件格式错误时降级到默认值，输出警告日志 |

**配置测试代码示例**：

```go
// bkeadm/pkg/config/config_test.go

func TestResolveClientConfig_Defaults(t *testing.T) {
    // 清理环境变量
    os.Unsetenv("KUBE_CLIENT_QPS")
    os.Unsetenv("KUBE_CLIENT_BURST")
    ClientConfigFile = "/nonexistent/path"

    ResolveClientConfig()

    assert.Equal(t, float32(50), ClientQPS)
    assert.Equal(t, 100, ClientBurst)
}

func TestResolveClientConfig_EnvOverride(t *testing.T) {
    os.Setenv("KUBE_CLIENT_QPS", "80")
    os.Setenv("KUBE_CLIENT_BURST", "160")
    defer os.Unsetenv("KUBE_CLIENT_QPS")
    defer os.Unsetenv("KUBE_CLIENT_BURST")

    ResolveClientConfig()

    assert.Equal(t, float32(80), ClientQPS)
    assert.Equal(t, 160, ClientBurst)
}

func TestResolveClientConfig_FileOverride(t *testing.T) {
    // 创建临时配置文件
    tmpFile, _ := os.CreateTemp("", "client-config-*.yaml")
    defer os.Remove(tmpFile.Name())
    tmpFile.Write([]byte("qps: 60\nburst: 120\n"))
    tmpFile.Close()

    os.Unsetenv("KUBE_CLIENT_QPS")
    os.Unsetenv("KUBE_CLIENT_BURST")
    ClientConfigFile = tmpFile.Name()

    ResolveClientConfig()

    assert.Equal(t, float32(60), ClientQPS)
    assert.Equal(t, 120, ClientBurst)
}

func TestResolveClientConfig_CLIOverridesAll(t *testing.T) {
    // 创建临时配置文件
    tmpFile, _ := os.CreateTemp("", "client-config-*.yaml")
    defer os.Remove(tmpFile.Name())
    tmpFile.Write([]byte("qps: 60\nburst: 120\n"))
    tmpFile.Close()

    os.Setenv("KUBE_CLIENT_QPS", "80")
    os.Setenv("KUBE_CLIENT_BURST", "160")
    defer os.Unsetenv("KUBE_CLIENT_QPS")
    defer os.Unsetenv("KUBE_CLIENT_BURST")

    ClientConfigFile = tmpFile.Name()
    // 模拟命令行标志已解析
    ClientQPS = 100
    ClientBurst = 200

    ResolveClientConfig()

    // 命令行标志优先级最高
    assert.Equal(t, float32(100), ClientQPS)
    assert.Equal(t, 200, ClientBurst)
}
```

### 7.2 集成测试

| 测试场景 | 验证内容 |
|---------|---------|
| 66 节点集群安装 | 验证 "Submit cluster-api yaml" 阶段耗时 < 30s |
| 大规模集群安装（100+ 节点） | 验证无 API 限流日志 |

### 7.3 性能测试

| 测试指标 | 优化前 | 优化后目标 |
|---------|-------|-----------|
| "Submit cluster-api yaml" 耗时 | 8m37s | < 30s |
| 整体安装时间（66 节点） | 21m15s | < 13m |
| API 限流日志数量 | ~50 条 | 0 条 |

## 8. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| QPS/Burst 过高导致 API Server 压力 | API Server 负载增加 | 默认值 QPS=50, Burst=100 是经过验证的安全值，与 capbke 保持一致 |
| 配置文件格式错误 | 配置加载失败 | 配置文件解析失败时降级到默认值，输出警告日志，不影响启动 |
| 环境变量配置错误 | 配置值异常 | 环境变量解析失败时忽略该值，继续使用其他配置来源 |
| RESTMapper 缓存过期 | 新创建的 CRD 无法被发现 | 当前场景下 CRD 在客户端初始化前已存在，无风险 |
| 配置优先级冲突 | 配置值不符合预期 | 启动时打印最终生效的配置值及来源，便于排查 |

## 9. 工作量评估

| 阶段 | 任务内容 | 工作量 |
|------|---------|--------|
| 配置层 | 新增 `bkeadm/pkg/config/config.go`，实现多级配置优先级 | 0.5 人天 |
| 代码修改 | 修改 `NewKubernetesClient` 和 `processYamlResources` | 0.5 人天 |
| main.go 集成 | 修改 `main.go` 集成配置解析 | 0.25 人天 |
| 测试验证 | 单元测试 + 集成测试 + 性能测试 | 1 人天 |
| 文档更新 | 更新性能分析报告 | 0.25 人天 |
| **总计** | | **2.5 人天** |

## 10. 相关文件

| 文件路径 | 说明 |
|---------|------|
| `bkeadm/pkg/config/config.go` | 新增：集中化 QPS/Burst 配置模块 |
| `bkeadm/pkg/executor/k8s/k8s.go` | 主要修改文件 |
| `bkeadm/main.go` | 修改：集成配置解析 |
| `cluster-api-provider-bke/pkg/kube/client_factory.go` | capbke 的参考实现 |
| `cluster-api-provider-bke/utils/capbke/config/config.go` | capbke 的 QPS/Burst 配置定义 |
| `code/performance/report/api-throttling-optimization.md` | capbke 限流优化方案参考 |
| `code/performance/report2/bke-cluster-create2.log` | 性能分析日志 |
| `code/performance/report2/analysis.md` | 性能分析报告 |

---

**文档版本**: v1.1  
**维护者**: openFuyao Team