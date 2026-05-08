# job/builtin`
在 `pkg/job/builtin` 目录下的设计，其实就是 **内置 Job 执行器的集合**，它们为 BKE Agent 提供了最基础的节点操作能力。这些 Job 是控制器下发后，Agent 在节点上执行的“原子动作”，用来支撑 Addon 的安装和环境初始化。
## 📑 设计思路
1. **统一接口**  
   - 每个 Job 都实现了一个通用的接口（通常是 `Run()` 或类似方法），保证可以被 Agent 调度。  
   - Job 的输入来自控制器下发的 `Command` 或 `AddonTransfer`，输出是执行结果（成功/失败、日志）。
2. **内置 Job 分类**
   - **文件/包分发类**：  
     - `file_downloader.go` → 下载文件到节点。  
     - `package_downloader.go` → 下载并安装软件包。  
   - **环境初始化类**：  
     - `install_lxcfs.go` → 安装 lxcfs，提供容器资源隔离。  
     - `install_nfsutils.go` → 安装 NFS 工具。  
     - `install_etcdctl.go` → 安装 etcdctl 工具。  
   - **系统操作类**：  
     - `systemctl.go` → 控制系统服务（启动/停止/重启）。  
     - `ntp_sync.go` → 同步系统时间。  
   - **调试/通用类**：  
     - `echo.go` → 简单输出，用于测试 Job 通道。  
3. **执行机制**
   - Job 被封装为 `builtin.Job`，由 Agent 的 `CommandReconciler` 调用。  
   - 每个 Job 内部通常会调用系统命令（如 `exec.Command`），或者操作文件系统。  
   - 执行结果通过 `JobResult` 回传给控制器。
4. **扩展性**
   - 新增 Job 只需在 `pkg/job/builtin` 下实现一个文件，并注册到 Job 工厂。  
   - 保持了模块化和可插拔性，方便扩展更多节点操作能力。
## 📊 设计优势与风险
| 方面 | 优势 | 风险 |
|------|------|------|
| **模块化** | 每个 Job 独立实现，职责清晰 | Job 数量多时，维护成本增加 |
| **可扩展性** | 新增 Job 简单，易于扩展 | Job 工厂注册需要保持一致性 |
| **通用性** | 支持文件、包、服务、时间等常见操作 | Job 内部直接调用系统命令，安全性需控制 |
| **可观测性** | JobResult 回传执行结果 | 错误日志可能不足以定位复杂问题 |
## ✅ 总结
- `pkg/job/builtin` 是 **Agent 的内置 Job 库**，提供文件分发、包安装、环境初始化、系统操作等能力。  
- 它们是 Addon 执行的底层支撑，保证控制器下发的任务能在节点上落地。  
- 设计上强调 **模块化、可扩展、可观测**，但需要在安全和错误处理上进一步加强。  

# builtin job如何使用及使用样例
在 BKE 的架构里，**builtin job** 是 Agent 内置的一组“原子操作”，它们的使用方式并不是直接在业务代码里调用，而是通过 **控制器下发任务 → Agent 调度 → builtin job 执行** 这一条链路来完成的。下面我帮你梳理一下使用方式和一个具体样例。
## 📑 使用方式
1. **控制器下发任务**
   - 控制器会创建一个 `Command` CRD 或 `AddonTransfer` 对象。
   - 在对象里指定要执行的 Job 名称（例如 `file-downloader`、`install-lxcfs`）和参数。
2. **Agent 接收任务**
   - Agent 的 `CommandReconciler` 监听到新的任务对象。
   - 它解析任务类型，找到对应的 builtin job 实现。
3. **Job 工厂匹配**
   - 在 `pkg/job/builtin` 下，每个 Job 都实现了统一接口（如 `Run()`）。
   - Agent 会通过 Job 工厂找到对应的 Job 实例。
4. **执行与回传**
   - Job 在节点上执行（下载文件、安装包、运行脚本等）。
   - 执行结果封装为 `JobResult`，回传给控制器，更新状态。
## ⚙️ 使用样例
### **场景：下载文件到节点**
控制器下发一个 `Command` CRD：
```yaml
apiVersion: bkeagent.io/v1beta1
kind: Command
metadata:
  name: download-config
spec:
  job: file-downloader
  args:
    - "https://example.com/config.yaml"
    - "/etc/kubernetes/config.yaml"
```
### **Agent 执行流程**
1. `CommandReconciler` 监听到 `download-config`。  
2. 匹配到 builtin job → `pkg/job/builtin/file_downloader.go`。  
3. 调用 `Run()` 方法，执行下载逻辑：  
   ```go
   func (j *FileDownloaderJob) Run(args []string) (JobResult, error) {
       url := args[0]
       dest := args[1]
       // 下载文件并保存到目标路径
   }
   ```
4. 执行结果封装为 `JobResult`，回传控制器。  
### **结果**
- 文件被下载到节点 `/etc/kubernetes/config.yaml`。  
- 控制器更新任务状态为 `Succeeded`。  
## ✅ 总结
- builtin job 的使用方式是 **通过 CRD/AddonTransfer 下发任务，由 Agent 调度执行**。  
- 每个 builtin job 都是一个独立的原子操作（下载文件、安装工具、控制服务等）。  
- 使用样例：下发一个 `Command` CRD，指定 job 名称和参数，Agent 自动匹配并执行。  

# Job 工厂包含builtin 、k8s与shell的Job吗？
是的，**Job 工厂的设计就是一个统一的注册与调度中心**，它通常会包含三类 Job：  
## 📑 Job 工厂包含的三类 Job
### 1. **builtin Job**
- 位于 `pkg/job/builtin` 目录。  
- 提供节点常用的原子操作，例如：  
  - `install-lxcfs` → 安装 lxcfs  
  - `systemctl` → 控制系统服务  
  - `ntp-sync` → 同步时间  
  - `file-downloader` → 下载文件  
  - `package-downloader` → 安装软件包  
- 这些 Job 是 Agent 内置的，保证集群初始化和节点环境配置能顺利完成。
### 2. **k8s Job**
- 封装对 Kubernetes API 或工具的调用。  
- 典型操作：  
  - 应用 YAML 清单（`kubectl apply`）。  
  - 安装 Helm Chart。  
  - 管理 Deployment、Service 等资源。  
- 这类 Job 让 Agent 能够直接在集群层面执行操作，而不仅仅是节点 OS 层。
### 3. **shell Job**
- 封装直接在节点操作系统层面执行的命令或脚本。  
- 典型操作：  
  - 执行 `.sh` 脚本（如 `install-nfsutils.sh`）。  
  - 调用系统命令（如 `systemctl restart docker`）。  
- 这类 Job 让 Agent 能够完成节点环境初始化和运维操作。
## ⚙️ 工厂的工作方式
- 工厂维护一个 **注册表**，把 Job 名称映射到对应的实现。  
- 当 Agent 收到任务时：  
  ```go
  job := factory.GetJob(command.Spec.Job)
  result, err := job.Run(command.Spec.Args)
  ```
- 如果是 builtin → 调用 `pkg/job/builtin` 下的实现。  
- 如果是 k8s → 调用 Kubernetes 客户端。  
- 如果是 shell → 调用系统命令/脚本。  
## ✅ 总结
- **Job 工厂确实包含 builtin、k8s 与 shell 三类 Job**。  
- 它的作用是统一调度，不管任务来自控制器是 YAML、Chart，还是脚本，都能通过工厂找到对应的实现并执行。  
- 这样，控制器只需下发任务名称，Agent 就能通过工厂完成节点或集群操作。  
