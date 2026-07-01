# 设计：通用 TemplateContext 扩展 + forEach 机制

## 设计：通用 TemplateContext 扩展 + forEach 机制

### 1. TemplateContext 扩展（通用，不绑定特定组件）

```go
type TemplateContext struct {
    // ... 现有字段保持不变 ...

    // 新增：组件级结构化数据 (通用, 任意组件可注入)
    // key: 组件名 (如 "containerd", "docker")
    // value: 该组件的结构化数据 (map/slice/任意类型)
    // 模板引用: {{index .ComponentData "containerd" "registry"}}
    ComponentData map[string]map[string]interface{}
}
```
**设计要点**：
- `ComponentData` 是 `map[string]map[string]interface{}`，第一层 key 为组件名，第二层为该组件的命名数据项
- 不引入任何组件特定类型，任何组件都可注入任意结构的 `interface{}` 数据
- Go template 通过 `index` 函数动态访问，天然支持嵌套结构

### 2. containerd 数据注入（两种场景）

#### 场景 A：在线（有 ContainerdConfig CR）

```
ContainerdConfig CR
  └── RegistryConfig
       ├── ConfigPath: "/etc/containerd/certs.d"
       └── Configs:
            ├── "harbor.example.com": {host, capabilities, tls, auth, ...}
            ├── "docker.io":          {host, skipVerify, ...}
            └── "ghcr.io":            {host, ...}
```

填充逻辑（`BinaryComponentExecutor` 或 `BinaryInstaller`）：
```go
func buildContainerdComponentData(containerdConfigRef *ContainerdConfigRef, bkeConfig *BkeConfig) map[string]interface{} {
    data := map[string]interface{}{
        "registryConfigPath": "/etc/containerd/certs.d",
        "sandboxImage":       fmt.Sprintf("%s/pause:3.9", bkeConfig.Cluster.ImageRepo.Domain),
        "root":               "/var/lib/containerd",
        "state":              "/run/containerd",
    }

    if containerdConfigRef != nil {
        spec, _ := plugin.GetContainerdConfig(...)

        // Registry
        if spec.Registry != nil {
            if spec.Registry.ConfigPath != "" {
                data["registryConfigPath"] = spec.Registry.ConfigPath
            }
            registryMap := make(map[string]interface{})
            for name, cfg := range spec.Registry.Configs {
                registryMap[name] = map[string]interface{}{
                    "host":         cfg.Host,
                    "capabilities": cfg.Capabilities,
                    "skipVerify":   cfg.SkipVerify,
                    "plainHTTP":    cfg.PlainHTTP,
                    "insecure":     cfg.Insecure,
                    "overridePath": cfg.OverridePath,
                    "tls":          convertTLS(cfg.TLS),
                    "auth":         convertAuth(cfg.Auth),
                    "header":       cfg.Header,
                }
            }
            data["registry"] = registryMap
        }

        // Main
        if spec.Main != nil {
            if spec.Main.SandboxImage != "" { data["sandboxImage"] = spec.Main.SandboxImage }
            if spec.Main.Root != ""         { data["root"] = spec.Main.Root }
            if spec.Main.State != ""        { data["state"] = spec.Main.State }
            if spec.Main.MetricsAddress != "" { data["metricsAddress"] = spec.Main.MetricsAddress }
        }
    }

    return data
}
```

#### 场景 B：离线（Legacy，无 ContainerdConfig CR）

