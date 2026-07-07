# 基于对 kep6-detailed-design.md 的深入分析，我将从功能可独立验收的维度拆分为以下 Story：

## Story 拆分方案

### Story 1: ComponentVersion CRD 扩展与基础设施
**工作量**: 25 人日  
**功能范围**:
- ComponentVersion CRD 类型定义扩展（Binary/Helm/YAML/Selector）
- BinarySpec、HelmSpec、YAMLSpec、SelectorSpec 字段定义
- ArtifactSpec、ConfigTemplateSpec 等子类型定义
- NodeFilterSpec 节点过滤策略
- CRD YAML Schema 定义
- DeepCopy 方法生成
- 版本迁移设计（v1alpha1 扩展）

**验收标准**:
- ComponentVersion CRD 支持 binary/helm/yaml/selector 四种类型
- 所有字段定义完整，包含校验规则
- CRD 可以通过 kubectl apply 部署
- 旧版本 ComponentVersion 向后兼容
- 单元测试覆盖率 > 90%

---

### Story 2: 模板变量系统与 TemplateContext
**工作量**: 22 人日  
**功能范围**:
- TemplateContext 数据结构设计
- 8 类 50+ 模板变量实现（集群信息、节点信息、版本信息、制品信息、镜像仓库、安装路径、操作类型、自定义变量）
- TemplateRenderer 模板渲染引擎
- 自定义函数定义（upper/lower/eq/ne/gt/ge/lt/le/default/joinPath/now/date/semver）
- ConfigRenderer 配置渲染器（Content/Secret/Kubeconfig 三种模式）
- forEach 动态多文件生成机制
- TemplateContext 构建流程

**验收标准**:
- 支持 8 类 50+ 模板变量
- 模板渲染正确，支持条件渲染
- 自定义函数功能完整
- ConfigRenderer 支持三种渲染模式
- forEach 可以动态生成多个文件
- 单元测试覆盖率 > 90%

---

### Story 3: BinaryInstaller 核心实现
**工作量**: 35 人日  
**功能范围**:
- BinaryInstaller 架构设计与接口定义
- ArtifactDownloader 制品下载器（HTTP 下载、本地缓存、Checksum 校验）
- SSH Executor 执行器（文件上传、脚本执行、结果收集）
- HealthChecker 健康检查（SSH 脚本执行）
- Install/Upgrade/Uninstall 完整流程
- 错误处理与重试机制
- 多架构支持（amd64/arm64）
- 多操作系统支持（CentOS/Ubuntu）

**验收标准**:
- 可以下载二进制制品并校验 Checksum
- 可以通过 SSH 上传制品和配置到远程节点
- 可以执行安装脚本并收集结果
- 支持 Install/Upgrade/Uninstall 三种操作
- 支持 amd64/arm64 架构
- 支持 CentOS 7/8、Ubuntu 20.04/22.04
- 单元测试覆盖率 > 85%

---

### Story 4: HelmInstaller 核心实现
**工作量**: 30 人日  
**功能范围**:
- HelmInstaller 架构设计与接口定义
- ChartFetcher Chart 获取器（OCI Registry、HTTP URL、本地路径）
- ValuesRenderer Values 渲染器（模板变量替换、valuesFiles 加载、合并策略）
- Helm Action Executor（Install/Upgrade/Rollback/Uninstall）
- Wait/Atomic 控制机制
- HealthChecker 健康检查（PodReady/EndpointReady/Custom）
- Hooks 执行引擎（PreInstallHooks/PreUninstallHooks）
- 错误处理与回滚机制

**验收标准**:
- 可以从 OCI/HTTP/本地获取 Helm Chart
- 可以渲染 Values 并支持 valuesFiles
- 支持 Install/Upgrade/Rollback/Uninstall 四种操作
- 支持 --wait 和 --atomic 标志
- 支持 PodReady/EndpointReady/Custom 健康检查
- 支持 PreInstallHooks 和 PreUninstallHooks
- 单元测试覆盖率 > 85%

---

