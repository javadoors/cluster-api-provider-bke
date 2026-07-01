# containerd

## containerd 安装完整清单

### 一、二进制文件（从 tar.gz 解压到 `/`）

| 二进制 | 目标路径 | 来源 |
|--------|---------|------|
| `containerd` | `/usr/bin/containerd` | tar.gz 内置 |
| `containerd-shim-runc-v2` | `/usr/bin/containerd-shim-runc-v2` | tar.gz 内置 |
| `containerd-shim-shimless-v2` | `/usr/bin/containerd-shim-shimless-v2` | tar.gz 内置 (v2.1+) |
| `containerd-stress` | `/usr/bin/containerd-stress` | tar.gz 内置 |
| `ctr` | `/usr/bin/ctr` | tar.gz 内置 |
| `containerd-shim` | `/usr/bin/containerd-shim` | tar.gz 内置 (旧版本) |
| `runc` | `/usr/local/sbin/runc` | tar.gz 内置 |
| `crictl` | `/usr/bin/crictl` | tar.gz 内置 |
| `nerdctl` | `/usr/bin/nerdctl` | tar.gz 内置 |

**代码证据**：清理代码 `pkg/job/builtin/reset/clean.go:297-306` 列出了所有需删除的文件路径。

### 二、配置文件

| 文件 | 目标路径 | 生成方式 | 代码位置 |
|------|---------|---------|---------|
| `config.toml` | `/etc/containerd/config.toml` | 从嵌入模板生成 | `containerd/containerd.go:509-521` |
| `hosts.toml` (每个 registry) | `/etc/containerd/certs.d/{registry}/hosts.toml` | 代码生成（内联模板或 CR 模板） | `containerd/containerd.go:99-153` + `hosts_toml.go` |
| `10-override.conf` | `/etc/systemd/system/containerd.service.d/10-override.conf` | 从嵌入模板生成（可选，CR 驱动） | `containerd/service.go` + `service-dropin.tmpl` |
| `crictl.yaml` | `/etc/crictl.yaml` | tar.gz 内置（推测） | `clean.go:302` |

### 三、Systemd 服务

| 文件 | 目标路径 | 来源 |
|------|---------|------|
| `containerd.service` | `/usr/lib/systemd/system/containerd.service` | tar.gz 内置 |

安装后执行：
```bash
systemctl enable containerd
systemctl restart containerd
```
**代码位置**：`containerd/containerd.go:379-393`

### 四、目录

| 目录 | 用途 | 创建方式 |
|------|------|---------|
| `/var/lib/containerd` | 数据根目录 | 代码 `os.MkdirAll` 或 tar.gz |
| `/run/containerd` | 运行时状态目录 | containerd 启动时创建 |
| `/etc/containerd/` | 配置目录 | tar.gz 解压 config.toml 时创建 |
| `/etc/containerd/certs.d` | Registry 证书目录 | 代码 `os.MkdirAll` |
| `/etc/containerd/certs.d/{registry}` | 每个 registry 配置 | 代码 `os.MkdirAll` |
| `/etc/systemd/system/containerd.service.d` | Service drop-in 目录 | `service.go:57` |
| `/var/lib/nerdctl` | nerdctl 数据目录 | nerdctl 运行时创建 |

**默认数据根目录常量**：`common/cluster/initialize/defaults.go:78`
```go
DefaultCRIContainerdDataRootDir = "/var/lib/containerd"
```

### 五、Socket 文件

| Socket | 路径 | 代码位置 |
|--------|------|---------|
| containerd gRPC socket | `/var/run/containerd/containerd.sock` | `executor/containerd/containerd.go:80-82` |

config.toml 模板中也定义了：
```toml
address = '/run/containerd/containerd.sock'
```

### 六、完整安装流程