从 `repo` + `insecureRegistries` 合成等价的 registry map：
```go
func buildLegacyContainerdData(bkeConfig *BkeConfig) map[string]interface{} {
    repo := bkeConfig.Cluster.ImageRepo.Domain
    registryMap := make(map[string]interface{})

    // 主仓库
    registryMap[repo] = map[string]interface{}{
        "host":         repo,
        "capabilities": []string{"pull", "resolve", "push"},
        "skipVerify":   true,
    }

    // insecureRegistries → 每个都生成一个 entry
    if insecure := bkeConfig.Cluster.InsecureRegistries; insecure != "" {
        for _, reg := range strings.Split(insecure, ",") {
            reg = strings.TrimSpace(reg)
            if reg == "" { continue }
            registryMap[reg] = map[string]interface{}{
                "host":         reg,
                "capabilities": []string{"pull", "resolve"},
                "skipVerify":   true,
                "plainHTTP":    true,
            }
        }
    }

    return map[string]interface{}{
        "registryConfigPath": "/etc/containerd/certs.d",
        "sandboxImage":       fmt.Sprintf("%s/pause:3.9", repo),
        "root":               "/var/lib/containerd",
        "state":              "/run/containerd",
        "registry":           registryMap,
    }
}
```
**两种场景产出相同的 `map[string]interface{}` 结构**，下游模板无感知。

### 3. forEach 机制设计

#### 3.1 ConfigTemplateSpec 扩展

```go
type ConfigTemplateSpec struct {
    Name         string `json:"name"`
    Path         string `json:"path,omitempty"`
    PathTemplate string `json:"pathTemplate,omitempty"`  // 🆕 动态路径
    ForEach      string `json:"forEach,omitempty"`       // 🆕 迭代源路径
    Mode         string `json:"mode,omitempty"`
    Owner        string `json:"owner,omitempty"`
    Content      string `json:"content,omitempty"`
    SecretRef    *SecretRefSpec `json:"secretRef,omitempty"`
    KubeconfigTemplate *KubeconfigTemplateSpec `json:"kubeconfigTemplate,omitempty"`
}
```

#### 3.2 forEach 语义

`forEach` 值为 **点分隔路径**，从 `TemplateContext` 中解析：

| forEach 值 | 解析路径 | 迭代类型 |
|------------|---------|---------|
| `"ComponentData.containerd.registry"` | `tmplCtx.ComponentData["containerd"]["registry"]` | `map[string]interface{}` → 按 key/value 迭代 |
| `"ComponentData.containerd.registryList"` | `tmplCtx.ComponentData["containerd"]["registryList"]` | `[]interface{}` → 按 index/value 迭代 |

#### 3.3 迭代上下文（ForEachContext）

每次迭代创建一个包装上下文，**同时保留全部 TemplateContext 变量 + 迭代变量**：
```go
// ForEachContext 包装 TemplateContext + 迭代变量
// Go template 通过反射访问字段, 嵌入的 TemplateContext 字段可直接用 .ClusterName 等访问
type ForEachContext struct {
    manifest.TemplateContext   // 嵌入: 保留所有现有变量
    Key   string              // 当前迭代 key (map) 或 index (slice, 转 string)
    Value interface{}          // 当前迭代值
}
```
模板变量访问规则：

| 变量 | 来源 | 示例 |
|------|------|------|
| `{{.ClusterName}}` | TemplateContext（嵌入） | `my-cluster` |
| `{{.NodeIP}}` | TemplateContext（嵌入） | `192.168.1.10` |
| `{{.imageRegistry}}` | TemplateContext（嵌入） | `registry.example.com` |
| `{{.Key}}` | ForEachContext（迭代） | `harbor.example.com` |
| `{{.Value}}` | ForEachContext（迭代） | `map[host:... capabilities:...]` |
| `{{index .Value "host"}}` | 动态访问 Value 内部字段 | `harbor.example.com` |
| `{{index .Value "capabilities"}}` | 动态访问 Value 内部字段 | `[pull resolve]` |

### 4. ComponentVersion YAML 示例

#### 4.1 在线场景（ContainerdConfig CR 提供完整 registry 配置）

