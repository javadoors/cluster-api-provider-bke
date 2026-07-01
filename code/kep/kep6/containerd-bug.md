# `kep6-detailed-design.md` 中 containerd 重构点的问题审视

## `kep6-detailed-design.md` 中 containerd 重构点的问题审视

### 问题 1：config.toml 版本严重不一致

重构设计中 `configTemplates[0].content` 使用的是 **`version = 2`** 的旧版 TOML 格式（含 `io.containerd.grpc.v1.cri` 插件），但实际代码中嵌入的 [config.toml](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/containerruntime/containerd/config.toml) 已经是 **`version = 3`**，使用的是 `io.containerd.cri.v1.images` + `io.containerd.cri.v1.runtime` 的新版插件路径。

| 对比项 | 设计文档 (12.3.2) | 实际代码 config.toml |
|--------|-------------------|---------------------|
| version | `2` | `3` |
| CRI 插件路径 | `io.containerd.grpc.v1.cri` | `io.containerd.cri.v1.images` + `io.containerd.cri.v1.runtime` |
| sandbox_image 位置 | `plugins."io.containerd.grpc.v1.cri".sandbox_image` | `plugins.'io.containerd.cri.v1.images'.pinned_images.sandbox` |
| registry 配置 | `plugins."io.containerd.grpc.v1.cri".registry.mirrors` | `plugins.'io.containerd.cri.v1.images'.registry.config_path` |
| snapshotter 位置 | `plugins."io.containerd.grpc.v1.cri".containerd.snapshotter` | `plugins.'io.containerd.cri.v1.images'.snapshotter` |

**影响**：如果按设计文档的 config.toml 实施，会导致 containerd v2.1+ 无法正确读取配置，功能完全不可用。

---

### 问题 2：hosts.toml 配置完全缺失

当前代码中 hosts.toml 是 containerd 安装的**核心产物**，有两条生成路径：