### Story 5: YamlInstaller 核心实现
**工作量**: 25 人日  
**功能范围**:
- YamlInstaller 架构设计与接口定义
- ManifestDownloader 清单下载器（ManifestStore、URL 下载、内联 Resources）
- YAML Parser 解析器（多文档解析、GVK 识别、资源分组）
- ApplyStrategy Engine 应用策略引擎（ServerSideApply/Replace/CreateOnly）
- K8s Applier 清单应用器
- Prune 裁剪功能（按 label selector 删除废弃资源）
- HealthChecker 健康检查（PodReady/EndpointReady/Custom）
- Uninstall 流程

**验收标准**:
- 可以从 ManifestStore/URL/内联 Resources 加载清单
- 可以解析多文档 YAML
- 支持 ServerSideApply/Replace/CreateOnly 三种策略
- 支持 Prune 裁剪功能
- 支持 PodReady/EndpointReady/Custom 健康检查
- 单元测试覆盖率 > 85%

---

### Story 6: HealthCheck 共享包与通用组件
**工作量**: 20 人日  
**功能范围**:
- HealthCheck 共享包设计
- HealthCheckSpec 类型定义
- PodReady 检查实现
- EndpointReady 检查实现
- Custom 检查实现
- 健康检查执行流程
- 超时与重试机制
- BinaryInstaller/HelmInstaller/YamlInstaller 集成

**验收标准**:
- HealthCheck 共享包可以被三个 Installer 复用
- 支持 PodReady/EndpointReady/Custom 三种检查类型
- 支持超时与重试机制
- 健康检查结果正确
- 单元测试覆盖率 > 90%

---

### Story 7: DAG 集成与执行器注册
**工作量**: 30 人日  
**功能范围**:
- DAG 执行器注册机制
- ExecutorRegistry 执行器注册表
- ComponentExecutor 接口定义
- BinaryComponentExecutor 实现
- HelmComponentExecutor 实现
- YamlComponentExecutor 实现
- InlineComponentExecutor 适配
- 类型分发逻辑
- 依赖注入机制
- ExecutionContext 执行上下文

**验收标准**:
- 支持 Binary/Helm/YAML/Inline 四种组件类型
- 执行器注册机制符合开闭原则
- 类型分发正确
- 依赖注入机制完整
- ExecutionContext 包含所有必要信息
- 单元测试覆盖率 > 85%

---

### Story 8: DAG 调度与状态管理
**工作量**: 28 人日  
**功能范围**:
- DAG 构建流程
- ComponentVersionStore 组件版本存储
- DAG 调度器实现
- 拓扑排序与批次执行
- 状态模型设计（NodeComponentStatuses/ComponentStatuses）
- 幂等性保证（isAlreadyAtTarget）
- 节点过滤策略（NodeFilter）
- 失败策略（FailFast/Continue/Rollback）
- 兼容性设计（新旧状态模型）

**验收标准**:
- DAG 构建正确，支持依赖关系
- 拓扑排序与批次执行正确
- 状态模型完整，包含节点级和组件级状态
- 幂等性保证正确
- 节点过滤策略支持角色/标签/幂等/预约节点
- 支持 FailFast/Continue/Rollback 三种失败策略
- 单元测试覆盖率 > 85%

---

### Story 9: 完整升级流程实现
**工作量**: 32 人日  
**功能范围**:
- 升级流程设计与实现
- ReleaseImage 解析
- 版本对比逻辑（compareVersions）
- VersionContext 版本上下文
- 升级 DAG 构建
- 滚动升级策略（Rolling）
- 批量升级策略（Batch）
- 并行升级策略（Parallel）
- 升级失败恢复机制
- 健康检查与验证

**验收标准**:
- 可以解析 ReleaseImage 并对比版本
- VersionContext 正确携带版本事实
- 升级 DAG 构建正确
- 支持 Rolling/Batch/Parallel 三种升级策略
- 升级失败可以恢复
- 健康检查正确
- 集成测试通过
- 单元测试覆盖率 > 80%

---