```yaml
configTemplates:
  # --- 静态: config.toml ---
  - name: config.toml
    path: "/etc/containerd/config.toml"
    mode: "0644"
    content: |
      version = 2
      root = "{{index (index .ComponentData "containerd") "root"}}"
      state = "{{index (index .ComponentData "containerd") "state"}}"
      [plugins]
        [plugins."io.containerd.grpc.v1.cri"]
          sandbox_image = "{{index (index .ComponentData "containerd") "sandboxImage"}}"
          [plugins."io.containerd.grpc.v1.cri".registry]
            config_path = "{{index (index .ComponentData "containerd") "registryConfigPath"}}"
      {{- if index (index .ComponentData "containerd") "metricsAddress"}}
      [metrics]
        address = "{{index (index .ComponentData "containerd") "metricsAddress"}}"
      {{- end}}

  # --- 静态: containerd.service ---
  - name: containerd.service
    path: "/etc/systemd/system/containerd.service"
    mode: "0644"
    content: |
      [Unit]
      Description=containerd container runtime
      After=network.target
      [Service]
      ExecStart=/usr/local/bin/containerd
      Restart=always
      [Install]
      WantedBy=multi-user.target

  # --- 动态: hosts.toml (forEach 展开, 每个 registry 一个文件) ---
  - name: hosts.toml
    forEach: "ComponentData.containerd.registry"
    pathTemplate: "{{index (index .ComponentData "containerd") "registryConfigPath"}}/{{.Key}}/hosts.toml"
    mode: "0644"
    content: |
      server = "https://{{.Key}}"

      [host."https://{{index .Value "host"}}"]
        capabilities = {{index .Value "capabilities" | toJson}}
        skip_verify = {{index .Value "skipVerify"}}
        {{- if index .Value "plainHTTP"}}
        plain_http = true
        {{- end}}
        {{- if $tls := index .Value "tls"}}
        {{- if index $tls "caFile"}}
        ca = "{{index $tls "caFile"}}"
        {{- end}}
        {{- if index $tls "certFile"}}
        client = [["{{index $tls "certFile"}}", "{{index $tls "keyFile"}}"]]
        {{- end}}
        {{- end}}
        {{- if $auth := index .Value "auth"}}
        {{- if index $auth "auth"}}
        [host."https://{{index .Value "host"}}".header]
          authorization = ["Basic {{index $auth "auth"}}"]
        {{- end}}
        {{- end}}
```

#### 4.2 离线场景（Legacy，repo + insecureRegistries）

**同一个 ComponentVersion YAML 无需修改**，因为 `buildLegacyContainerdData()` 产出的 `registry` map 结构与在线一致。区别仅在数据注入侧：
```
在线 registry map:                          离线 registry map:
├── "harbor.example.com"                    ├── "cr.openfuyao.cn"
│   ├── host: "harbor.example.com"          │   ├── host: "cr.openfuyao.cn"
│   ├── capabilities: [pull, resolve]       │   ├── capabilities: [pull, resolve, push]
│   ├── tls: {caFile: /etc/ssl/ca.crt}      │   ├── skipVerify: true
│   └── auth: {auth: "dXNlcjpwYXNz"}        │   └── plainHTTP: false
├── "docker.io"                             ├── "docker.io"
│   ├── host: "mirror.internal"             │   ├── host: "docker.io"
│   ├── skipVerify: true                    │   ├── capabilities: [pull, resolve]
│   └── capabilities: [pull, resolve]       │   ├── skipVerify: true
                                            │   └── plainHTTP: true
生成 2 个 hosts.toml                        ├── "ghcr.io"
                                            │   ├── host: "ghcr.io"
                                            │   ├── capabilities: [pull, resolve]
                                            │   ├── skipVerify: true
                                            │   └── plainHTTP: true
                                            生成 3 个 hosts.toml
```

### 5. 渲染引擎核心代码