- **Legacy 路径**：[containerd.go:99-153](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/containerruntime/containerd/containerd.go#L99-L153) `createHostsTOML()` — 为 repo + insecureRegistries 生成 hosts.toml
- **CR 驱动路径**：[hosts_toml.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/containerruntime/containerd/hosts_toml.go) — 从 `ContainerdConfig.Registry` 生成多 registry 的 hosts.toml

但重构设计的 `configTemplates` 中**完全没有 hosts.toml**。只有 `config.toml` 和 `containerd.service` 两个模板。

`containerd.md` 中虽然提到了可以用 `configTemplates` 实现 hosts.toml，但 12.3.2 节的正式 ComponentVersion YAML 定义中没有包含。这意味着重构后镜像仓库访问配置将丢失，容器无法拉取镜像。

---

### 问题 3：Service Drop-in（10-override.conf）能力丢失

当前代码支持通过 `ContainerdConfig` CR 的 `Service` 字段生成 systemd drop-in 文件 `/etc/systemd/system/containerd.service.d/10-override.conf`（[service.go](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/containerruntime/containerd/service.go)），可覆盖 ExecStart、Slice、KillMode、Restart、日志等。

重构设计中 `configTemplates[1]` 只是一个**静态的 containerd.service**，没有 drop-in 机制。用户无法：
- 修改 ExecStart（如添加 `--log-level debug` 或 `--config` 自定义路径）
- 设置 Slice 做资源隔离
- 调整日志输出目标
- 修改重启策略

---

### 问题 4：installPath 路径不一致

设计文档中 `artifacts[0].installPath: "/"` 但注释写 `解压到根目录`，而 [containerd.md](file:///d:/code/github/cluster-api-provider-bke/code/kep/kep6/containerd.md) 已指出实际二进制安装到 `/usr/bin/`（不是 `/usr/local/bin/`）。

同时 `containerd.service` 模板中 `ExecStart=/usr/local/bin/containerd`，但实际二进制路径是 `/usr/bin/containerd`（tar.gz 解压到 `/` 后二进制在 `/usr/bin/` 下）。

| 项目 | 设计文档 | 实际代码 |
|------|---------|---------|
| installPath | `/` | `/`（一致） |
| ExecStart 路径 | `/usr/local/bin/containerd` | `/usr/bin/containerd`（tar.gz 解压后） |
| chmod 目标 | `/usr/bin/containerd` | `/usr/bin/containerd`（一致） |

ExecStart 和 chmod 目标路径自相矛盾。

---

### 问题 5：config.toml 内容极度简化，丢失大量关键配置

设计文档中 config.toml 只有 10 行左右，而实际嵌入模板有 **288 行**，包含大量关键配置：

| 丢失的配置项 | 影响 |
|-------------|------|
| `root` / `state` 数据/状态目录 | 无法自定义数据目录 |
| `grpc.address` socket 路径 | 默认值不同可能导致 kubelet 连接失败 |
| `metrics.address` | 无法暴露 Prometheus 指标 |
| `runtimes.runc.options.SystemdCgroup = true` | **关键**：不设置会导致 cgroup 驱动不匹配，kubelet 无法启动 |
| `cni.bin_dirs` / `cni.conf_dir` | CNI 配置路径丢失，网络插件无法工作 |
| `task.platforms` | 架构平台声明丢失 |
| `gc.scheduler` | GC 调度参数丢失 |
| `snapshotter` 配置（overlayfs/native/devmapper 等） | 存储驱动配置丢失 |
| `stream_processors` (OCI 加密) | 镜像加密解密能力丢失 |
| `nri` 配置 | NRI 插件能力丢失 |

如果用设计文档中的简化 config.toml 替换现有 288 行模板，**集群将无法正常工作**。

---

### 问题 6：Script 执行能力丢失

当前 CR 驱动路径支持 `ContainerdConfig.Script`（[containerd.go:254-259](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/containerruntime/containerd/containerd.go#L254-L259)），允许用户在配置阶段执行自定义 shell 脚本。

重构设计中没有等价机制。`installScript` 是安装阶段执行，而 Script 是**配置阶段**执行（在 config.toml / hosts.toml / service 生成之前或之后），两者语义不同。

---

### 问题 7：containerd.service 来源矛盾

当前代码中 `containerd.service` 是 **tar.gz 内置**的（解压到 `/usr/lib/systemd/system/containerd.service`），而重构设计将其作为 `configTemplates[1]` 通过 ConfigRenderer 生成并上传到 `/etc/systemd/system/containerd.service`。

两个问题：
1. **路径不同**：tar.gz 内置放 `/usr/lib/systemd/system/`，设计文档放 `/etc/systemd/system/`。systemd 优先读 `/etc/`，可能导致两份 service 文件冲突
2. **installScript 中没有删除旧 service 的步骤**：如果 tar.gz 解压出 `/usr/lib/systemd/system/containerd.service`，同时 ConfigRenderer 写入 `/etc/systemd/system/containerd.service`，systemd 行为不可预测

---

### 问题 8：升级路径中 Reset 阶段缺失

当前升级是两步：`resetContainerd(scope=containerd-cfg)` → `redeployContainerd(scope=runtime)`。Reset 阶段会停止服务、删除配置、删除二进制，确保干净状态。

重构设计中 `installScript` 只做 `systemctl stop containerd || true`，没有清理旧配置。如果旧配置中有残留（如旧的 hosts.toml、旧的 drop-in），可能导致新配置与残留配置冲突。

---

### 问题 9：WaitContainerdReady 就绪检查缺失

当前代码在启动后调用 `econd.WaitContainerdReady()` 轮询等待 containerd 就绪（[containerd.go:379-393](file:///d:/code/github/cluster-api-provider-bke/pkg/job/builtin/containerruntime/containerd/containerd.go#L379-L393)）。

重构设计中 `installScript` 只做 `systemctl start containerd` + `/usr/bin/containerd --version`，没有等待 CRI API 就绪的检查。`healthCheck` 虽然定义了 `crictl info` 检查，但那是**安装后**的异步检查，不是安装流程中的同步等待。在安装脚本和后续环境初始化之间可能存在时序问题。

---

### 问题 10：离线模式 registry 重定向逻辑丢失

当前 Legacy 路径的 `createHostsTOML` 有完整的离线模式处理：当 `repoInsecure=true` 时，将公共仓库（docker.io、gcr.io 等）的请求重定向到私有仓库。CR 路径的 `hosts_toml.go` 也支持多 registry 配置。

重构设计中没有等价逻辑。即使补充 hosts.toml 到 configTemplates，也需要动态生成多个 registry 的 hosts.toml（数量取决于 insecureRegistries 列表），而 configTemplates 是**静态声明**的，无法根据运行时参数动态增减。

---

### 总结

| 严重程度 | 问题 | 根因 |
|---------|------|------|
| **P0-致命** | config.toml version=2 vs version=3 | 设计文档未同步代码中已升级的模板 |
| **P0-致命** | hosts.toml 完全缺失 | 遗漏了 registry 配置这一核心产物 |
| **P0-致命** | SystemdCgroup=true 等关键配置丢失 | config.toml 过度简化 |
| **P1-严重** | Service drop-in 能力丢失 | configTemplates 无法表达 drop-in 语义 |
| **P1-严重** | 离线模式重定向逻辑丢失 | 静态 configTemplates 无法动态生成多 registry |
| **P1-严重** | Script 执行能力丢失 | installScript 与配置阶段 Script 语义不同 |
| **P1-严重** | containerd.service 路径冲突 | tar.gz 内置 vs ConfigRenderer 生成路径不同 |
| **P2-中等** | installPath/ExecStart 路径不一致 | 设计文档未核实实际二进制路径 |
| **P2-中等** | 升级 Reset 阶段缺失 | installScript 未覆盖配置清理 |
| **P2-中等** | WaitContainerdReady 缺失 | 安装脚本缺少同步就绪检查 |

核心问题是：**设计文档用极度简化的示例替代了实际 288 行的 config.toml + 动态 hosts.toml 生成逻辑，且未同步代码中已完成的 containerd v2.1 (version=3) 升级**。重构设计如果要落地，必须以实际代码中的完整模板和 CR 驱动能力为基准，而非从零编写简化版。
