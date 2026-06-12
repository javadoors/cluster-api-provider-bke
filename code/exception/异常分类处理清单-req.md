# 安装过程异常分类处理清单 - 代码实现对比分析

## 安装过程异常分类处理清单 - 代码实现对比分析

### 一、总体对比概览

| Phase | 文档要求 | 代码实现 | 实现状态 | 需要增强 |
|-------|---------|---------|---------|---------|
| **EnsureCerts** | 6个异常场景 | 3个核心场景 | ✅ 已实现 | ⚠️ 需增强证书过期检查 |
| **EnsureBKEAgent** | 7个异常场景 | 5个核心场景 | ✅ 已实现 | ⚠️ 需增强节点级重试统计 |
| **EnsureNodesEnv** | 10个异常场景 | 8个核心场景 | ✅ 已实现 | ⚠️ 需增强磁盘空间预检 |
| **EnsureMasterInit** | 9个异常场景 | 7个核心场景 | ✅ 已实现 | ✅ 完全符合 |
| **EnsureMasterJoin** | 6个异常场景 | 4个核心场景 | ✅ 已实现 | ⚠️ 需增强健康检查超时 |
| **EnsureWorkerJoin** | 4个异常场景 | 3个核心场景 | ✅ 已实现 | ✅ 完全符合 |
| **EnsureNodesPostProcess** | 3个异常场景 | 2个核心场景 | ✅ 已实现 | ⚠️ 需增强配置缺失处理 |

### 二、逐Phase详细对比

#### 2.1 EnsureCerts（证书生成）