### Story 10: Feature Gate 与迁移策略
**工作量**: 25 人日  
**功能范围**:
- Feature Gate 设计与实现
- BinaryComponentSupport/HelmComponentSupport 开关
- 兼容层实现（新旧路径切换）
- 灰度发布机制
- 向后兼容保证
- 迁移验证清单
- 迁移文档编写

**验收标准**:
- Feature Gate 可以通过注解/全局 flag 控制
- 兼容层可以正确切换新旧路径
- 旧集群保持旧路径，新集群使用新路径
- 支持灰度发布
- 向后兼容，旧 ComponentVersion 不受影响
- 迁移文档完整
- 集成测试通过

---

### Story 11: 容器运行时重构（containerd + Docker + Selector）
**工作量**: 35 人日  
**功能范围**:
- containerd ComponentVersion YAML 编写
- Docker ComponentVersion YAML 编写
- cri-dockerd ComponentVersion YAML 编写
- container-runtime Selector 类型实现
- Selector 展开规则（condition 评估）
- Selector 依赖处理
- EnsureNodesEnv 重构（移除 runtime scope）
- containerd 配置模板（config.toml、service、hosts.toml）
- Docker 配置模板（daemon.json）
- forEach 动态生成 hosts.toml

**验收标准**:
- containerd 可以通过 BinaryInstaller 安装/升级
- Docker + cri-dockerd 可以通过 BinaryInstaller 安装/升级
- Selector 可以根据 CRI 类型展开为 containerd 或 docker
- EnsureNodesEnv 不包含 runtime scope
- containerd 配置正确（config.toml、service、hosts.toml）
- Docker 配置正确（daemon.json）
- 集成测试通过
- 单元测试覆盖率 > 80%

---

### Story 12: bkeagent 重构与 BKEAgentSwitch
**工作量**: 28 人日  
**功能范围**:
- bkeagent ComponentVersion YAML 编写
- bkeagent 配置模板（bkeagent.conf、TLS 证书、kubeconfig）
- bkeagent 安装/升级流程
- BKEAgentSwitch 独立组件设计
- bkeagent 监听目标切换逻辑
- cluster-api 集成
- bkeagent-switch ComponentVersion YAML 编写
- 切换流程验证

**验收标准**:
- bkeagent 可以通过 BinaryInstaller 安装/升级
- bkeagent 配置正确（bkeagent.conf、TLS 证书、kubeconfig）
- BKEAgentSwitch 可以在 cluster-api 部署后切换监听目标
- 切换流程正确
- 集成测试通过
- 单元测试覆盖率 > 80%

---

### Story 13: 完整安装流程实现
**工作量**: 30 人日  
**功能范围**:
- 安装流程设计与实现
- ReleaseImage 解析（install.components）
- 安装 DAG 构建
- CommonPhases 前置判断
- DeployPhases 核心部署
- PostPhases 后置处理
- BinaryComponentExecutor 执行
- HelmComponentExecutor 执行
- YamlComponentExecutor 执行
- InlineComponentExecutor 执行
- 健康检查与验证
- 安装失败恢复机制

**验收标准**:
- 可以解析 ReleaseImage 并构建安装 DAG
- 安装 DAG 构建正确，支持依赖关系
- 四种组件类型可以正确执行
- 健康检查正确
- 安装失败可以恢复
- 集成测试通过
- 单元测试覆盖率 > 80%

---

### Story 14: 集成测试与 E2E 测试
**工作量**: 25 人日  
**功能范围**:
- 集成测试用例设计
- 全新安装测试（binary/helm/yaml）
- 升级测试（binary/helm/yaml）
- 回滚测试（binary/helm）
- 离线环境测试
- 多架构测试（amd64/arm64）
- E2E 测试用例设计
- 小规模安装测试（1M+2W）
- 中规模安装测试（3M+10W）
- 跨版本升级测试
- 升级失败恢复测试
- YAML Prune 验证测试

**验收标准**:
- 集成测试覆盖率 > 80%
- E2E 测试通过率 > 95%
- 所有测试场景通过
- 测试文档完整
- 测试报告可追溯

---