```
Phase 1: Reset (scope=containerd-cfg)
├── systemctl stop containerd
├── systemctl disable containerd
├── 删除二进制: /usr/bin/containerd* /usr/local/sbin/runc /usr/bin/crictl /usr/bin/nerdctl
├── 删除服务: /usr/lib/systemd/system/containerd.service
├── 删除配置: /etc/crictl.yaml /etc/containerd/
└── 删除目录: /usr/local/beyondvm

Phase 2: Redeploy (scope=runtime)
├── 下载 tar.gz → /tmp/containerd-{id}.tar.gz
├── 解压到 / → 安装二进制 + service + crictl.yaml
├── 生成 config.toml → /etc/containerd/config.toml
├── 生成 hosts.toml → /etc/containerd/certs.d/{registry}/hosts.toml
├── [可选] 生成 10-override.conf (CR 驱动)
├── systemctl enable containerd
├── systemctl restart containerd
└── WaitContainerdReady() 轮询等待就绪
```

### 七、关键源文件索引

| 用途 | 文件路径 |
|------|---------|
| 主安装插件 | `pkg/job/builtin/containerruntime/containerd/containerd.go` |
| 嵌入 config.toml 模板 | `pkg/job/builtin/containerruntime/containerd/config.toml` |
| Service drop-in 生成器 | `pkg/job/builtin/containerruntime/containerd/service.go` |
| Service drop-in 模板 | `pkg/job/builtin/containerruntime/containerd/service-dropin.tmpl` |
| Hosts.toml 生成器 (CR) | `pkg/job/builtin/containerruntime/containerd/hosts_toml.go` |
| Hosts.toml 模板 | `pkg/job/builtin/containerruntime/containerd/hosts.toml.tmpl` |
| Containerd 客户端/执行器 | `pkg/executor/containerd/containerd.go` |
| 升级 Phase | `pkg/phaseframe/phases/ensure_containerd_upgrade.go` |
| Reset/Redeploy 命令 | `pkg/command/env.go` |
| 运行时初始化 (downloadContainerd) | `pkg/job/builtin/kubeadm/env/init.go` |
| 清理/Reset 逻辑 | `pkg/job/builtin/reset/clean.go` |
| ContainerdConfig CRD 类型 | `api/bkecommon/v1beta1/containerdconfig_types.go` |
| 运行时检测 | `utils/bkeagent/runtime/runtime.go` |
| 默认常量 | `common/cluster/initialize/defaults.go` |
| tar.gz 名称模板 | `common/cluster/initialize/export.go` |
| 校验正则 | `common/cluster/validation/validation.go` |

## 对设计文档的影响

当前设计文档中 containerd 的 ComponentVersion YAML 示例（11.3.2 节）只列出了 **1 个 artifact**（`containerd`），但实际 tar.gz 解压后包含 **9 个二进制文件**。设计文档中的 `artifacts` 定义与实际安装内容有差距：

| 设计文档 artifacts | 实际安装 |
|-------------------|---------|
| `containerd` (installPath: `/usr/local`) | `containerd` → `/usr/bin/containerd` |
| — | `containerd-shim-runc-v2` → `/usr/bin/` |
| — | `containerd-shim-shimless-v2` → `/usr/bin/` |
| — | `containerd-stress` → `/usr/bin/` |
| — | `ctr` → `/usr/bin/` |
| — | `containerd-shim` → `/usr/bin/` |
| — | `runc` → `/usr/local/sbin/` |
| — | `crictl` → `/usr/bin/` |
| — | `nerdctl` → `/usr/bin/` |


另外，设计文档中 `installPath: "/usr/local"` 但实际安装到 `/usr/bin/`，路径不一致。

# func (cp *ContainerdPlugin) createHostsTOML(runtimeParam map[string]string)功能

## `createHostsTOML` 函数功能分析

### 核心功能
为 containerd 创建 `hosts.toml` 配置文件，用于配置镜像仓库的访问规则和重定向策略。

### 主要逻辑

#### 1. 参数解析
```go
repo := runtimeParam["repo"]              // 主仓库地址（私有仓库）
offline := runtimeParam["repoInsecure"]   // 是否离线模式
registries := []string{repo}              // 需要配置的仓库列表
```

#### 2. 离线模式处理
```go
if offline == "true" {
    // 获取公共仓库列表（如 docker.io, gcr.io 等）
    publicRegistries := strings.Split(runtimeParam["insecureRegistries"], ",")
    registries = append(registries, publicRegistries...)
}
```

