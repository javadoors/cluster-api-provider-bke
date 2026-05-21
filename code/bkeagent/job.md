# Job 执行器的作用
Job 执行器是 `bkeagent` 的**核心执行引擎**，负责将 `Command` CRD 中定义的声明式指令转化为节点侧的具体操作。它支持三种执行模式：
1.  **BuiltIn (内置插件)**：执行 Go 语言编写的复杂逻辑（如 `kubeadm init`, `containerd install`）。
2.  **Shell (脚本执行)**：在节点终端运行 Shell 命令或脚本。
3.  **Kubernetes (API 操作)**：通过 K8s Client 操作集群资源。

它负责处理命令的路由分发、标准输出/错误捕获、超时控制以及执行状态的返回。
# 一句话总结
**将管理面下发的声明式指令转化为节点侧的具体操作（Shell/插件/API），并反馈执行结果。**