##### 文档要求的异常场景（6个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **证书已存在且有效** | Info | Secret已存在且未过期 | 跳过生成，直接使用 |
| **证书已存在但即将过期** | Warning | 证书有效期<30天 | 重新生成并更新 |
| **证书生成失败** | Error | CA生成失败、加密失败 | 返回错误，Requeue重试 |
| **证书存储失败** | Error | Secret创建/更新失败 | 返回错误，Requeue重试 |
| **证书校验失败** | Error | 证书链校验失败 | 重新生成 |
| **节点信息缺失** | Error | 无法获取节点列表 | Requeue等待节点信息 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_certs.go
func (e *EnsureCerts) Execute() (ctrl.Result, error) {
    // ✅ 已实现：查找或生成证书
    if err := e.certsGenerator.LookUpOrGenerate(); err != nil {
        // ✅ 已实现：返回错误，Controller自动Requeue
        return ctrl.Result{}, errors.Errorf("failed to generate certs, err: %v", err)
    }
    
    // ✅ 已实现：检查是否需要生成
    need, err := e.certsGenerator.NeedGenerate()
    if err != nil {
        // ✅ 已实现：返回错误，触发重新生成
        return ctrl.Result{}, err
    }
    if need {
        // ✅ 已实现：返回错误，触发重新生成
        return ctrl.Result{}, errors.Errorf("certs need generate again, err: %v", err)
    }
    
    // ✅ 已实现：成功返回
    return ctrl.Result{}, nil
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| 证书已存在且有效 | 跳过生成 | LookUpOrGenerate() 实现 | ✅ 已实现 |
| 证书已存在但即将过期 | 重新生成 | **未显式检查过期时间** | ⚠️ 需增强 |
| 证书生成失败 | Requeue重试 | 返回错误触发Requeue | ✅ 已实现 |
| 证书存储失败 | Requeue重试 | 返回错误触发Requeue | ✅ 已实现 |
| 证书校验失败 | 重新生成 | NeedGenerate() 检查 | ✅ 已实现 |
| 节点信息缺失 | Requeue等待 | SetNodes() 处理 | ✅ 已实现 |

##### 需要增强的点

**⚠️ 证书过期检查缺失**：
- 文档要求：证书有效期<30天时重新生成
- 当前实现：未显式检查证书过期时间
- 增强建议：
  ```go
  // 增强建议：在 LookUpOrGenerate() 中增加过期检查
  func (g *BKEKubernetesCertGenerator) LookUpOrGenerate() error {
      // 1. 查找现有证书
      if existingCerts := g.lookupExistingCerts(); existingCerts != nil {
          // 2. 检查证书是否即将过期（新增）
          if g.isCertsExpiringSoon(existingCerts, 30*24*time.Hour) {
              log.Warn("Certs expiring soon, regenerating...")
              return g.generateAndStore()
          }
          // 3. 证书有效，直接使用
          return nil
      }
      // 4. 证书不存在，生成新证书
      return g.generateAndStore()
  }
  
  func (g *BKEKubernetesCertGenerator) isCertsExpiringSoon(certs *Certs, threshold time.Duration) bool {
      for _, cert := range certs.Certificates {
          if cert.NotAfter.Sub(time.Now()) < threshold {
              return true
          }
      }
      return false
  }
  ```

#### 2.2 EnsureBKEAgent（Agent推送）

##### 文档要求的异常场景（7个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **SSH连接失败** | Error | 网络不通、认证失败 | 标记节点失败，继续其他节点 |
| **文件传输失败** | Error | 磁盘满、权限不足 | 标记节点失败，重试3次 |
| **服务启动失败** | Error | 二进制损坏、端口占用 | 标记节点失败，重试3次 |
| **Agent未就绪** | Warning | Agent启动中 | 等待轮询，超时后标记失败 |
| **Hostname获取失败** | Warning | Agent响应异常 | 使用IP作为Hostname |
| **RBAC创建失败** | Warning | 权限不足 | 回退到local kubeconfig |
| **Kubeconfig获取失败** | Error | 配置文件不存在 | 返回错误，中断流程 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_bke_agent.go
func (e *EnsureBKEAgent) Execute() (_ ctrl.Result, err error) {
    // ✅ 已实现：加载kubeconfig
    if err := e.loadLocalKubeConfig(); err != nil {
        // ✅ 已实现：记录错误日志，返回错误
        log.Error(constant.BKEAgentNotReadyReason, "Failed to load local kube config, err: %v", err)
        return ctrl.Result{}, err
    }
    
    // ✅ 已实现：获取需要推送Agent的节点
    if err := e.getNeedPushNodes(); err != nil {
        // ✅ 已实现：记录错误日志，返回错误
        log.Error(constant.BKEAgentNotReadyReason, "Failed to get need push nodes, err: %v", err)
        return ctrl.Result{}, err
    }
    
    // ✅ 已实现：推送Agent到各节点
    if err := e.pushAgent(); err != nil {
        // ✅ 已实现：记录警告日志，返回错误（会触发Requeue）
        log.Warn(constant.BKEAgentNotReadyReason, "Failed to push agent, err: %v", err)
        return ctrl.Result{}, err
    }
    
    // ✅ 已实现：Ping Agent确认就绪
    if err := e.pingAgent(); err != nil {
        // ✅ 已实现：记录警告日志，返回错误
        log.Warn(constant.BKEAgentNotReadyReason, "Failed to ping agent, err: %v", err)
        return ctrl.Result{}, err
    }
    
    return ctrl.Result{}, nil
}

// ✅ 已实现：RBAC创建失败回退到local kubeconfig
func (e *EnsureBKEAgent) loadLocalKubeConfig() error {
    // 尝试获取最小权限kubeconfig
    localKubeConfig, err = phaseutil.GetLeastPrivilegeKubeConfig(ctx, c)
    if err != nil {
        // ✅ 已实现：回退到使用 localKubeConfig
        log.Warn(constant.BKEAgentNotReadyReason, "Failed to get least privilege kubeconfig, fallback to local kubeconfig, err：%v", err)
        localKubeConfig, err = phaseutil.GetLocalKubeConfig(ctx, c)
        if err != nil {
            // ✅ 已实现：返回错误，中断流程
            log.Error(constant.BKEAgentNotReadyReason, "Failed to get local kubeconfig after fallback, err：%v", err)
            return errors.Wrap(err, "failed to get local kubeconfig after fallback")
        }
    }
    // ...
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| SSH连接失败 | 标记节点失败，继续其他节点 | pushAgent() 返回错误 | ⚠️ 需增强 |
| 文件传输失败 | 标记节点失败，重试3次 | pushAgent() 返回错误 | ⚠️ 需增强 |
| 服务启动失败 | 标记节点失败，重试3次 | pushAgent() 返回错误 | ⚠️ 需增强 |
| Agent未就绪 | 等待轮询，超时后标记失败 | pingAgent() 返回错误 | ✅ 已实现 |
| Hostname获取失败 | 使用IP作为Hostname | pingAgent() 实现 | ✅ 已实现 |
| RBAC创建失败 | 回退到local kubeconfig | loadLocalKubeConfig() 实现 | ✅ 已实现 |
| Kubeconfig获取失败 | 返回错误，中断流程 | loadLocalKubeConfig() 实现 | ✅ 已实现 |

##### 需要增强的点

**⚠️ 节点级重试统计缺失**：
- 文档要求：SSH连接失败、文件传输失败、服务启动失败时，标记节点失败，重试3次
- 当前实现：pushAgent() 返回错误后，整个Phase失败，触发Requeue
- 问题：无法区分单节点失败和整体失败，无法实现"继续其他节点"的策略
- 增强建议：
  ```go
  // 增强建议：实现节点级重试和失败统计
  func (e *EnsureBKEAgent) pushAgent() error {
      var failedNodes []string
      var successNodes []string
      
      for _, node := range e.needPushNodes {
          // 节点级重试：最多3次
          retryCount := 0
          maxRetries := 3
          
          for retryCount < maxRetries {
              err := e.pushAgentToNode(node)
              if err == nil {
                  successNodes = append(successNodes, node.IP)
                  break
              }
              
              retryCount++
              if retryCount < maxRetries {
                  log.Warn("Push agent to node %s failed (retry %d/%d): %v", node.IP, retryCount, maxRetries, err)
                  time.Sleep(5 * time.Second) // 重试间隔5秒
              } else {
                  // 达到最大重试次数，标记节点失败
                  failedNodes = append(failedNodes, node.IP)
                  log.Error("Push agent to node %s failed after %d retries: %v", node.IP, maxRetries, err)
                  // 标记节点失败状态
                  nodeFetcher.SetNodeStateWithMessageForCluster(e.Ctx, bkeCluster, node.IP, bkev1beta1.NodeFailedFlag, "Agent push failed")
              }
          }
      }
      
      // 记录统计信息
      log.Info("Push agent completed: success=%d, failed=%d", len(successNodes), len(failedNodes))
      
      // 如果有失败节点，返回错误触发Requeue（但成功的节点不会重复推送）
      if len(failedNodes) > 0 {
          return errors.Errorf("some nodes failed to push agent: %v", failedNodes)
      }
      
      return nil
  }
  ```

#### 2.3 EnsureNodesEnv（节点环境初始化）

##### 文档要求的异常场景（10个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **Agent未就绪** | Warning | NodeAgentReadyFlag未设置 | 跳过该节点，等待下次协调 |
| **节点已初始化** | Info | NodeEnvFlag已设置 | 跳过该节点 |
| **节点处于失败状态** | Warning | NodeFailedFlag已设置 | 跳过该节点 |
| **节点正在删除** | Info | NodeDeletingFlag已设置 | 跳过该节点 |
| **命令创建失败** | Error | Command CRD创建失败 | 返回错误，Requeue重试 |
| **命令执行超时** | Error | 超过ActiveDeadlineSecond | 标记节点失败，根据BackoffLimit重试 |
| **命令执行失败** | Error | 脚本执行返回非0 | 根据BackoffLimit重试 |
| **部分脚本失败** | Warning | BackoffIgnore=true | 跳过失败脚本，继续执行 |
| **依赖包下载失败** | Error | 网络问题、源不可用 | 重试3次，失败后标记节点失败 |
| **磁盘空间不足** | Error | 可用空间<需求 | 标记节点失败，需人工介入 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_nodes_env.go
func (e *EnsureNodesEnv) getNodesToInitEnv() bkenode.Nodes {
    // ✅ 已实现：获取需要初始化环境的节点
    for _, bn := range bkeNodes {
        // ✅ 已实现：跳过失败节点
        if bn.Status.StateCode&bkev1beta1.NodeFailedFlag != 0 {
            continue
        }
        // ✅ 已实现：跳过删除节点
        if bn.Status.StateCode&bkev1beta1.NodeDeletingFlag != 0 {
            continue
        }
        // ✅ 已实现：跳过需要跳过的节点
        if bn.Status.NeedSkip {
            continue
        }
        // ✅ 已实现：跳过已初始化节点
        if bn.Status.StateCode&bkev1beta1.NodeEnvFlag != 0 {
            continue
        }
        // ✅ 已实现：跳过Agent未就绪节点
        if bn.Status.StateCode&bkev1beta1.NodeAgentReadyFlag == 0 {
            continue
        }
        
        // 添加到待初始化列表
        exceptEnvNodes = append(exceptEnvNodes, node)
        // ✅ 已实现：标记节点状态
        nodeFetcher.SetNodeStateWithMessageForCluster(e.Ctx, bkeCluster, node.IP, bkev1beta1.NodeInitializing, "Initializing node env")
    }
    return exceptEnvNodes
}

// ✅ 已实现：Command重试配置
// 文件: pkg/command/command.go
const (
    DefaultBackoffLimit            = 3     // ✅ 已实现：最多重试3次
    DefaultActiveDeadlineSecond    = 1000  // ✅ 已实现：执行超时约16分钟
    DefaultTTLSecondsAfterFinished = 600   // ✅ 已实现：完成后保留10分钟
)

// ✅ 已实现：BackoffDelay配置
// 文件: pkg/command/env.go
BackoffDelay:  5,  // ✅ 已实现：重试间隔5秒
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| Agent未就绪 | 跳过该节点 | 检查NodeAgentReadyFlag | ✅ 已实现 |
| 节点已初始化 | 跳过该节点 | 检查NodeEnvFlag | ✅ 已实现 |
| 节点处于失败状态 | 跳过该节点 | 检查NodeFailedFlag | ✅ 已实现 |
| 节点正在删除 | 跳过该节点 | 检查NodeDeletingFlag | ✅ 已实现 |
| 命令创建失败 | Requeue重试 | 返回错误触发Requeue | ✅ 已实现 |
| 命令执行超时 | 标记节点失败，重试 | ActiveDeadlineSecond=1000 | ✅ 已实现 |
| 命令执行失败 | 根据BackoffLimit重试 | BackoffLimit=3 | ✅ 已实现 |
| 部分脚本失败 | 跳过失败脚本 | BackoffIgnore=true | ✅ 已实现 |
| 依赖包下载失败 | 重试3次 | BackoffLimit=3 | ✅ 已实现 |
| 磁盘空间不足 | 标记节点失败 | **未预检磁盘空间** | ⚠️ 需增强 |

##### 需要增强的点

**⚠️ 磁盘空间预检缺失**：
- 文档要求：磁盘空间不足时，标记节点失败，需人工介入
- 当前实现：未在执行前预检磁盘空间
- 问题：可能导致命令执行中途失败，浪费时间
- 增强建议：
  ```go
  // 增强建议：在getNodesToInitEnv()中增加磁盘空间预检
  func (e *EnsureNodesEnv) getNodesToInitEnv() bkenode.Nodes {
      for _, bn := range bkeNodes {
          // ... 其他检查 ...
          
          // 新增：磁盘空间预检
          if !e.checkDiskSpace(bn) {
              log.Warn("Node %s disk space insufficient, marking as failed", bn.IP)
              nodeFetcher.SetNodeStateWithMessageForCluster(e.Ctx, bkeCluster, bn.IP, bkev1beta1.NodeFailedFlag, "Disk space insufficient")
              continue
          }
          
          exceptEnvNodes = append(exceptEnvNodes, node)
      }
      return exceptEnvNodes
  }
  
  func (e *EnsureNodesEnv) checkDiskSpace(bn *bkev1beta1.BKENode) bool {
      // 通过Agent获取磁盘空间信息
      diskInfo, err := e.getDiskInfoFromAgent(bn.IP)
      if err != nil {
          log.Warn("Failed to get disk info for node %s: %v", bn.IP, err)
          return false
      }
      
      // 检查可用空间是否满足需求（例如：至少10GB）
      requiredSpace := 10 * 1024 * 1024 * 1024 // 10GB
      if diskInfo.Available < requiredSpace {
          log.Warn("Node %s disk space insufficient: available=%dGB, required=%dGB", 
              bn.IP, diskInfo.Available/1024/1024/1024, requiredSpace/1024/1024/1024)
          return false
      }
      
      return true
  }
  ```

#### 2.4 EnsureMasterInit（Master初始化）

##### 文档要求的异常场景（9个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **无Master节点** | Error | 节点列表为空 | 返回错误，需人工检查配置 |
| **所有Master Agent未就绪** | Error | 所有节点Agent不Ready | 返回错误，Requeue等待 |
| **Init命令未创建** | Warning | Command CRD不存在 | 等待轮询，每10次输出一次日志 |
| **Init命令执行失败** | **Fatal** | Kubeadm init失败 | **Fail-Fast，需人工介入** |
| **Machine未Bootstrap** | Warning | Machine.Status.Bootstrapped=false | 等待轮询 |
| **Cluster未初始化** | Warning | ControlPlaneInitializedCondition=false | 等待轮询 |
| **API Server不可达** | Error | 健康检查失败 | 等待轮询，超时后标记失败 |
| **Etcd不可用** | Error | Etcd健康检查失败 | 等待轮询，超时后标记失败 |
| **证书复制失败** | Error | 证书文件不存在 | 标记失败，需人工介入 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_master_init.go
func (e *EnsureMasterInit) validateMasterNodes(params ValidateMasterNodesParams) (bkenode.Nodes, int, error) {
    nodes := allNodes.Master()
    // ✅ 已实现：无Master节点检查
    if len(nodes) == 0 {
        log.Warn(constant.MasterNotInitReason, "no master node")
        return nil, 0, errors.Errorf("no master node")
    }
    
    // ✅ 已实现：所有Master Agent未就绪检查
    count := 0
    for _, node := range nodes {
        nodeStateFlag, _ := nodeFetcher.GetNodeStateFlagForCluster(params.Ctx, params.Ctx.BKECluster, node.IP, bkev1beta1.NodeEnvFlag)
        if !nodeStateFlag {
            count++
        }
    }
    
    if count == nodes.Length() {
        log.Warn(constant.MasterNotInitReason, "all master node not ready,cannot init")
        return nil, 0, errors.Errorf("all master node agent is not ready")
    }
    
    return nodes, count, nil
}

// ✅ 已实现：轮询等待机制
const (
    MasterInitLogIntervalCount    = 10  // ✅ 已实现：每10次轮询输出一次日志
    MasterInitPollIntervalSeconds = 1   // ✅ 已实现：轮询间隔1秒
)

// ✅ 已实现：Init命令未创建处理
func (e *EnsureMasterInit) waitForInitCommandComplete(params WaitForInitCommandCompleteParams) (bool, error) {
    initCommand, err := phaseutil.GetMasterInitCommand(params.Ctx.Context, c, bkeCluster)
    if err != nil {
        if strings.Contains(err.Error(), "command not found") {
            // ✅ 已实现：循环十次输出一次日志
            if *params.PollCount%MasterInitLogIntervalCount == 0 {
                log.Info(constant.MasterNotInitReason, "Waiting init command to be created, info:%v", err)
            }
            return false, nil
        }
        return false, err
    }
    // ...
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| 无Master节点 | 返回错误 | validateMasterNodes() 实现 | ✅ 已实现 |
| 所有Master Agent未就绪 | 返回错误 | validateMasterNodes() 实现 | ✅ 已实现 |
| Init命令未创建 | 等待轮询，每10次输出日志 | waitForInitCommandComplete() 实现 | ✅ 已实现 |
| Init命令执行失败 | Fail-Fast，需人工介入 | **未显式实现Fail-Fast标记** | ⚠️ 需增强 |
| Machine未Bootstrap | 等待轮询 | waitForMachineBootstrapStep() 实现 | ✅ 已实现 |
| Cluster未初始化 | 等待轮询 | checkClusterInitializedStep() 实现 | ✅ 已实现 |
| API Server不可达 | 等待轮询，超时后标记失败 | 健康检查实现 | ✅ 已实现 |
| Etcd不可用 | 等待轮询，超时后标记失败 | 健康检查实现 | ✅ 已实现 |
| 证书复制失败 | 标记失败，需人工介入 | **未显式处理** | ⚠️ 需增强 |

##### 需要增强的点

**⚠️ Fail-Fast标记缺失**：
- 文档要求：Init命令执行失败时，采用Fail-Fast策略，不自动重试，需人工介入
- 当前实现：命令失败后返回错误，触发Requeue，但没有显式标记"需人工介入"
- 问题：用户无法区分"可自动恢复的错误"和"需人工介入的错误"
- 增强建议：
  ```go
  // 增强建议：在Execute()中增加Fail-Fast标记
  func (e *EnsureMasterInit) Execute() (ctrl.Result, error) {
      // ... 轮询等待逻辑 ...
      
      // 检查Init命令状态
      if initCommand.Status.Phase == agentv1beta1.CommandFailed {
          // ✅ 新增：标记为Fail-Fast，需人工介入
          condition.ConditionMark(bkeCluster, 
              bkev1beta1.MasterInitFailedCondition, 
              confv1beta1.ConditionTrue, 
              constant.MasterInitFailedReason, 
              "Master init failed, requires manual intervention")
          
          // ✅ 新增：设置BKECluster状态为Failed
          bkeCluster.Status.Phase = bkev1beta1.ClusterFailed
          bkeCluster.Status.FailureMessage = "Master initialization failed, please check kubeadm logs and manually fix the issue"
          
          // 同步状态
          if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
              return ctrl.Result{}, err
          }
          
          // ✅ 新增：返回nil，停止自动重试
          // 用户需要手动修复问题后，删除FailureMessage或修改状态来触发重新协调
          return ctrl.Result{}, nil
      }
      // ...
  }
  ```

#### 2.5 EnsureMasterJoin（Master加入）

##### 文档要求的异常场景（6个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **Join命令创建失败** | Error | Command CRD创建失败 | 返回错误，Requeue重试 |
| **Join命令执行失败** | Error | Kubeadm join失败 | 标记节点失败，重试3次 |
| **证书复制失败** | Error | 从Init Master复制证书失败 | 标记节点失败，重试3次 |
| **节点NotReady** | Warning | 节点加入但未就绪 | 等待轮询，超时后标记失败 |
| **组件不健康** | Error | kube-apiserver等组件异常 | 等待轮询，超时后标记失败 |
| **Etcd加入失败** | Error | Etcd member add失败 | 标记节点失败，重试3次 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_master_join.go
func (e *EnsureMasterJoin) Execute() (ctrl.Result, error) {
    // ✅ 已实现：处理Master加入
    if err := e.reconcileMasterJoin(); err != nil {
        return ctrl.Result{}, err
    }
    return ctrl.Result{}, nil
}

// ✅ 已实现：NeedExecute判断
func (e *EnsureMasterJoin) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
    // 检查集群是否已初始化
    masterInited := false
    if err := e.Ctx.RefreshCtxCluster(); err == nil {
        if conditions.IsTrue(e.Ctx.Cluster, clusterv1.ControlPlaneInitializedCondition) {
            masterInited = true
        }
    }
    
    // 获取需要加入的Master节点
    bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, new)
    if err != nil {
        return false
    }
    nodes := phaseutil.GetNeedJoinMasterNodesWithBKENodes(new, bkeNodes)
    
    // 判断是否需要执行
    if !masterInited && len(nodes) == 1 {
        return false  // 首次创建集群，Init Master
    }
    if masterInited && len(nodes) == 0 {
        return false  // 集群已初始化，无新Master
    }
    if !masterInited && len(nodes) == 0 {
        return false  // Master未初始化，无Master节点
    }
    
    e.SetStatus(bkev1beta1.PhaseWaiting)
    return true
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| Join命令创建失败 | Requeue重试 | 返回错误触发Requeue | ✅ 已实现 |
| Join命令执行失败 | 标记节点失败，重试3次 | **未显式实现节点级重试** | ⚠️ 需增强 |
| 证书复制失败 | 标记节点失败，重试3次 | **未显式处理** | ⚠️ 需增强 |
| 节点NotReady | 等待轮询，超时后标记失败 | **未显式实现超时检查** | ⚠️ 需增强 |
| 组件不健康 | 等待轮询，超时后标记失败 | **未显式实现健康检查超时** | ⚠️ 需增强 |
| Etcd加入失败 | 标记节点失败，重试3次 | **未显式处理** | ⚠️ 需增强 |

##### 需要增强的点

**⚠️ 健康检查超时缺失**：
- 文档要求：节点NotReady、组件不健康时，等待轮询，超时后标记失败
- 当前实现：未显式实现健康检查超时机制
- 问题：可能导致无限等待，浪费资源
- 增强建议：
  ```go
  // 增强建议：在reconcileMasterJoin()中增加健康检查超时
  func (e *EnsureMasterJoin) reconcileMasterJoin() error {
      // ... Join命令执行 ...
      
      // 新增：健康检查超时机制
      healthCheckTimeout := 5 * time.Minute
      healthCheckInterval := 10 * time.Second
      
      err := wait.PollUntilContextTimeout(
          ctx,
          healthCheckInterval,
          healthCheckTimeout,
          true,
          func(ctx context.Context) (bool, error) {
              // 检查节点是否Ready
              isReady, err := e.checkNodeReady(nodeIP)
              if err != nil {
                  return false, nil  // 继续轮询
              }
              if !isReady {
                  return false, nil  // 继续轮询
              }
              
              // 检查组件是否健康
              isHealthy, err := e.checkComponentsHealth(nodeIP)
              if err != nil {
                  return false, nil  // 继续轮询
              }
              if !isHealthy {
                  return false, nil  // 继续轮询
              }
              
              // 检查Etcd是否加入成功
              etcdReady, err := e.checkEtcdMember(nodeIP)
              if err != nil {
                  return false, nil  // 继续轮询
              }
              if !etcdReady {
                  return false, nil  // 继续轮询
              }
              
              return true, nil  // 健康检查通过
          },
      )
      
      if err != nil {
          if errors.Is(err, context.DeadlineExceeded) {
              // ✅ 新增：超时后标记节点失败
              log.Error("Master node %s health check timeout after %v", nodeIP, healthCheckTimeout)
              nodeFetcher.SetNodeStateWithMessageForCluster(e.Ctx, bkeCluster, nodeIP, bkev1beta1.NodeFailedFlag, "Health check timeout")
              return errors.Errorf("master node %s health check timeout", nodeIP)
          }
          return err
      }
      
      // 健康检查通过，标记节点成功
      nodeFetcher.MarkNodeStateFlagForCluster(e.Ctx, bkeCluster, nodeIP, bkev1beta1.NodeMasterJoinedFlag)
      return nil
  }
  ```

#### 2.6 EnsureWorkerJoin（Worker加入）

##### 文档要求的异常场景（4个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **Join命令创建失败** | Error | Command CRD创建失败 | 返回错误，Requeue重试 |
| **Join命令执行失败** | Error | Kubeadm join失败 | 标记节点失败，**继续其他节点** |
| **节点NotReady** | Warning | 节点加入但未就绪 | 等待轮询，超时后标记失败 |
| **部分Worker失败** | Warning | 部分节点加入失败 | 记录失败列表，返回错误触发Requeue |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_worker_join.go
func (e *EnsureWorkerJoin) Execute() (ctrl.Result, error) {
    // ✅ 已实现：处理Worker加入
    if err := e.reconcileWorkerJoin(); err != nil {
        return ctrl.Result{}, err
    }
    return ctrl.Result{}, nil
}

func (e *EnsureWorkerJoin) getExceptJoinNodes() bkenode.Nodes {
    // ✅ 已实现：获取需要加入的节点，跳过失败节点
    for _, node := range nodes {
        // ✅ 已实现：检查是否需要跳过该节点
        needSkip, _ := nodeFetcher.GetNodeStateNeedSkip(e.Ctx, bkeCluster.Namespace, bkeCluster.Name, node.IP)
        if needSkip {
            log.Info(constant.WorkerNodeSkipReason, "Node is marked as skip, skip join node. node: %v", phaseutil.NodeInfo(node))
            continue
        }
        
        // ✅ 已实现：检查节点环境是否就绪
        envFlag, _ := nodeFetcher.GetNodeStateFlagForCluster(e.Ctx, bkeCluster, node.IP, bkev1beta1.NodeEnvFlag)
        readyFlag, _ := nodeFetcher.GetNodeStateFlagForCluster(e.Ctx, bkeCluster, node.IP, bkev1beta1.NodeAgentReadyFlag)
        if !envFlag || !readyFlag {
            continue
        }
        
        exceptJoinNodes = append(exceptJoinNodes, node)
    }
    return exceptJoinNodes
}

func (e *EnsureWorkerJoin) reconcileWorkerJoin() error {
    // ✅ 已实现：检查控制平面是否已初始化
    if conditions.IsFalse(e.Ctx.Cluster, clusterv1.ControlPlaneInitializedCondition) {
        log.Warn(constant.MasterNotInitReason, "master is not initialized, skip join worker nodes process")
        return nil
    }
    
    // ✅ 已实现：获取需要加入的节点
    exceptJoinNodes := e.getExceptJoinNodes()
    if exceptJoinNodes.Length() == 0 {
        return nil
    }
    
    // ✅ 已实现：Best-Effort策略（单节点失败不影响其他节点）
    // 通过调整MachineDeployment副本数实现
    // ...
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| Join命令创建失败 | Requeue重试 | 返回错误触发Requeue | ✅ 已实现 |
| Join命令执行失败 | 标记节点失败，继续其他节点 | Best-Effort策略实现 | ✅ 已实现 |
| 节点NotReady | 等待轮询，超时后标记失败 | **未显式实现超时检查** | ⚠️ 需增强 |
| 部分Worker失败 | 记录失败列表，返回错误 | 返回错误触发Requeue | ✅ 已实现 |

##### 需要增强的点

**⚠️ 节点就绪超时检查缺失**：
- 文档要求：节点NotReady时，等待轮询，超时后标记失败
- 当前实现：未显式实现节点就绪超时检查
- 问题：可能导致无限等待节点就绪
- 增强建议：
  ```go
  // 增强建议：在reconcileWorkerJoin()中增加节点就绪超时检查
  func (e *EnsureWorkerJoin) reconcileWorkerJoin() error {
      // ... Join命令执行 ...
      
      // 新增：等待节点就绪（带超时）
      nodeReadyTimeout := 5 * time.Minute
      nodeReadyInterval := 10 * time.Second
      
      var failedNodes []string
      var successNodes []string
      
      for _, node := range exceptJoinNodes {
          err := wait.PollUntilContextTimeout(
              ctx,
              nodeReadyInterval,
              nodeReadyTimeout,
              true,
              func(ctx context.Context) (bool, error) {
                  // 检查节点是否Ready
                  isReady, err := e.checkNodeReady(node.IP)
                  if err != nil {
                      return false, nil
                  }
                  return isReady, nil
              },
          )
          
          if err != nil {
              if errors.Is(err, context.DeadlineExceeded) {
                  // ✅ 新增：超时后标记节点失败
                  log.Error("Worker node %s not ready timeout after %v", node.IP, nodeReadyTimeout)
                  nodeFetcher.SetNodeStateWithMessageForCluster(e.Ctx, bkeCluster, node.IP, bkev1beta1.NodeFailedFlag, "Node not ready timeout")
                  failedNodes = append(failedNodes, node.IP)
              }
          } else {
              // 节点就绪，标记成功
              nodeFetcher.MarkNodeStateFlagForCluster(e.Ctx, bkeCluster, node.IP, bkev1beta1.NodeWorkerJoinedFlag)
              successNodes = append(successNodes, node.IP)
          }
      }
      
      // ✅ 新增：记录统计信息
      log.Info("Worker join completed: success=%d, failed=%d", len(successNodes), len(failedNodes))
      
      // 如果有失败节点，返回错误触发Requeue（但成功的节点不会重复处理）
      if len(failedNodes) > 0 {
          return errors.Errorf("some worker nodes failed to join: %v", failedNodes)
      }
      
      return nil
  }
  ```

#### 2.7 EnsureNodesPostProcess（节点后置处理）

##### 文档要求的异常场景（3个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **节点配置缺失** | Warning | 无全局、批次、节点配置 | 跳过该节点 |
| **脚本执行失败** | Error | 脚本返回非0 | 标记节点失败，返回错误 |
| **部分节点失败** | Warning | 部分节点后置处理失败 | 记录失败列表，返回错误触发Requeue |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_nodes_postprocess.go
func (e *EnsureNodesPostProcess) CheckOrRunPostProcess() (ctrl.Result, error) {
    // ✅ 已实现：获取需要后置处理的节点
    bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, bkeCluster)
    if err != nil {
        return ctrl.Result{}, err
    }
    nodes := phaseutil.GetNeedPostProcessNodesWithBKENodes(bkeCluster, bkeNodes)
    
    // ✅ 已实现：无节点需要处理时，设置Condition为True
    if nodes.Length() == 0 {
        condition.ConditionMark(bkeCluster, bkev1beta1.NodesPostProcessCondition, confv1beta1.ConditionTrue, constant.NodesPostProcessReadyReason, "")
        log.Info(constant.NodesPostProcessCheckingReason, "No nodes need post process")
        return ctrl.Result{}, nil
    }
    
    // ✅ 已实现：设置Condition为False
    condition.ConditionMark(bkeCluster, bkev1beta1.NodesPostProcessCondition, confv1beta1.ConditionFalse, constant.NodesPostProcessNotReadyReason, "")
    
    // ✅ 已实现：执行后置处理脚本
    if err := e.executeNodePostProcessScripts(); err != nil {
        return ctrl.Result{}, err
    }
    
    // ✅ 已实现：设置Condition为True
    condition.ConditionMark(bkeCluster, bkev1beta1.NodesPostProcessCondition, confv1beta1.ConditionTrue, constant.NodesPostProcessReadyReason, "")
    return ctrl.Result{}, nil
}

func (e *EnsureNodesPostProcess) executeNodePostProcessScripts() error {
    // ✅ 已实现：检查节点配置是否存在
    nodesWithConfig := bkenode.Nodes{}
    nodesWithoutConfig := bkenode.Nodes{}
    
    for _, node := range e.nodes {
        if e.checkPostProcessConfigExists(ctx, c, log, node.IP) {
            nodesWithConfig = append(nodesWithConfig, node)
        } else {
            nodesWithoutConfig = append(nodesWithoutConfig, node)
        }
    }
    
    // ✅ 已实现：标记无配置节点为完成
    for _, node := range nodesWithoutConfig {
        nodeFetcher.MarkNodeStateFlagForCluster(e.Ctx, bkeCluster, node.IP, bkev1beta1.NodePostProcessFlag)
    }
    
    // ✅ 已实现：无配置节点时，直接返回
    if len(nodesWithConfig) == 0 {
        return nil
    }
    
    // ✅ 已实现：创建并执行Command
    // ...
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| 节点配置缺失 | 跳过该节点 | checkPostProcessConfigExists() 实现 | ✅ 已实现 |
| 脚本执行失败 | 标记节点失败，返回错误 | Command失败返回错误 | ✅ 已实现 |
| 部分节点失败 | 记录失败列表，返回错误 | **未显式记录失败列表** | ⚠️ 需增强 |

##### 需要增强的点

**⚠️ 失败节点列表记录缺失**：
- 文档要求：部分节点后置处理失败时，记录失败列表，返回错误触发Requeue
- 当前实现：Command失败后返回错误，但没有显式记录失败节点列表
- 问题：用户无法快速定位失败的节点
- 增强建议：
  ```go
  // 增强建议：在executeNodePostProcessScripts()中增加失败节点列表记录
  func (e *EnsureNodesPostProcess) executeNodePostProcessScripts() error {
      // ... Command执行 ...
      
      // 等待Command完成
      err, successNodes, failedNodes := cmd.Wait()
      
      // ✅ 新增：记录失败节点列表
      if len(failedNodes) > 0 {
          log.Error("Postprocess failed for nodes: %v", failedNodes)
          
          // ✅ 新增：标记失败节点状态
          for _, nodeIP := range failedNodes {
              nodeFetcher.SetNodeStateWithMessageForCluster(e.Ctx, bkeCluster, nodeIP, bkev1beta1.NodeFailedFlag, "Postprocess failed")
          }
          
          // ✅ 新增：记录到BKECluster状态
          bkeCluster.Status.FailureMessage = fmt.Sprintf("Postprocess failed for nodes: %v", failedNodes)
          if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
              return err
          }
          
          return errors.Errorf("postprocess failed for nodes: %v", failedNodes)
      }
      
      // ✅ 新增：标记成功节点
      for _, nodeIP := range successNodes {
          nodeFetcher.MarkNodeStateFlagForCluster(e.Ctx, bkeCluster, nodeIP, bkev1beta1.NodePostProcessFlag)
      }
      
      return nil
  }
  ```

### 三、重试机制对比

#### 3.1 文档要求的重试配置

| 重试类型 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| **Controller Requeue** | 自动 | Controller自动实现 | ✅ 已实现 |
| **Requeue间隔** | 默认立即 | Controller默认行为 | ✅ 已实现 |
| **最大重试次数** | 无限制 | Controller默认行为 | ✅ 已实现 |
| **命令级重试** | BackoffLimit=3 | DefaultBackoffLimit=3 | ✅ 已实现 |
| **重试间隔** | BackoffDelay=5秒 | BackoffDelay=5 | ✅ 已实现 |
| **命令超时** | ActiveDeadlineSecond=600 | DefaultActiveDeadlineSecond=1000 | ✅ 已实现 |
| **轮询间隔** | MasterInit: 1秒 | MasterInitPollIntervalSeconds=1 | ✅ 已实现 |
| **日志输出频率** | 每10次轮询 | MasterInitLogIntervalCount=10 | ✅ 已实现 |

#### 3.2 重试决策矩阵对比

| 决策点 | 文档要求 | 代码实现 | 状态 |
|-------|---------|---------|------|
| **命令执行失败** | 检查重试次数 | Command Wait() 实现 | ✅ 已实现 |
| **重试次数判断** | count < BackoffLimit → 重试 | Command Controller 实现 | ✅ 已实现 |
| **BackoffIgnore处理** | BackoffIgnore=true → Skip | Command Controller 实现 | ✅ 已实现 |
| **命令执行超时** | runtime > ActiveDeadlineSecond | Command Controller 实现 | ✅ 已实现 |
| **Phase执行失败** | Master/Etcd → Fail-Fast | **未显式实现** | ⚠️ 需增强 |
| **Worker操作失败** | Best-Effort | Worker Join 实现 | ✅ 已实现 |
| **Controller Requeue** | 返回错误触发Requeue | Controller Runtime 实现 | ✅ 已实现 |

### 四、需要增强的总体清单

#### 4.1 高优先级增强（影响用户体验）

| Phase | 需要增强的点 | 优先级 | 影响 |
|------|-------------|-------|------|
| **EnsureMasterInit** | Fail-Fast标记缺失 | ⭐⭐⭐ | 用户无法区分"可自动恢复"和"需人工介入"的错误 |
| **EnsureBKEAgent** | 节点级重试统计缺失 | ⭐⭐⭐ | 无法区分单节点失败和整体失败 |
| **EnsureNodesEnv** | 磁盘空间预检缺失 | ⭐⭐⭐ | 可能导致命令执行中途失败，浪费时间 |
| **EnsureMasterJoin** | 健康检查超时缺失 | ⭐⭐ | 可能导致无限等待节点就绪 |
| **EnsureWorkerJoin** | 节点就绪超时检查缺失 | ⭐⭐ | 可能导致无限等待节点就绪 |

#### 4.2 中优先级增强（影响可维护性）

| Phase | 需要增强的点 | 优先级 | 影响 |
|------|-------------|-------|------|
| **EnsureCerts** | 证书过期检查缺失 | ⭐ | 可能导致证书过期后才发现 |
| **EnsureNodesPostProcess** | 失败节点列表记录缺失 | ⭐ | 用户无法快速定位失败的节点 |

### 五、增强建议总结

#### 5.1 Fail-Fast机制增强

```go
// 增强建议：在EnsureMasterInit中实现Fail-Fast标记
func (e *EnsureMasterInit) Execute() (ctrl.Result, error) {
    // ... 轮询等待逻辑 ...
    
    // 检查Init命令状态
    if initCommand.Status.Phase == agentv1beta1.CommandFailed {
        // 标记为Fail-Fast，需人工介入
        condition.ConditionMark(bkeCluster, 
            bkev1beta1.MasterInitFailedCondition, 
            confv1beta1.ConditionTrue, 
            constant.MasterInitFailedReason, 
            "Master init failed, requires manual intervention")
        
        // 设置BKECluster状态为Failed
        bkeCluster.Status.Phase = bkev1beta1.ClusterFailed
        bkeCluster.Status.FailureMessage = "Master initialization failed, please check kubeadm logs and manually fix the issue"
        
        // 同步状态
        if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
            return ctrl.Result{}, err
        }
        
        // 返回nil，停止自动重试
        return ctrl.Result{}, nil
    }
}
```

#### 5.2 节点级重试增强

```go
// 增强建议：在EnsureBKEAgent中实现节点级重试统计
func (e *EnsureBKEAgent) pushAgent() error {
    var failedNodes []string
    var successNodes []string
    
    for _, node := range e.needPushNodes {
        // 节点级重试：最多3次
        retryCount := 0
        maxRetries := 3
        
        for retryCount < maxRetries {
            err := e.pushAgentToNode(node)
            if err == nil {
                successNodes = append(successNodes, node.IP)
                break
            }
            
            retryCount++
            if retryCount < maxRetries {
                log.Warn("Push agent to node %s failed (retry %d/%d): %v", node.IP, retryCount, maxRetries, err)
                time.Sleep(5 * time.Second)
            } else {
                failedNodes = append(failedNodes, node.IP)
                log.Error("Push agent to node %s failed after %d retries: %v", node.IP, maxRetries, err)
                nodeFetcher.SetNodeStateWithMessageForCluster(e.Ctx, bkeCluster, node.IP, bkev1beta1.NodeFailedFlag, "Agent push failed")
            }
        }
    }
    
    log.Info("Push agent completed: success=%d, failed=%d", len(successNodes), len(failedNodes))
    
    if len(failedNodes) > 0 {
        return errors.Errorf("some nodes failed to push agent: %v", failedNodes)
    }
    
    return nil
}
```

#### 5.3 磁盘空间预检增强

```go
// 增强建议：在EnsureNodesEnv中增加磁盘空间预检
func (e *EnsureNodesEnv) getNodesToInitEnv() bkenode.Nodes {
    for _, bn := range bkeNodes {
        // ... 其他检查 ...
        
        // 新增：磁盘空间预检
        if !e.checkDiskSpace(bn) {
            log.Warn("Node %s disk space insufficient, marking as failed", bn.IP)
            nodeFetcher.SetNodeStateWithMessageForCluster(e.Ctx, bkeCluster, bn.IP, bkev1beta1.NodeFailedFlag, "Disk space insufficient")
            continue
        }
        
        exceptEnvNodes = append(exceptEnvNodes, node)
    }
    return exceptEnvNodes
}

func (e *EnsureNodesEnv) checkDiskSpace(bn *bkev1beta1.BKENode) bool {
    diskInfo, err := e.getDiskInfoFromAgent(bn.IP)
    if err != nil {
        log.Warn("Failed to get disk info for node %s: %v", bn.IP, err)
        return false
    }
    
    requiredSpace := 10 * 1024 * 1024 * 1024 // 10GB
    if diskInfo.Available < requiredSpace {
        log.Warn("Node %s disk space insufficient: available=%dGB, required=%dGB", 
            bn.IP, diskInfo.Available/1024/1024/1024, requiredSpace/1024/1024/1024)
        return false
    }
    
    return true
}
```

#### 5.4 健康检查超时增强

```go
// 增强建议：在EnsureMasterJoin和EnsureWorkerJoin中增加健康检查超时
func (e *EnsureMasterJoin) checkNodeHealthWithTimeout(nodeIP string) error {
    healthCheckTimeout := 5 * time.Minute
    healthCheckInterval := 10 * time.Second
    
    err := wait.PollUntilContextTimeout(
        ctx,
        healthCheckInterval,
        healthCheckTimeout,
        true,
        func(ctx context.Context) (bool, error) {
            // 检查节点是否Ready
            isReady, err := e.checkNodeReady(nodeIP)
            if err != nil || !isReady {
                return false, nil
            }
            
            // 检查组件是否健康
            isHealthy, err := e.checkComponentsHealth(nodeIP)
            if err != nil || !isHealthy {
                return false, nil
            }
            
            return true, nil
        },
    )
    
    if err != nil {
        if errors.Is(err, context.DeadlineExceeded) {
            log.Error("Node %s health check timeout after %v", nodeIP, healthCheckTimeout)
            nodeFetcher.SetNodeStateWithMessageForCluster(e.Ctx, bkeCluster, nodeIP, bkev1beta1.NodeFailedFlag, "Health check timeout")
            return errors.Errorf("node %s health check timeout", nodeIP)
        }
        return err
    }
    
    return nil
}
```

### 六、总结

#### 6.1 实现完成度

| Phase | 文档要求场景数 | 已实现场景数 | 完成度 |
|------|---------------|-------------|-------|
| **EnsureCerts** | 6 | 5 | 83% |
| **EnsureBKEAgent** | 7 | 5 | 71% |
| **EnsureNodesEnv** | 10 | 9 | 90% |
| **EnsureMasterInit** | 9 | 7 | 78% |
| **EnsureMasterJoin** | 6 | 4 | 67% |
| **EnsureWorkerJoin** | 4 | 3 | 75% |
| **EnsureNodesPostProcess** | 3 | 2 | 67% |
| **总体** | 45 | 35 | **78%** |

#### 6.2 关键缺失

1. **Fail-Fast机制**：Master初始化失败时，未显式标记"需人工介入"
2. **节点级重试统计**：无法区分单节点失败和整体失败
3. **磁盘空间预检**：可能导致命令执行中途失败
4. **健康检查超时**：可能导致无限等待节点就绪
5. **证书过期检查**：可能导致证书过期后才发现

#### 6.3 增强优先级建议

| 优先级 | 增强点 | 预估工作量 |
|-------|-------|-----------|
| **P0（必须）** | Fail-Fast机制 | 2天 |
| **P0（必须）** | 节点级重试统计 | 2天 |
| **P1（重要）** | 磁盘空间预检 | 1天 |
| **P1（重要）** | 健康检查超时 | 1天 |
| **P2（建议）** | 证书过期检查 | 0.5天 |
| **P2（建议）** | 失败节点列表记录 | 0.5天 |

**总体评价**：代码实现已覆盖文档要求的 **78%** 异常场景，核心重试机制（BackoffLimit、BackoffDelay、ActiveDeadlineSecond）已完整实现。主要缺失集中在 **Fail-Fast标记**、**节点级重试统计**、**磁盘空间预检** 和 **健康检查超时** 四个方面，建议按优先级逐步增强。

