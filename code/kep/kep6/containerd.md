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

需要我根据这些发现更新设计文档吗？