```go
// renderConfigTemplates 扩展: 支持 forEach 多文件展开
func (r *ConfigRenderer) renderConfigTemplates(
    ctx context.Context,
    templates []ConfigTemplateSpec,
    tmplCtx manifest.TemplateContext,
) (map[string][]byte, error) {
    configs := make(map[string][]byte)

    for _, tmpl := range templates {
        if tmpl.ForEach != "" {
            // 动态展开
            items, err := resolveForEach(tmpl.ForEach, tmplCtx)
            if err != nil {
                return nil, fmt.Errorf("forEach %q: %w", tmpl.ForEach, err)
            }
            for _, item := range items {
                // 构建迭代上下文: 保留全部 TemplateContext + 注入 Key/Value
                iterCtx := manifest.ForEachContext{
                    TemplateContext: tmplCtx,
                    Key:             item.Key,
                    Value:           item.Value,
                }

                // 渲染路径 (支持全部 TemplateContext 变量 + Key/Value)
                path, err := r.renderTemplateString(tmpl.PathTemplate, iterCtx)
                if err != nil {
                    return nil, fmt.Errorf("pathTemplate for key=%s: %w", item.Key, err)
                }

                // 渲染内容 (同上)
                content, err := r.renderContentTemplate(ctx, tmpl, iterCtx)
                if err != nil {
                    return nil, fmt.Errorf("content for key=%s: %w", item.Key, err)
                }

                configs[path] = content
            }
        } else {
            // 原有逻辑: 静态单文件
            content, err := r.RenderConfig(ctx, tmpl, tmplCtx)
            if err != nil {
                return nil, fmt.Errorf("template %s: %w", tmpl.Name, err)
            }
            configs[tmpl.Name] = content
        }
    }
    return configs, nil
}

// resolveForEach 按点分隔路径从 TemplateContext 中解析迭代源
func resolveForEach(path string, tmplCtx manifest.TemplateContext) ([]ForEachItem, error) {
    parts := strings.SplitN(path, ".", 2)
    if len(parts) != 2 {
        return nil, fmt.Errorf("invalid forEach path: %s", path)
    }

    // 第一层: 从 TemplateContext 顶层字段获取
    var source interface{}
    switch parts[0] {
    case "ComponentData":
        // 第二层: map key
        keys := strings.Split(parts[1], ".")
        if len(keys) < 2 {
            return nil, fmt.Errorf("ComponentData path too short: %s", path)
        }
        m := tmplCtx.ComponentData
        for i := 0; i < len(keys)-1; i++ {
            next, ok := m[keys[i]]
            if !ok {
                return nil, fmt.Errorf("key %q not found in ComponentData", keys[i])
            }
            if sub, ok := next.(map[string]interface{}); ok {
                if i == len(keys)-2 {
                    source = sub[keys[len(keys)-1]]
                } else {
                    m = sub
                }
            } else {
                return nil, fmt.Errorf("key %q is not a map", keys[i])
            }
        }
    default:
        return nil, fmt.Errorf("unsupported forEach root: %s", parts[0])
    }

    // 迭代: 支持 map 和 slice
    var items []ForEachItem
    switch v := source.(type) {
    case map[string]interface{}:
        for k, val := range v {
            items = append(items, ForEachItem{Key: k, Value: val})
        }
    case []interface{}:
        for i, val := range v {
            items = append(items, ForEachItem{Key: strconv.Itoa(i), Value: val})
        }
    default:
        return nil, fmt.Errorf("forEach source is not iterable: %T", source)
    }
    return items, nil
}

type ForEachItem struct {
    Key   string
    Value interface{}
}
```

### 6. 模板辅助函数（简化 index 嵌套语法）

当前 `index (index .ComponentData "containerd") "registry"` 语法冗长，建议在 `ConfigRenderer.funcMap` 中添加：
```go
funcMap := template.FuncMap{
    // 现有
    "upper":  strings.ToUpper,
    "lower":  strings.ToLower,
    "trim":   strings.TrimSpace,
    "toJson": funcJson,

    // 🆕 便捷函数: 按路径取值, 避免多层 index 嵌套
    // 用法: {{cd "containerd" "registry"}} 等价于 {{index (index .ComponentData "containerd") "registry"}}
    "cd": func(keys ...string) interface{} {
        return resolveNestedPath(tmplCtx.ComponentData, keys...)
    },
}
```
简化后的模板：
```yaml
content: |
  server = "https://{{.Key}}"
  [host."https://{{index .Value "host"}}"]
    capabilities = {{index .Value "capabilities" | toJson}}

pathTemplate: "{{cd "containerd" "registryConfigPath"}}/{{.Key}}/hosts.toml"
```

