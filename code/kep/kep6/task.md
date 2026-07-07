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