### Story 15: 错误处理与恢复机制
**工作量**: 22 人日  
**功能范围**:
- 错误分类机制（可重试/不可重试/部分失败）
- 重试机制实现（指数退避）
- 超时控制
- 详细错误日志
- 错误恢复策略
- 状态记录与追踪
- 错误处理流程图
- 错误处理文档编写

**验收标准**:
- 错误分类正确
- 重试机制符合指数退避
- 超时控制正确
- 错误日志详细
- 错误恢复策略有效
- 状态记录完整
- 单元测试覆盖率 > 85%

---

## 总结

**总 Story 数量**: 15 个  
**总工作量**: 402 人日（约 18 人月）  
**平均每个 Story**: 26.8 人日

**Story 依赖关系**:
```
Story 1 (CRD) → Story 2 (TemplateContext) → Story 3/4/5 (Installer)
                                              ↓
                                    Story 6 (HealthCheck)
                                              ↓
                                    Story 7/8 (DAG)
                                              ↓
                                    Story 9 (升级流程)
                                              ↓
                                    Story 10 (Feature Gate)
                                              ↓
                                    Story 11/12 (容器运行时/bkeagent)
                                              ↓
                                    Story 13 (安装流程)
                                              ↓
                                    Story 14 (测试)
                                    
Story 15 (错误处理) 可以并行开发
```

**建议开发顺序**:
1. 第一阶段（第1-2周）：Story 1 + Story 2（基础设施）
2. 第二阶段（第3-6周）：Story 3 + Story 4 + Story 5 + Story 6（核心安装器）
3. 第三阶段（第7-8周）：Story 7 + Story 8（DAG 集成）
4. 第四阶段（第9-10周）：Story 9 + Story 10（升级流程 + Feature Gate）
5. 第五阶段（第11-12周）：Story 11 + Story 12（容器运行时 + bkeagent）
6. 第六阶段（第13-14周）：Story 13（安装流程）
7. 第七阶段（第15-16周）：Story 14 + Story 15（测试 + 错误处理）

这个拆分方案确保每个 Story 都可以独立验收，工作量不少于 1 人月，并且有明确的验收标准和依赖关系。

# 基于 kep6-detailed-design.md 的设计内容，我将为这4个任务制定详细的工作内容和验收标准。

## 任务1：支持Chart组件的安装与升级

### 具体内容

**1.1 HelmInstaller 核心实现**
- ChartFetcher：支持 OCI Registry、HTTP URL、本地路径三种 Chart 来源
- ValuesRenderer：支持模板变量替换、valuesFiles 加载、合并策略
- Helm Action Executor：实现 Install/Upgrade/Rollback/Uninstall 四种操作
- Wait/Atomic 控制：支持 `--wait` 和 `--atomic` 标志
- 健康检查：支持 PodReady/EndpointReady/Custom 三种检查类型
- Hooks 执行引擎：支持 PreInstallHooks 和 PreUninstallHooks

**1.2 ComponentVersion Helm 类型定义**
- HelmSpec 字段定义（Chart、Namespace、ReleaseName、Values、ValuesFiles）
- HelmStrategySpec 字段定义（Mode、Wait、WaitTimeout、Atomic、CleanupOnFail）
- HealthCheckSpec 字段定义（PodReady、EndpointReady、Custom）
- HookSpec 字段定义（Name、Type、Manifest）

**1.3 模板变量系统集成**
- TemplateContext 扩展：支持集群信息、版本信息等变量
- Values 模板渲染：支持 `{{clusterName}}`、`{{componentVersion}}` 等变量
- 自定义函数：支持 upper/lower/eq/ne/default/joinPath 等函数

**1.4 DAG 集成**
- HelmComponentExecutor 实现
- 执行器注册到 ExecutorRegistry
- 类型分发：根据 `cv.Spec.Type == "helm"` 选择执行器

**1.5 状态管理**
- ComponentStatuses 组件级状态记录
- 版本跟踪：记录当前版本和目标版本
- 失败策略：支持 FailFast/Continue/Rollback

### 验收标准