#### 3. 为每个仓库创建配置
```go
for _, registry := range registries {
    // 创建目录：/etc/containerd/certs.d/{registry}/
    registryDir := filepath.Join(containerdCertsDir, registry)
    os.MkdirAll(registryDir, utils.RwxRxRx)
    
    // 创建 hosts.toml 文件
    hostsPath := filepath.Join(registryDir, "hosts.toml")
    // 写入配置内容
}
```

#### 4. 生成的 hosts.toml 内容
```toml
server = "https://{{.Registry}}"
[host."https://{{.Repo}}"]
  capabilities = ["pull", "resolve", "push"]
  skip_verify = true
```

### 实际效果

#### 在线模式
```
registry = "cr.openfuyao.cn"
offline = "false"

生成文件：/etc/containerd/certs.d/cr.openfuyao.cn/hosts.toml
内容：
  server = "https://cr.openfuyao.cn"
  [host."https://cr.openfuyao.cn"]
    capabilities = ["pull", "resolve", "push"]
    skip_verify = true
```

#### 离线模式
```
registry = "cr.openfuyao.cn"
offline = "true"
insecureRegistries = "docker.io,gcr.io,quay.io"

生成文件：
  /etc/containerd/certs.d/cr.openfuyao.cn/hosts.toml
  /etc/containerd/certs.d/docker.io/hosts.toml
  /etc/containerd/certs.d/gcr.io/hosts.toml
  /etc/containerd/certs.d/quay.io/hosts.toml

docker.io/hosts.toml 内容：
  server = "https://docker.io"
  [host."https://cr.openfuyao.cn"]    ← 重定向到私有仓库
    capabilities = ["pull", "resolve", "push"]
    skip_verify = true
```

### 应用场景

#### 场景 1：离线环境部署
在无法访问公网的环境中，将所有公共镜像仓库的请求重定向到私有仓库：
- 拉取 `docker.io/library/nginx:latest` → 实际从 `cr.openfuyao.cn/library/nginx:latest` 获取
- 需要提前将镜像同步到私有仓库

#### 场景 2：镜像加速
通过私有仓库缓存公共镜像，减少外网访问：
- 首次拉取从公网获取并缓存
- 后续拉取直接从私有仓库获取

#### 场景 3：安全合规
强制所有镜像拉取都经过私有仓库，便于：
- 镜像扫描和审计
- 访问控制和权限管理
- 镜像签名验证

### 与 KEP-6 的关系

在 KEP-6 的新设计中，这个功能应该通过 `configTemplates` 实现：
```yaml
configTemplates:
  - name: docker.io-hosts.toml
    path: "/etc/containerd/certs.d/docker.io/hosts.toml"
    mode: "0644"
    content: |
      server = "https://docker.io"
      [host."https://{{imageRegistry}}"]
        capabilities = ["pull", "resolve", "push"]
        skip_verify = true
```
这样可以：
1. 将配置声明式化
2. 支持模板变量（如 `{{imageRegistry}}`）
3. 与 BinaryInstaller 的 ConfigRenderer 集成
4. 避免硬编码逻辑

# func (cp *ContainerdPlugin) Execute(commands []string)的commands 是从哪里传进来的

## `ContainerdPlugin.Execute(commands []string)` 的调用链

### 完整调用链

```
Command CRD (Kubernetes API)
  └── Spec.Commands[].Command: []string
      ↓
CommandReconciler.startTask (controllers/bkeagent/command_controller.go:555)
  └── for _, execCommand := range command.Spec.Commands
      ↓
CommandReconciler.processExecCommand (line 502)
  └── r.executeWithRetry(execCommand, ...)
      ↓
CommandReconciler.executeWithRetry (line 465)
  └── r.executeByType(execCommand.Type, execCommand.Command)
      ↓
CommandReconciler.executeByType (line 445)
  └── case agentv1beta1.CommandBuiltIn:
        return r.Job.BuiltIn.Execute(command)
      ↓
BuiltIn.Execute (pkg/job/builtin/builtin.go:99)
  └── pluginRegistry[strings.ToLower(execCommands[0])].Execute(execCommands)
      ↓
ContainerdPlugin.Execute (pkg/job/builtin/containerruntime/containerd/containerd.go:295)
```

### 数据来源

**commands 参数来自 Command CRD 的 `spec.commands[].command` 字段**

#### Command CRD 结构