### 7. 完整数据流

```
┌─────────────────────────────────────────────────────────────────────┐
│                        在线场景                                      │
│  BKECluster.Spec.ClusterConfig.Cluster.ContainerdConfigRef          │
│       │                                                             │
│       ▼                                                             │
│  ContainerdConfig CR (API Server)                                   │
│       │                                                             │
│       ▼                                                             │
│  buildContainerdComponentData()                                     │
│       │  registry: {                                                │
│       │    "harbor.example.com": {host, capabilities, tls, auth},   │
│       │    "docker.io": {host, skipVerify}                          │
│       │  }                                                          │
└───────┼─────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────────┐
│                        离线场景 (Legacy)                             │
│  BKECluster.Spec.ClusterConfig.Cluster                              │
│       │  ImageRepo: "cr.openfuyao.cn"                               │
│       │  InsecureRegistries: "docker.io,ghcr.io"                    │
│       ▼                                                             │
│  buildLegacyContainerdData()                                        │
│       │  registry: {                                                │
│       │    "cr.openfuyao.cn": {host, capabilities:[pull,resolve,push]},│
│       │    "docker.io": {host, skipVerify, plainHTTP},              │
│       │    "ghcr.io": {host, skipVerify, plainHTTP}                 │
│       │  }                                                          │
└───────┼─────────────────────────────────────────────────────────────┘
        │
        ▼  两种场景产出相同的 map[string]interface{} 结构
┌─────────────────────────────────────────────────────────────────────┐
│  TemplateContext.ComponentData["containerd"] = data                 │
│       │                                                             │
│       ▼                                                             │
│  ConfigRenderer.renderConfigTemplates()                             │
│       │                                                             │
│       ├── 静态 template (无 forEach)                                 │
│       │   └── config.toml → /etc/containerd/config.toml             │
│       │   └── containerd.service → /etc/systemd/system/...          │
│       │                                                             │
│       └── 动态 template (forEach: "ComponentData.containerd.registry")│
│           ├── Key="harbor.example.com" → .../harbor.example.com/    │
│           │   └── hosts.toml (含 TLS/Auth)                          │
│           └── Key="docker.io" → .../docker.io/hosts.toml            │
│               └── hosts.toml (skipVerify=true)                      │
│                                                                     │
│  每次迭代: ForEachContext{TemplateContext + Key + Value}             │
│  pathTemplate/content 可访问:                                       │
│    - 全部 TemplateContext 变量 (.ClusterName, .NodeIP, ...)         │
│    - 迭代变量 (.Key, .Value, index .Value "field")                  │
│    - 辅助函数 (cd "containerd" "xxx")                               │
└─────────────────────────────────────────────────────────────────────┘
```

### 8. 关键设计决策总结

| 决策 | 选择 | 理由 |
|------|------|------|
| TemplateContext 扩展方式 | 新增通用 `ComponentData` 字段 | 不绑定特定组件，任意组件可注入 |
| 数据类型 | `map[string]map[string]interface{}` | 第一层按组件名隔离，第二层按语义分组 |
| forEach 迭代变量 | `ForEachContext` 嵌入 `TemplateContext` | 迭代中可访问全部变量，无信息丢失 |

| 在线/离线统一 | 两种场景产出相同 `interface{}` 结构 | 模板无感知，ComponentVersion YAML 通用 |
| 路径语法 | 点分隔路径 + `cd` 辅助函数 | 平衡通用性与可读性 |
| 迭代源类型 | 支持 `map` 和 `slice` | map 用于 registry（key 即仓库名），slice 用于有序列表 |