**功能验收**
- [ ] 可以从 OCI Registry 拉取 Helm Chart
- [ ] 可以从 HTTP URL 下载 Helm Chart
- [ ] 可以从本地路径加载 Helm Chart
- [ ] 可以渲染 Values 模板（支持 50+ 变量）
- [ ] 支持 valuesFiles 加载和合并
- [ ] 支持 helm install 操作
- [ ] 支持 helm upgrade 操作
- [ ] 支持 helm rollback 操作
- [ ] 支持 helm uninstall 操作
- [ ] 支持 `--wait` 标志（等待 Pod Ready）
- [ ] 支持 `--atomic` 标志（失败自动回滚）
- [ ] 支持 PodReady 健康检查
- [ ] 支持 EndpointReady 健康检查
- [ ] 支持 Custom 健康检查
- [ ] 支持 PreInstallHooks 执行
- [ ] 支持 PreUninstallHooks 执行

**集成验收**
- [ ] 可以安装 coredns Helm 组件
- [ ] 可以升级 coredns Helm 组件（v1.10.1 → v1.11.1）
- [ ] 升级失败时自动回滚（atomic=true）
- [ ] 健康检查失败时返回错误
- [ ] 支持 FailFast 策略（立即终止）
- [ ] 支持 Continue 策略（记录警告继续）
- [ ] 支持 Rollback 策略（回滚后继续）

**测试验收**
- [ ] 单元测试覆盖率 > 85%
- [ ] 集成测试通过（安装/升级/回滚场景）
- [ ] E2E 测试通过（小规模集群）

---

## 任务2：支持二进制组件的安装与升级

### 具体内容

**2.1 BinaryInstaller 核心实现**
- ArtifactDownloader：HTTP 下载、本地缓存、Checksum 校验
- TemplateRenderer：Go template 渲染引擎、自定义函数
- ConfigRenderer：支持 Content/Secret/Kubeconfig 三种渲染模式
- SSH Executor：文件上传、脚本执行、结果收集
- HealthChecker：SSH 执行健康检查脚本（退出码 0=健康）
- forEach 动态多文件生成：支持按 registry 生成多个 hosts.toml

**2.2 ComponentVersion Binary 类型定义**
- BinarySpec 字段定义（Variables、Artifacts、ConfigTemplates、InstallScript、UninstallScript）
- ArtifactSpec 字段定义（Name、URL、Checksum、InstallPath）
- ConfigTemplateSpec 字段定义（Name、Path、PathTemplate、ForEach、Content、SecretRef、KubeconfigTemplate、Condition）
- NodeFilterSpec 字段定义（Roles、MatchLabels、SkipCompleted、ExcludeAppointment）

**2.3 模板变量系统**
- TemplateContext 扩展：支持集群信息、节点信息、版本信息、制品信息、镜像仓库、安装路径、操作类型、自定义变量
- 8 类 50+ 变量实现
- 自定义函数：upper/lower/eq/ne/gt/ge/lt/le/default/joinPath/now/date/semver

**2.4 多架构与多操作系统支持**
- 架构发现：通过 SSH 执行 `uname -m` 获取节点架构
- 架构适配：支持 amd64 和 arm64
- 操作系统支持：支持 CentOS 7/8、Ubuntu 20.04/22.04

**2.5 DAG 集成**
- BinaryComponentExecutor 实现
- 执行器注册到 ExecutorRegistry
- 类型分发：根据 `cv.Spec.Type == "binary"` 选择执行器
- 节点级调度：支持 Rolling/Parallel/Batch 三种策略

**2.6 状态管理**
- NodeComponentStatuses 节点级状态记录
- ComponentStatuses 组件级状态记录
- 版本跟踪：记录每个节点的当前版本和目标版本
- 幂等性保证：isAlreadyAtTarget 检查
- 节点过滤：支持角色/标签/幂等/预约节点过滤

**2.7 错误处理与恢复**
- 错误分类：可重试/不可重试/部分失败
- 重试机制：指数退避
- 超时控制
- 失败策略：FailFast/Continue/Rollback
- 回滚机制：执行 UninstallScript

### 验收标准