```yaml
apiVersion: bkeagent.bocloud.com/v1beta1
kind: Command
metadata:
  name: install-containerd-node1
  namespace: default
spec:
  nodeName: node1                    # 目标节点
  commands:
    - id: cmd1
      type: BuiltIn                  # 命令类型
      command:                       # ← 这就是传递给 Plugin.Execute 的 commands
        - "InstallContainerd"
        - "url=http://deploy.bocloud.k8s:40080/containerd-1.6.16-linux-amd64.tar.gz"
        - "sandbox=deploy.bocloud.k8s:40443/kubernetes/pause:3.5.1"
        - "repo=cr.openfuyao.cn"
    - id: cmd2
      type: BuiltIn
      command:
        - "K8sEnvInit"
        - "init=true"
        - "check=true"
```

### 命令解析流程

在 `ContainerdPlugin.Execute` 中（第 298 行）：
```go
runtimeParam, err := plugin.ParseCommands(cp, commands)
```

`ParseCommands` 会将 `commands` 数组解析为 `map[string]string`：
```go
// 输入
commands = [
    "InstallContainerd",
    "url=http://deploy.bocloud.k8s:40080/containerd-1.6.16-linux-amd64.tar.gz",
    "sandbox=deploy.bocloud.k8s:40443/kubernetes/pause:3.5.1",
    "repo=cr.openfuyao.cn"
]

// 输出
runtimeParam = {
    "url": "http://deploy.bocloud.k8s:40080/containerd-1.6.16-linux-amd64.tar.gz",
    "sandbox": "deploy.bocloud.k8s:40443/kubernetes/pause:3.5.1",
    "repo": "cr.openfuyao.cn"
}
```

### 谁创建 Command CRD？

Command CRD 由 **控制器（cluster-api-provider-bke）** 创建，用于向 bkeagent 下发命令。

**示例**：在 `ensure_nodes_env.go` 中创建 containerd 安装命令
```go
// pkg/command/env.go
func (e *ENV) buildContainerdCommand() []string {
    return []string{
        "InstallContainerd",
        fmt.Sprintf("url=%s", e.containerdURL),
        fmt.Sprintf("sandbox=%s", e.sandboxImage),
        fmt.Sprintf("repo=%s", e.imageRegistry),
    }
}

// 创建 Command CRD
command := &agentv1beta1.Command{
    Spec: agentv1beta1.CommandSpec{
        NodeName: node.Hostname,
        Commands: []agentv1beta1.ExecCommand{
            {
                ID:      "install-containerd",
                Type:    agentv1beta1.CommandBuiltIn,
                Command: e.buildContainerdCommand(),
            },
        },
    },
}
```

### 总结

| 层级 | 组件 | 职责 |
|------|------|------|
| **数据源** | Command CRD | 定义要执行的命令列表 |
| **创建者** | 控制器 (cluster-api-provider-bke) | 创建 Command CRD 下发给 bkeagent |
| **执行者** | bkeagent (CommandReconciler) | Watch Command CRD 并执行 |
| **路由器** | BuiltIn.Execute | 根据命令类型路由到对应 Plugin |
| **执行器** | ContainerdPlugin.Execute | 执行具体的 containerd 安装逻辑 |

# 根据运行时参数生成 containerd 的配置

该函数的作用是**根据运行时参数生成 containerd 的配置**，具体分四步：

1. **解析配置** — 从 `runtimeParam["containerdConfig"]` 中获取 `ContainerdConfig` 对象（`containerd.go:250`）
2. **执行脚本** — 如果配置中包含 `Script`，则执行 shell 脚本（`containerd.go:254-259`）
3. **生成 service override** — 如果配置中包含 `Service`，则生成 containerd.service 的覆盖配置（`containerd.go:260-265`）
4. **渲染 config.toml** — 如果配置中包含 `Main`，则渲染 containerd 主配置文件 `config.toml`（`containerd.go:266-271`）
5. **生成 hosts.toml** — 如果配置中包含 `Registry`，则生成镜像仓库的 `hosts.toml` 配置（`containerd.go:272-277`）

该函数由 `configureContainerd`（`containerd.go:340`）在 `containerdConfig` 参数非空时调用，是新版配置逻辑（替代 legacy 方式）。

# 
