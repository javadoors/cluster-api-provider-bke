# containerruntime

是的，除了 containerd，现有代码还支持 **Docker**（含 cri-dockerd）。共 3 个容器运行时插件：

## 支持的容器运行时

| 运行时 | 插件 | 路径 | 常量 |
|--------|------|------|------|
| **containerd** | `ContainerdPlugin` | `pkg/job/builtin/containerruntime/containerd/` | `CRIContainerd = "containerd"` |
| **docker** | `DockerPlugin` | `pkg/job/builtin/containerruntime/docker/` | `CRIDocker = "docker"` |
| **cri-dockerd** | `CRIDockerPlugin` | `pkg/job/builtin/containerruntime/cridocker/` | （Docker 的 CRI 适配层，K8s ≥1.24 时随 Docker 一起安装） |

## 选择逻辑

在 `init.go:789-797`（`downloadContainerRuntime`）中按 `BKECluster.Spec.Cluster.ContainerRuntime.CRI` 分发：
```go
switch containerRuntime {
case runtime.ContainerRuntimeContainerd:
    // 调用 ContainerdPlugin — 下载 containerd tar.gz + 生成 config.toml + hosts.toml
case runtime.ContainerRuntimeDocker:
    // 调用 DockerPlugin — 下载 docker 二进制 + 生成 daemon.json
    // 然后调用 CRIDockerPlugin — 下载 cri-dockerd（K8s ≥1.24 需要）
default:
    return errors.Errorf("unsupported container runtime type %s", containerRuntime)
}
```
默认值在 `defaults.go:288`：`obj.ContainerRuntime.CRI = CRIContainerd`（默认 containerd）。

## Docker 与 containerd 的差异

| 对比项 | containerd | docker |
|--------|-----------|--------|
| 配置文件 | `config.toml` + `hosts.toml`（多 registry） | `daemon.json` |
| hosts.toml | 有（离线重定向） | 无（docker 的 registry 镜像配置在 daemon.json 的 `registry-mirrors` 中） |
| CRI 适配 | 原生 CRI | 需 cri-dockerd（K8s ≥1.24） |
| `ContainerdConfigRef` | 可选（CR 驱动高级配置） | 不适用 |

## KEP-6 设计的关系

当前 KEP-6 设计仅将 **containerd** 重构为 `ComponentVersion`（type=binary）。Docker 和 cri-dockerd 仍在旧的 bkeagent 插件路径中。如果后续要将 Docker 也迁移到声明式管理，需要单独设计 Docker 的 `ComponentVersion` YAML（`daemon.json` 作为 configTemplate，无 hosts.toml 需求）。