**功能验收**
- [ ] 可以下载二进制制品（HTTP）
- [ ] 支持本地缓存（避免重复下载）
- [ ] 支持 Checksum 校验（sha256）
- [ ] 可以渲染 installScript 模板
- [ ] 可以渲染 configTemplates（Content 模式）
- [ ] 可以从 Secret 获取配置（Secret 模式）
- [ ] 可以动态生成 kubeconfig（Kubeconfig 模式）
- [ ] 支持 forEach 动态多文件生成
- [ ] 支持 condition 条件渲染
- [ ] 可以通过 SSH 上传制品到远程节点
- [ ] 可以通过 SSH 上传配置到远程节点
- [ ] 可以通过 SSH 执行安装脚本
- [ ] 可以收集脚本执行结果（stdout/stderr）
- [ ] 支持 Install 操作
- [ ] 支持 Upgrade 操作
- [ ] 支持 Uninstall 操作
- [ ] 支持 SSH 健康检查（退出码 0=健康）
- [ ] 支持 amd64 架构
- [ ] 支持 arm64 架构
- [ ] 支持 CentOS 7/8
- [ ] 支持 Ubuntu 20.04/22.04

**集成验收**
- [ ] 可以安装通用二进制组件
- [ ] 可以升级通用二进制组件
- [ ] 支持 Rolling 策略（逐节点升级）
- [ ] 支持 Parallel 策略（全节点并行）
- [ ] 支持 Batch 策略（分批升级）
- [ ] 支持 FailFast 策略
- [ ] 支持 Continue 策略
- [ ] 支持 Rollback 策略（执行 UninstallScript）
- [ ] 支持节点过滤（角色/标签/幂等/预约）
- [ ] 支持幂等性保证（已安装节点跳过）
- [ ] 支持失败重试

**测试验收**
- [ ] 单元测试覆盖率 > 85%
- [ ] 集成测试通过（安装/升级/卸载场景）
- [ ] E2E 测试通过（多架构、多操作系统）

---

## 任务3：支持容器运行时的二进制组件

### 具体内容

**3.1 containerd 组件实现**
- containerd ComponentVersion YAML 编写
- containerd 制品定义（tar.gz 解压）
- containerd 配置模板：
  - config.toml：containerd 主配置
  - containerd.service：systemd 服务文件
  - hosts.toml：镜像仓库配置（支持 forEach 动态生成）
- containerd installScript：停止服务 → 解压安装 → 启动服务
- containerd uninstallScript：停止服务 → 删除文件 → 清理配置
- containerd 健康检查：systemctl is-active + 版本验证

**3.2 Docker 组件实现**
- Docker ComponentVersion YAML 编写
- Docker 安装方式：包管理器安装（yum/apt），无 artifacts
- Docker 配置模板：
  - daemon.json：Docker 主配置（registry-mirrors、insecure-registries）
- Docker installScript：停止服务 → 包管理器安装 → 启动服务
- Docker uninstallScript：停止服务 → 卸载包 → 清理配置
- Docker 健康检查：systemctl is-active + docker info

**3.3 cri-dockerd 组件实现**
- cri-dockerd ComponentVersion YAML 编写
- cri-dockerd 制品定义（二进制下载）
- cri-dockerd 配置模板：
  - cri-dockerd.service：systemd 服务文件
  - cri-dockerd.socket：systemd socket 文件
- cri-dockerd installScript：停止服务 → 安装二进制 → 启动服务
- cri-dockerd 健康检查：systemctl is-active

**3.4 Selector 类型实现**
- container-runtime ComponentVersion YAML（type: selector）
- subComponents 定义：containerd、docker、cri-dockerd
- condition 评估：根据 `BKECluster.Spec.Cluster.ContainerRuntime.CRI` 选择
- Selector 展开规则：DAG 构建期评估 condition
- Selector 依赖处理：子组件继承 selector 的依赖

**3.5 EnsureNodesEnv 重构**
- 移除 runtime scope（containerd 安装由 BinaryInstaller 接管）
- 确保 EnsureNodesEnv 不包含 containerd 安装逻辑
- 验证 EnsureNodesEnv 其他功能正常

**3.6 在线/离线场景支持**
- 在线场景：仅为 imageRepo 生成 hosts.toml
- 离线场景：为所有公共仓库生成 hosts.toml（重定向到私有仓库）
- isOffline 变量：根据 `Config.Cluster.ContainerRuntime.Registry` 判断
- forEach 动态生成：按 registry 生成多个 hosts.toml

**3.7 DAG 依赖关系**
- containerd 依赖 bkeagent
- docker 依赖 bkeagent
- cri-dockerd 依赖 docker
- EnsureNodesEnv 依赖 containerd/docker

### 验收标准

**功能验收**
- [ ] containerd 可以通过 BinaryInstaller 安装
- [ ] containerd 可以通过 BinaryInstaller 升级
- [ ] containerd 配置正确（config.toml、service、hosts.toml）
- [ ] containerd 支持在线场景（单个 hosts.toml）
- [ ] containerd 支持离线场景（多个 hosts.toml）
- [ ] containerd 支持 forEach 动态生成 hosts.toml
- [ ] Docker 可以通过 BinaryInstaller 安装
- [ ] Docker 可以通过 BinaryInstaller 升级
- [ ] Docker 配置正确（daemon.json）
- [ ] Docker 支持 registry-mirrors 配置
- [ ] Docker 支持 insecure-registries 配置
- [ ] cri-dockerd 可以通过 BinaryInstaller 安装
- [ ] cri-dockerd 配置正确（service、socket）
- [ ] Selector 可以根据 CRI 类型展开为 containerd
- [ ] Selector 可以根据 CRI 类型展开为 docker + cri-dockerd
- [ ] Selector condition 评估正确
- [ ] EnsureNodesEnv 不包含 runtime scope

**集成验收**
- [ ] 可以安装 containerd 容器运行时
- [ ] 可以升级 containerd 容器运行时（v1.7.15 → v1.7.18）
- [ ] 可以安装 Docker 容器运行时
- [ ] 可以升级 Docker 容器运行时（v24.0.0 → v26.0.0）
- [ ] 可以安装 cri-dockerd（K8s >= 1.24）
- [ ] containerd 健康检查通过
- [ ] Docker 健康检查通过
- [ ] cri-dockerd 健康检查通过
- [ ] Selector 展开正确（containerd 或 docker）
- [ ] EnsureNodesEnv 正常工作（不包含 runtime）

**测试验收**
- [ ] 单元测试覆盖率 > 80%
- [ ] 集成测试通过（containerd 安装/升级）
- [ ] 集成测试通过（Docker 安装/升级）
- [ ] E2E 测试通过（完整集群安装）
- [ ] 在线场景测试通过
- [ ] 离线场景测试通过

---

## 任务4：支持bkeagent/bkeagentswitch二进制组件

### 具体内容

**4.1 bkeagent 组件实现**
- bkeagent ComponentVersion YAML 编写
- bkeagent 制品定义（二进制下载，无版本号）
- bkeagent 配置模板：
  - node：节点标识文件（{{nodeHostname}}）
  - bkeagent.conf：bkeagent 主配置
  - TLS 证书：从 Secret 获取（trust-chain.crt、global-ca.crt、global-ca.key）
  - kubeconfig：从 Secret 获取（kube-system/localkubeconfig）
  - bkeagent.service：systemd 服务文件
- bkeagent installScript：停止服务 → 安装二进制 → 启动服务
- bkeagent uninstallScript：停止服务 → 删除文件 → 清理配置
- bkeagent 健康检查：systemctl is-active + 版本验证

**4.2 BKEAgentSwitch 组件实现**
- bkeagent-switch ComponentVersion YAML 编写
- bkeagent-switch 制品定义：无 artifacts（bkeagent 已安装）
- bkeagent-switch 配置模板：
  - kubeconfig：目标集群 kubeconfig（从 Secret 获取）
  - node：节点标识
  - cluster：集群标识
- bkeagent-switch installScript：重启 bkeagent
- bkeagent-switch 健康检查：systemctl is-active
- 切换逻辑：在 cluster-api 部署完成后切换监听目标

**4.3 依赖关系**
- bkeagent 无依赖（首个安装的组件）
- bkeagent-switch 依赖 cluster-api
- containerd/docker 依赖 bkeagent
- EnsureNodesEnv 依赖 bkeagent

**4.4 状态管理**
- NodeComponentStatuses 记录 bkeagent 安装状态
- NodeAgentReadyFlag 标记 bkeagent 就绪
- BKENode.Status.StateCode 更新

**4.5 时序保证**
- DAG Batch 1: bkeagent 安装
- DAG Batch 2: containerd/docker 安装（依赖 bkeagent）
- DAG Batch 3: EnsureNodesEnv 执行（检查 NodeAgentReadyFlag）

**4.6 升级场景**
- bkeagent 升级流程
- skipCompleted=false（所有节点都执行）
- 版本对比逻辑

### 验收标准

**功能验收**
- [ ] bkeagent 可以通过 BinaryInstaller 安装
- [ ] bkeagent 可以通过 BinaryInstaller 升级
- [ ] bkeagent 配置正确（node、bkeagent.conf、TLS、kubeconfig、service）
- [ ] bkeagent 可以从 Secret 获取 TLS 证书
- [ ] bkeagent 可以从 Secret 获取 kubeconfig
- [ ] bkeagent 健康检查通过
- [ ] bkeagent-switch 可以通过 BinaryInstaller 执行
- [ ] bkeagent-switch 可以切换监听目标
- [ ] bkeagent-switch 配置正确（kubeconfig、node、cluster）
- [ ] bkeagent-switch 健康检查通过

**集成验收**
- [ ] bkeagent 安装到所有节点
- [ ] bkeagent 升级成功
- [ ] bkeagent 就绪后 containerd/docker 可以安装
- [ ] bkeagent 就绪后 EnsureNodesEnv 可以执行
- [ ] cluster-api 部署后 bkeagent-switch 可以执行
- [ ] bkeagent-switch 切换后 bkeagent 监听目标集群
- [ ] NodeAgentReadyFlag 正确设置
- [ ] NodeComponentStatuses 正确记录

**测试验收**
- [ ] 单元测试覆盖率 > 80%
- [ ] 集成测试通过（bkeagent 安装/升级）
- [ ] 集成测试通过（bkeagent-switch 执行）
- [ ] E2E 测试通过（完整集群安装）
- [ ] 时序保证测试通过（bkeagent → containerd → EnsureNodesEnv）

---

## 任务依赖关系与开发顺序

### 依赖关系图

```
任务2（二进制组件通用能力）
    ↓
任务1（Helm 组件）← 可并行
    ↓
任务3（容器运行时）
    ↓
任务4（bkeagent/bkeagent-switch）
```

### 建议开发顺序

**第一阶段（第1-4周）**：任务2（二进制组件通用能力）
- 实现 BinaryInstaller 核心功能
- 实现模板变量系统
- 实现 DAG 集成（BinaryComponentExecutor）
- 实现状态管理和错误处理

**第二阶段（第3-6周）**：任务1（Helm 组件）- 可与任务2部分并行
- 实现 HelmInstaller 核心功能
- 实现 DAG 集成（HelmComponentExecutor）
- 实现健康检查和 Hooks

**第三阶段（第5-8周）**：任务3（容器运行时）
- 实现 containerd/Docker/cri-dockerd 组件
- 实现 Selector 类型
- 重构 EnsureNodesEnv
- 实现在线/离线场景

**第四阶段（第7-10周）**：任务4（bkeagent/bkeagent-switch）
- 实现 bkeagent 组件
- 实现 bkeagent-switch 组件
- 验证时序保证
- 验证依赖关系

### 总工作量估算

- 任务1（Helm 组件）：30 人日
- 任务2（二进制组件通用能力）：35 人日
- 任务3（容器运行时）：35 人日
- 任务4（bkeagent/bkeagent-switch）：28 人日
- **总计**：128 人日（约 6 人月）

这个拆分方案确保每个任务都有明确的功能范围、具体的工作项和清晰的验收标准，可以独立开发和验收。
