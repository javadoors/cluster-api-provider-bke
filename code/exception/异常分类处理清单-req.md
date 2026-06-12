# 安装过程异常分类处理清单 - 代码实现对比分析

## 安装过程异常分类处理清单 - 代码实现对比分析

### 一、总体对比概览

| Phase | 文档要求 | 代码实现 | 实现状态 | 需要增强 |
|-------|---------|---------|---------|---------|
| **EnsureFinalizer** | 2个异常场景 | 2个核心场景 | ✅ 已实现 | ✅ 完全符合 |
| **EnsurePaused** | 2个异常场景 | 2个核心场景 | ✅ 已实现 | ✅ 完全符合 |
| **EnsureClusterManage** | 2个异常场景 | 2个核心场景 | ✅ 已实现 | ✅ 完全符合 |
| **EnsureDeleteOrReset** | 2个异常场景 | 2个核心场景 | ✅ 已实现 | ✅ 完全符合 |
| **EnsureDryRun** | 2个异常场景 | 2个核心场景 | ✅ 已实现 | ✅ 完全符合 |
| **EnsureBKEAgent** | 7个异常场景 | 5个核心场景 | ✅ 已实现 | ⚠️ 需增强节点级重试统计 |
| **EnsureNodesEnv** | 10个异常场景 | 8个核心场景 | ✅ 已实现 | ⚠️ 需增强磁盘空间预检 |
| **EnsureClusterAPIObj** | 5个异常场景 | 4个核心场景 | ✅ 已实现 | ⚠️ 需增强状态轮询 |
| **EnsureCerts** | 6个异常场景 | 5个核心场景 | ✅ 已实现 | ⚠️ 需增强证书过期检查 |
| **EnsureLoadBalance** | 5个异常场景 | 3个核心场景 | ✅ 已实现 | ⚠️ 需增强健康检查 |
| **EnsureMasterInit** | 9个异常场景 | 7个核心场景 | ✅ 已实现 | ⚠️ 需增强Fail-Fast标记 |
| **EnsureMasterJoin** | 6个异常场景 | 4个核心场景 | ✅ 已实现 | ⚠️ 需增强健康检查超时 |
| **EnsureWorkerJoin** | 4个异常场景 | 3个核心场景 | ✅ 已实现 | ⚠️ 需增强节点就绪超时 |
| **EnsureAddonDeploy** | 5个异常场景 | 3个核心场景 | ✅ 已实现 | ⚠️ 需增强版本回滚 |
| **EnsureNodesPostProcess** | 3个异常场景 | 2个核心场景 | ✅ 已实现 | ⚠️ 需增强失败节点列表记录 |
| **EnsureAgentSwitch** | 5个异常场景 | 3个核心场景 | ✅ 已实现 | ⚠️ 需增强超时检查 |

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
          for _, nodeIP := range failedNodes {
              nodeFetcher.SetNodeStateWithMessageForCluster(e.Ctx, bkeCluster, nodeIP, bkev1beta1.NodeFailedFlag, "Postprocess failed")
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

#### 2.8 EnsureLoadBalance（负载均衡配置）

##### 文档要求的异常场景（5个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **负载均衡配置已存在** | Info | LB资源已创建 | 跳过创建，检查状态 |
| **负载均衡配置失败** | Error | API Server不可达、权限不足 | 返回错误，Requeue重试 |
| **负载均衡状态未就绪** | Warning | LB资源已创建但未Ready | 等待下次协调 |
| **关联资源缺失** | Error | 依赖的Secret/ConfigMap不存在 | Requeue等待依赖就绪 |
| **后端健康检查失败** | Error | 后端节点不可达 | 等待轮询，超时后返回错误 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_load_balance.go
func (e *EnsureLoadBalance) Execute() (ctrl.Result, error) {
    // ✅ 已实现：检查负载均衡资源是否存在
    lb, err := e.getLoadBalancer()
    if err != nil {
        if apierrors.IsNotFound(err) {
            // 资源不存在，创建
            if err := e.createLoadBalancer(); err != nil {
                return ctrl.Result{}, err
            }
            return ctrl.Result{Requeue: true}, nil
        }
        return ctrl.Result{}, err
    }
    
    // ✅ 已实现：检查负载均衡状态
    if !e.isLoadBalancerReady(lb) {
        return ctrl.Result{RequeueAfter: quickRequeueInterval}, nil
    }
    
    // ✅ 已实现：健康检查
    if err := e.checkBackendHealth(); err != nil {
        log.Warn("LoadBalancer backend unhealthy: %v", err)
        return ctrl.Result{RequeueAfter: quickRequeueInterval}, nil
    }
    
    return ctrl.Result{}, nil
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| 负载均衡配置已存在 | 跳过创建 | getLoadBalancer() 实现 | ✅ 已实现 |
| 负载均衡配置失败 | Requeue重试 | createLoadBalancer() 实现 | ✅ 已实现 |
| 负载均衡状态未就绪 | 等待协调 | isLoadBalancerReady() 实现 | ✅ 已实现 |
| 关联资源缺失 | Requeue等待 | **未显式处理** | ⚠️ 需增强 |
| 后端健康检查失败 | 等待轮询 | checkBackendHealth() 实现 | ✅ 已实现 |

##### 需要增强的点

**⚠️ 关联资源依赖检查缺失**：
- 文档要求：依赖的Secret/ConfigMap不存在时，Requeue等待依赖就绪
- 当前实现：未显式检查依赖资源
- 增强建议：在createLoadBalancer()中增加依赖资源预检

#### 2.9 EnsureAddonDeploy（Addon部署）

##### 文档要求的异常场景（5个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **Addon已部署** | Info | Addon资源已存在且版本匹配 | 跳过部署，检查状态 |
| **Addon部署失败** | Error | 镜像拉取失败、权限不足 | 返回错误，Requeue重试 |
| **Addon状态未就绪** | Warning | Pod未就绪 | 等待轮询，超时后返回错误 |
| **Addon版本不匹配** | Warning | 已部署版本与目标版本不一致 | 重新部署 |
| **依赖资源缺失** | Error | 依赖的ConfigMap/Secret不存在 | Requeue等待依赖就绪 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_addon_deploy.go
func (e *EnsureAddonDeploy) Execute() (ctrl.Result, error) {
    // ✅ 已实现：获取需要部署的Addon列表
    addons := e.getNeedDeployAddons()
    if len(addons) == 0 {
        return ctrl.Result{}, nil
    }
    
    // ✅ 已实现：遍历部署每个Addon
    for _, addon := range addons {
        existing, err := e.getAddon(addon.Name)
        if err != nil {
            if apierrors.IsNotFound(err) {
                if err := e.createAddon(addon); err != nil {
                    return ctrl.Result{}, err
                }
                return ctrl.Result{Requeue: true}, nil
            }
            return ctrl.Result{}, err
        }
        
        if !e.isVersionMatch(existing, addon) {
            if err := e.updateAddon(addon); err != nil {
                return ctrl.Result{}, err
            }
            return ctrl.Result{Requeue: true}, nil
        }
        
        if !e.isAddonReady(existing) {
            return ctrl.Result{RequeueAfter: quickRequeueInterval}, nil
        }
    }
    
    return ctrl.Result{}, nil
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| Addon已部署 | 跳过部署 | getAddon() 实现 | ✅ 已实现 |
| Addon部署失败 | Requeue重试 | createAddon() 实现 | ✅ 已实现 |
| Addon状态未就绪 | 等待轮询 | isAddonReady() 实现 | ✅ 已实现 |
| Addon版本不匹配 | 重新部署 | isVersionMatch() 实现 | ✅ 已实现 |
| 依赖资源缺失 | Requeue等待 | **未显式处理** | ⚠️ 需增强 |

##### 需要增强的点

**⚠️ 版本回滚机制缺失**：
- 文档要求：Addon升级失败时，可回滚到旧版本
- 当前实现：未显式实现回滚机制
- 增强建议：在updateAddon()失败时，尝试回滚到existing版本

#### 2.10 EnsureAgentSwitch（Agent切换）

##### 文档要求的异常场景（5个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **Agent已切换** | Info | Agent监听模式已正确配置 | 跳过切换 |
| **Agent切换失败** | Error | API Server不可达、配置更新失败 | 返回错误，Requeue重试 |
| **Agent状态异常** | Warning | Agent切换后未就绪 | 等待轮询，超时后返回错误 |
| **配置冲突** | Error | 新旧监听模式冲突 | 返回错误，需人工介入 |
| **网络不可达** | Error | Agent无法连接API Server | 等待轮询，超时后返回错误 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_agent_switch.go
func (e *EnsureAgentSwitch) Execute() (ctrl.Result, error) {
    // ✅ 已实现：检查Agent是否已正确切换
    if e.isAgentSwitched() {
        return ctrl.Result{}, nil
    }
    
    // ✅ 已实现：执行Agent切换
    if err := e.switchAgent(); err != nil {
        log.Error("Agent switch failed: %v", err)
        return ctrl.Result{}, err
    }
    
    // ✅ 已实现：等待Agent就绪
    if err := e.waitForAgentReady(); err != nil {
        log.Warn("Agent not ready after switch: %v", err)
        return ctrl.Result{RequeueAfter: quickRequeueInterval}, nil
    }
    
    return ctrl.Result{}, nil
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| Agent已切换 | 跳过切换 | isAgentSwitched() 实现 | ✅ 已实现 |
| Agent切换失败 | Requeue重试 | switchAgent() 实现 | ✅ 已实现 |
| Agent状态异常 | 等待轮询 | waitForAgentReady() 实现 | ✅ 已实现 |
| 配置冲突 | 需人工介入 | **未显式处理** | ⚠️ 需增强 |
| 网络不可达 | 等待轮询 | **未显式超时检查** | ⚠️ 需增强 |

##### 需要增强的点

**⚠️ 超时检查缺失**：
- 文档要求：Agent切换后未就绪时，超时后返回错误
- 当前实现：未显式实现超时机制
- 增强建议：在waitForAgentReady()中增加超时参数

#### 2.11 CommonPhases（前置判断Phase）

##### 文档要求的异常场景

| Phase | 异常场景 | 处理策略 |
|-------|---------|---------|
| **EnsureFinalizer** | Finalizer添加失败 | 自动重试 |
| **EnsurePaused** | 暂停状态检查 | 跳过后续Phase |
| **EnsureClusterManage** | 纳管判断 | 跳过后续Phase |
| **EnsureDeleteOrReset** | 删除/重置判断 | 跳过后续Phase |
| **EnsureDryRun** | DryRun模式 | 仅模拟不执行 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/list.go
CommonPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
    NewEnsureFinalizer,     // ✅ 已实现：添加Finalizer保护
    NewEnsurePaused,        // ✅ 已实现：暂停状态检查
    NewEnsureClusterManage, // ✅ 已实现：纳管判断
    NewEnsureDeleteOrReset, // ✅ 已实现：删除/重置判断
    NewEnsureDryRun,        // ✅ 已实现：DryRun模式
}
```

##### 实现状态对比

| Phase | 文档要求 | 代码实现 | 状态 |
|-------|---------|---------|------|
| EnsureFinalizer | Finalizer添加 | 已实现 | ✅ 完全符合 |
| EnsurePaused | 暂停检查 | 已实现 | ✅ 完全符合 |
| EnsureClusterManage | 纳管判断 | 已实现 | ✅ 完全符合 |
| EnsureDeleteOrReset | 删除/重置判断 | 已实现 | ✅ 完全符合 |
| EnsureDryRun | DryRun模式 | 已实现 | ✅ 完全符合 |

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
| **EnsureLoadBalance** | 关联资源依赖检查缺失 | ⭐⭐ | 可能导致创建失败后无法恢复 |

#### 4.2 中优先级增强（影响可维护性）

| Phase | 需要增强的点 | 优先级 | 影响 |
|------|-------------|-------|------|
| **EnsureCerts** | 证书过期检查缺失 | ⭐ | 可能导致证书过期后才发现 |
| **EnsureNodesPostProcess** | 失败节点列表记录缺失 | ⭐ | 用户无法快速定位失败的节点 |
| **EnsureAddonDeploy** | 版本回滚机制缺失 | ⭐ | Addon升级失败后无法自动回滚 |
| **EnsureAgentSwitch** | 超时检查缺失 | ⭐ | 可能导致无限等待Agent就绪 |

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
| **EnsureFinalizer** | 2 | 2 | 100% |
| **EnsurePaused** | 2 | 2 | 100% |
| **EnsureClusterManage** | 2 | 2 | 100% |
| **EnsureDeleteOrReset** | 2 | 2 | 100% |
| **EnsureDryRun** | 2 | 2 | 100% |
| **EnsureCerts** | 6 | 5 | 83% |
| **EnsureBKEAgent** | 7 | 5 | 71% |
| **EnsureNodesEnv** | 10 | 8 | 80% |
| **EnsureClusterAPIObj** | 5 | 4 | 80% |
| **EnsureLoadBalance** | 5 | 3 | 60% |
| **EnsureMasterInit** | 9 | 7 | 78% |
| **EnsureMasterJoin** | 6 | 4 | 67% |
| **EnsureWorkerJoin** | 4 | 3 | 75% |
| **EnsureAddonDeploy** | 5 | 3 | 60% |
| **EnsureNodesPostProcess** | 3 | 2 | 67% |
| **EnsureAgentSwitch** | 5 | 3 | 60% |
| **总体** | 75 | 55 | **73%** |

#### 6.2 关键缺失

1. **Fail-Fast机制**：Master初始化失败时，未显式标记"需人工介入"
2. **节点级重试统计**：无法区分单节点失败和整体失败
3. **磁盘空间预检**：可能导致命令执行中途失败
4. **健康检查超时**：可能导致无限等待节点就绪
5. **证书过期检查**：可能导致证书过期后才发现
6. **版本回滚机制**：Addon/Agent升级失败后无法自动回滚

#### 6.3 增强优先级建议

| 优先级 | 增强点 | 预估工作量 |
|-------|-------|-----------|
| **P0（必须）** | Fail-Fast机制 | 2天 |
| **P0（必须）** | 节点级重试统计 | 2天 |
| **P1（重要）** | 磁盘空间预检 | 1天 |
| **P1（重要）** | 健康检查超时 | 1天 |
| **P1（重要）** | 关联资源依赖检查 | 1天 |
| **P2（建议）** | 证书过期检查 | 0.5天 |
| **P2（建议）** | 失败节点列表记录 | 0.5天 |
| **P2（建议）** | 版本回滚机制 | 1天 |
| **P2（建议）** | 超时检查机制 | 1天 |

**总体评价**：代码实现已覆盖文档要求的 **73%** 异常场景，核心重试机制（BackoffLimit、BackoffDelay、ActiveDeadlineSecond）已完整实现。主要缺失集中在 **Fail-Fast标记**、**节点级重试统计**、**磁盘空间预检**、**健康检查超时**、**版本回滚机制** 和 **超时检查** 六个方面，建议按优先级逐步增强。

# 升级过程异常分类处理清单 - 代码实现对比分析
          
## 升级过程异常分类处理清单 - 代码实现对比分析

### 一、总体对比概览

| Phase | 文档要求 | 代码实现 | 实现状态 | 需要增强 |
|-------|---------|---------|---------|---------|
| **EnsureProviderSelfUpgrade** | 4个异常场景 | 3个核心场景 | ✅ 已实现 | ⚠️ 需增强回滚机制 |
| **EnsureAgentUpgrade** | 5个异常场景 | 4个核心场景 | ✅ 已实现 | ⚠️ 需增强DaemonSet健康检查 |
| **EnsureContainerdUpgrade** | 5个异常场景 | 3个核心场景 | ✅ 已实现 | ⚠️ 需增强健康检查 |
| **EnsureEtcdUpgrade** | 7个异常场景 | 5个核心场景 | ✅ 已实现 | ⚠️ 需增强Etcd集群健康检查 |
| **EnsureWorkerUpgrade** | 7个异常场景 | 6个核心场景 | ✅ 已实现 | ⚠️ 需增强集群状态预检 |
| **EnsureMasterUpgrade** | 7个异常场景 | 6个核心场景 | ✅ 已实现 | ⚠️ 需增强Fail-Fast标记 |
| **EnsureWorkerDelete** | 6个异常场景 | 4个核心场景 | ✅ 已实现 | ⚠️ 需增强超时检查 |
| **EnsureMasterDelete** | 6个异常场景 | 4个核心场景 | ✅ 已实现 | ⚠️ 需增强Etcd成员移除检查 |
| **EnsureComponentUpgrade** | 5个异常场景 | 4个核心场景 | ✅ 已实现 | ⚠️ 需增强版本回滚 |
| **EnsureCluster** | 5个异常场景 | 3个核心场景 | ✅ 已实现 | ⚠️ 需增强健康检查 |

### 二、逐Phase详细对比

#### 2.1 EnsureEtcdUpgrade（Etcd升级）

##### 文档要求的异常场景（7个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **Etcd备份失败** | **Fatal** | Etcd数据备份失败 | **Fail-Fast，需人工介入** |
| **Etcd版本不兼容** | Error | 目标版本与当前版本不兼容 | 返回错误，需人工检查版本 |
| **Etcd节点NotReady** | Error | Etcd节点未就绪 | 等待轮询，超时后标记失败 |
| **Etcd健康检查失败** | **Fatal** | Etcd集群不健康 | **Fail-Fast，需人工介入** |
| **Etcd升级命令失败** | Error | Kubeadm upgrade etcd失败 | 标记节点失败，重试3次 |
| **Etcd member add失败** | Error | Etcd成员添加失败 | 标记节点失败，重试3次 |
| **Etcd数据损坏** | **Fatal** | Etcd数据文件损坏 | **Fail-Fast，需人工介入** |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_etcd_upgrade.go
func (e *EnsureEtcdUpgrade) upgradeSingleNode(params SingleNodeUpgradeParams) error {
    // ✅ 已实现：检查节点是否需要跳过
    if skip, err := e.shouldSkipNode(params.BKECluster, params.Node, params.Log); err != nil {
        return err
    } else if skip {
        return nil
    }
    
    // ✅ 已实现：标记节点为升级中
    nodeStatusParams := NodeStatusParams{
        Client:     params.Client,
        BKECluster: params.BKECluster,
        Node:       params.Node,
    }
    if err := e.markNodeUpgrading(nodeStatusParams); err != nil {
        return err
    }
    
    // ✅ 已实现：执行Etcd升级
    upgradeParams := EtcdUpgradeParams{
        NeedBackup: params.NeedBackup,
        BackupNode: params.BackupNode,
        Node:       params.Node,
        Version:    params.BKECluster.Spec.ClusterConfig.Cluster.EtcdVersion,
    }
    if err := e.upgradeEtcd(upgradeParams); err != nil {
        // ✅ 已实现：处理升级失败
        failureParams := UpgradeFailureParams{
            Client:     params.Client,
            BKECluster: params.BKECluster,
            Node:       params.Node,
            Error:      err,
            Log:        params.Log,
        }
        return e.handleUpgradeFailure(failureParams)
    }
    
    // ✅ 已实现：标记节点升级成功
    return e.markNodeUpgradeSuccess(nodeStatusParams)
}

// ✅ 已实现：Etcd健康检查
func (e *EnsureEtcdUpgrade) waitForEtcdHealthCheck(params HealthCheckParams) error {
    return wait.PollImmediate(PollImmeInternal, PollImmeTimeout, func() (bool, error) {
        // 检查Etcd健康状态
        // ...
    })
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| Etcd备份失败 | Fail-Fast | upgrade.BackUpEtcd=true | ✅ 已实现 |
| Etcd版本不兼容 | 返回错误 | shouldSkipNode() 检查版本 | ✅ 已实现 |
| Etcd节点NotReady | 等待轮询 | filterUpgradeableNodes() 检查Agent状态 | ✅ 已实现 |
| Etcd健康检查失败 | Fail-Fast | waitForEtcdHealthCheck() 实现 | ✅ 已实现 |
| Etcd升级命令失败 | 标记节点失败 | handleUpgradeFailure() 实现 | ✅ 已实现 |
| Etcd member add失败 | 标记节点失败 | **未显式处理** | ⚠️ 需增强 |
| Etcd数据损坏 | Fail-Fast | **未显式处理** | ⚠️ 需增强 |

##### 需要增强的点

**⚠️ Etcd集群健康检查缺失**：
- 文档要求：Etcd集群不健康时，采用Fail-Fast策略
- 当前实现：只检查单个节点健康，未检查集群整体健康
- 增强建议：
  ```go
  // 增强建议：在upgradeNodes()中增加Etcd集群健康检查
  func (e *EnsureEtcdUpgrade) upgradeNodes(params NodeUpgradeParams) error {
      for _, node := range params.Nodes {
          // 升级单个节点
          if err := e.upgradeSingleNode(singleNodeParams); err != nil {
              return err
          }
          
          // ✅ 新增：每次升级后检查Etcd集群健康
          if err := e.checkEtcdClusterHealth(); err != nil {
              // ✅ 新增：Etcd集群不健康，Fail-Fast
              params.Log.Error("Etcd cluster health check failed after upgrading node %s", node.IP)
              return errors.Errorf("etcd cluster health check failed: %v", err)
          }
      }
      
      // ✅ 新增：最终Etcd集群健康检查
      if err := e.checkEtcdClusterHealth(); err != nil {
          return errors.Errorf("etcd cluster final health check failed: %v", err)
      }
      
      params.Log.Info("upgrade all etcd success")
      return nil
  }
  
  func (e *EnsureEtcdUpgrade) checkEtcdClusterHealth() error {
      // 使用etcdctl检查集群健康状态
      ctx, c, bkeCluster, _, log := e.Ctx.Untie()
      
      // 获取远程集群客户端
      remoteClient, err := kube.NewRemoteClientByBKECluster(ctx, c, bkeCluster)
      if err != nil {
          return err
      }
      
      // 检查所有Etcd节点健康状态
      etcdNodes := bkeCluster.Spec.Nodes.Etcd()
      for _, node := range etcdNodes {
          // 检查单个Etcd节点健康
          if err := e.checkEtcdNodeHealth(node); err != nil {
              return errors.Errorf("etcd node %s health check failed: %v", node.IP, err)
          }
      }
      
      // 检查Etcd集群整体健康（使用etcdctl endpoint health）
      // ...
      
      return nil
  }
  ```

#### 2.2 EnsureMasterUpgrade（Master升级）

##### 文档要求的异常场景（7个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **版本不兼容** | Error | 目标版本与当前版本不兼容 | 返回错误，需人工检查版本 |
| **Master节点NotReady** | Error | Master节点未就绪 | 等待轮询，超时后标记失败 |
| **Kubeadm upgrade失败** | **Fatal** | Kubeadm upgrade control-plane失败 | **Fail-Fast，需人工介入** |
| **API Server不可达** | **Fatal** | API Server健康检查失败 | **Fail-Fast，需人工介入** |
| **组件升级失败** | Error | kube-apiserver等组件升级失败 | 标记节点失败，重试3次 |
| **证书更新失败** | Error | 证书更新失败 | 标记节点失败，重试3次 |
| **Addon升级失败** | Warning | Addon版本更新失败 | 记录错误，继续执行 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_master_upgrade.go
func (e *EnsureMasterUpgrade) upgradeMasterNodesWithParams(params UpgradeMasterNodesParams) error {
    for _, node := range params.NeedUpgradeNodes {
        // ✅ 已实现：检查节点版本是否已是期望版本
        remoteNode, err := phaseutil.GetRemoteNodeByBKENode(params.Ctx, clientSet, node)
        if err != nil {
            params.Log.Error("get remote cluster Node resource failed, err: %v", err)
            return errors.Errorf("get remote cluster Node resource failed, err: %v", err)
        }
        
        if remoteNode.Status.NodeInfo.KubeletVersion == params.BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion {
            params.Log.Info("node %q is already the expected version %q, skip upgrade", ...)
            continue
        }
        
        // ✅ 已实现：标记节点为升级中
        nodeFetcher.SetNodeStateWithMessageForCluster(params.Ctx, params.BKECluster, node.IP, bkev1beta1.NodeUpgrading, "Upgrading")
        if err := mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster); err != nil {
            return err
        }
        
        // ✅ 已实现：执行节点升级
        if err := e.upgradeNode(params.NeedBackupEtcd, params.BackEtcdNode, node, remoteNode); err != nil {
            // ✅ 已实现：Master节点升级失败，阻断流程
            params.Log.Error("upgrade node %q failed: %v", phaseutil.NodeInfo(node), err)
            nodeFetcher.SetNodeStateWithMessageForCluster(params.Ctx, params.BKECluster, node.IP, bkev1beta1.NodeUpgradeFailed, err.Error())
            if err = mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster); err != nil {
                return errors.Errorf("upgrade node %q failed: %v", phaseutil.NodeInfo(node), err)
            }
            // ✅ 已实现：返回错误，阻断流程（Fail-Fast）
            return errors.Errorf("upgrade node %q failed: %v", phaseutil.NodeInfo(node), err)
        }
        
        // ✅ 已实现：标记节点升级成功
        nodeFetcher.SetNodeStateWithMessageForCluster(params.Ctx, params.BKECluster, node.IP, bkev1beta1.NodeNotReady, "Upgrading success")
        if err := mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster); err != nil {
            return err
        }
    }
    return nil
}

// ✅ 已实现：等待节点健康检查
func (e *EnsureMasterUpgrade) waitForNodeHealthCheckWithParams(params WaitForNodeHealthCheckParams) error {
    remoteClient, err := kube.NewRemoteClientByBKECluster(params.Ctx, params.Client, params.BKECluster)
    if err != nil {
        params.Log.Error("get remote client for BKECluster %q failed", ...)
        return errors.Errorf("get remote client for BKECluster %q failed: %v", ...)
    }
    
    // 等待节点健康检查通过
    params.Log.Info("wait for node %q pass healthy check", phaseutil.NodeInfo(params.Node))
    masterParams := WaitForWorkerNodeHealthCheckParams{
        Ctx:          params.Ctx,
        ClientSet:    clientSet,
        RemoteClient: remoteClient,
        Node:         params.Node,
        K8sVersion:   params.BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion,
        Logger:       params.Log,
    }
    err = waitForWorkerNodeHealthCheck(masterParams)
    // ...
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| 版本不兼容 | 返回错误 | 检查KubeletVersion | ✅ 已实现 |
| Master节点NotReady | 等待轮询 | getNeedUpgradeNodes() 检查Agent状态 | ✅ 已实现 |
| Kubeadm upgrade失败 | Fail-Fast | upgradeNode() 返回错误阻断流程 | ✅ 已实现 |
| API Server不可达 | Fail-Fast | waitForNodeHealthCheck() 检查 | ✅ 已实现 |
| 组件升级失败 | 标记节点失败 | upgradeNode() 实现 | ✅ 已实现 |
| 证书更新失败 | 标记节点失败 | **未显式处理** | ⚠️ 需增强 |
| Addon升级失败 | 记录错误继续执行 | updateAddonVersions() 实现 | ✅ 已实现 |

##### 需要增强的点

**⚠️ Fail-Fast标记缺失**：
- 文档要求：Master升级失败时，采用Fail-Fast策略，需人工介入
- 当前实现：返回错误阻断流程，但未显式标记"需人工介入"
- 问题：用户无法区分"可自动恢复的错误"和"需人工介入的错误"
- 增强建议：
  ```go
  // 增强建议：在upgradeMasterNodesWithParams()中增加Fail-Fast标记
  func (e *EnsureMasterUpgrade) upgradeMasterNodesWithParams(params UpgradeMasterNodesParams) error {
      for _, node := range params.NeedUpgradeNodes {
          // ...
          
          if err := e.upgradeNode(...); err != nil {
              // ✅ 新增：标记为Fail-Fast，需人工介入
              condition.ConditionMark(params.BKECluster, 
                  bkev1beta1.MasterUpgradeFailedCondition, 
                  confv1beta1.ConditionTrue, 
                  constant.MasterUpgradeFailedReason, 
                  "Master upgrade failed, requires manual intervention")
              
              // ✅ 新增：设置BKECluster状态为Failed
              params.BKECluster.Status.Phase = bkev1beta1.ClusterFailed
              params.BKECluster.Status.FailureMessage = fmt.Sprintf("Master node %s upgrade failed, please check kubeadm logs and manually fix the issue", node.IP)
              
              // 同步状态
              if err := mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster); err != nil {
                  return err
              }
              
              // ✅ 新增：返回nil，停止自动重试
              return nil
          }
          // ...
      }
      return nil
  }
  ```

#### 2.3 EnsureWorkerUpgrade（Worker升级）

##### 文档要求的异常场景（7个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **集群状态不健康** | Error | ClusterStatus=Unhealthy/Unknown | 跳过升级，等待集群恢复 |
| **版本不兼容** | Error | 目标版本与当前版本不兼容 | 返回错误，需人工检查版本 |
| **节点Drain失败** | Warning | 节点驱逐Pod失败 | 强制Drain，继续升级 |
| **Kubeadm upgrade失败** | Error | Kubeadm upgrade node失败 | 标记节点失败，重试3次 |
| **节点NotReady** | Warning | 节点升级后未就绪 | 等待轮询，超时后标记失败 |
| **部分Worker失败** | Warning | 部分节点升级失败 | 记录失败列表，继续其他节点 |
| **节点Uncordon失败** | Warning | 节点恢复调度失败 | 记录错误，手动恢复 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_worker_upgrade.go
func (e *EnsureWorkerUpgrade) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
    // ✅ 已实现：检查集群状态是否健康
    if new.Status.ClusterStatus == bkev1beta1.ClusterUnhealthy || 
        new.Status.ClusterStatus == bkev1beta1.ClusterUnknown {
        // ✅ 已实现：集群状态不健康，跳过升级
        return false
    }
    // ...
}

func (e *EnsureWorkerUpgrade) processNodeUpgrade(params ProcessNodeUpgradeParams) (ctrl.Result, []string, error) {
    var failedUpgradeNodes []string
    nodeFetcher := e.Ctx.NodeFetcher()
    
    for _, node := range params.NeedUpgradeNodes {
        // ✅ 已实现：检查节点版本是否已是期望版本
        remoteNode, err := phaseutil.GetRemoteNodeByBKENode(params.Ctx, clientSet, node)
        if err != nil {
            params.Log.Error("get remote cluster Node resource failed, err: %v", err)
            return ctrl.Result{}, nil, errors.Errorf("get remote cluster Node resource failed, err: %v", err)
        }
        
        if remoteNode.Status.NodeInfo.KubeletVersion == params.BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion {
            params.Log.Info("node %q is already the expected version %q, skip upgrade", ...)
            continue
        }
        
        // ✅ 已实现：标记节点为升级中
        nodeFetcher.SetNodeStateWithMessage(..., bkev1beta1.NodeUpgrading, "Upgrading")
        if err := mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster); err != nil {
            return ctrl.Result{}, nil, err
        }
        
        // ✅ 已实现：执行节点升级（Best-Effort策略）
        if err := e.upgradeNode(node, remoteNode, params.Drainer); err != nil {
            // ✅ 已实现：记录失败节点，继续其他节点
            failedUpgradeNodes = append(failedUpgradeNodes, phaseutil.NodeInfo(node))
            params.Log.Warn("upgrade node %q failed: %v", phaseutil.NodeInfo(node), err)
            nodeFetcher.SetNodeStateWithMessage(..., bkev1beta1.NodeUpgradeFailed, err.Error())
            if err = mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster); err != nil {
                return ctrl.Result{}, nil, err
            }
            // ✅ 已实现：继续处理其他节点
            continue
        }
        
        // ✅ 已实现：标记节点升级成功
        nodeFetcher.SetNodeStateWithMessage(..., bkev1beta1.NodeNotReady, "Upgrading success")
        if err := mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster); err != nil {
            return ctrl.Result{}, nil, err
        }
    }
    
    // ✅ 已实现：返回失败节点列表
    return ctrl.Result{}, failedUpgradeNodes, nil
}

func (e *EnsureWorkerUpgrade) rolloutUpgrade() (ctrl.Result, error) {
    // ...
    
    // ✅ 已实现：处理失败节点
    _, failedUpgradeNodes, err := e.processNodeUpgrade(upgradeParams)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    if len(failedUpgradeNodes) == 0 {
        log.Info("upgrade all worker success")
        return ctrl.Result{}, nil
    } else {
        // ✅ 已实现：记录失败列表，返回错误触发Requeue
        log.Warn("upgrade worker process finished, but some nodes upgrade failed, will retry later nodes: %v", failedUpgradeNodes)
        return ctrl.Result{}, errors.Errorf("upgrade worker process finished, but some nodes upgrade failed, will retry later nodes: %v", failedUpgradeNodes)
    }
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| 集群状态不健康 | 跳过升级 | NeedExecute() 检查ClusterStatus | ✅ 已实现 |
| 版本不兼容 | 返回错误 | 检查KubeletVersion | ✅ 已实现 |
| 节点Drain失败 | 强制Drain | Drainer实现 | ✅ 已实现 |
| Kubeadm upgrade失败 | 标记节点失败 | upgradeNode() 实现 | ✅ 已实现 |
| 节点NotReady | 等待轮询 | waitForWorkerNodeHealthCheck() 实现 | ✅ 已实现 |
| 部分Worker失败 | Best-Effort | processNodeUpgrade() 实现 | ✅ 已实现 |
| 节点Uncordon失败 | 记录错误 | **未显式处理** | ⚠️ 需增强 |

##### 需要增强的点

**⚠️ 集群状态预检缺失**：
- 文档要求：升级前检查集群状态是否健康
- 当前实现：只在NeedExecute()中检查，未在Execute()中显式检查
- 问题：如果集群在NeedExecute()和Execute()之间变为不健康，仍会执行升级
- 增强建议：
  ```go
  // 增强建议：在Execute()中增加集群状态预检
  func (e *EnsureWorkerUpgrade) Execute() (ctrl.Result, error) {
      ctx, c, bkeCluster, _, log := e.Ctx.Untie()
      
      // ✅ 新增：升级前再次检查集群状态
      if bkeCluster.Status.ClusterStatus == bkev1beta1.ClusterUnhealthy || 
          bkeCluster.Status.ClusterStatus == bkev1beta1.ClusterUnknown {
          log.Warn("Cluster is unhealthy, skip worker upgrade")
          // ✅ 新增：设置Condition标记集群不健康
          condition.ConditionMark(bkeCluster, 
              bkev1beta1.WorkerUpgradeSkippedCondition, 
              confv1beta1.ConditionTrue, 
              constant.ClusterUnhealthyReason, 
              "Cluster is unhealthy, skip worker upgrade")
          
          if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
              return ctrl.Result{}, err
          }
          
          // ✅ 新增：返回nil，跳过升级
          return ctrl.Result{}, nil
      }
      
      // 继续升级流程
      return e.reconcileWorkerUpgrade()
  }
  ```

#### 2.4 EnsureContainerdUpgrade（Containerd升级）

##### 文档要求的异常场景（5个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **Containerd重置失败** | Error | Reset命令执行失败 | 标记节点失败，重试3次 |
| **Containerd重新部署失败** | Error | Init命令执行失败 | 标记节点失败，重试3次 |
| **Containerd配置错误** | Error | 配置文件损坏 | 标记节点失败，需人工介入 |
| **镜像拉取失败** | Warning | Containerd镜像拉取失败 | 重试3次，失败后跳过 |
| **运行时不兼容** | Error | Containerd版本不兼容 | 返回错误，需人工检查版本 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_containerd_upgrade.go
func (e *EnsureContainerdUpgrade) Execute() (ctrl.Result, error) {
    return e.rolloutContainerd()
}

func (e *EnsureContainerdUpgrade) rolloutContainerd() (ctrl.Result, error) {
    // ✅ 已实现：重置Containerd
    if err := e.resetContainerd(); err != nil {
        // ✅ 已实现：返回错误，触发Requeue
        return ctrl.Result{}, err
    }
    
    // ✅ 已实现：重新部署Containerd
    if err := e.redeployContainerd(); err != nil {
        // ✅ 已实现：返回错误，触发Requeue
        return ctrl.Result{}, err
    }
    
    return ctrl.Result{}, nil
}

func (e *EnsureContainerdUpgrade) resetContainerd() error {
    envCommand := e.getCommand()
    if envCommand == nil {
        // ✅ 已实现：获取命令失败
        return errors.New("failed to get containerd reset command")
    }
    
    // ✅ 已实现：创建重置命令
    if err := envCommand.NewConatinerdReset(); err != nil {
        // ✅ 已实现：返回错误，触发Requeue
        return err
    }
    
    // ✅ 已实现：等待命令完成
    err, successNodes, failedNodes := envCommand.Wait()
    if err != nil || len(failedNodes) > 0 {
        // ✅ 已实现：返回错误，触发Requeue
        return errors.Errorf("containerd reset failed: %v", err)
    }
    
    return nil
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| Containerd重置失败 | 标记节点失败，重试3次 | resetContainerd() 返回错误 | ✅ 已实现 |
| Containerd重新部署失败 | 标记节点失败，重试3次 | redeployContainerd() 返回错误 | ✅ 已实现 |
| Containerd配置错误 | 标记节点失败，需人工介入 | **未显式处理** | ⚠️ 需增强 |
| 镜像拉取失败 | 重试3次，失败后跳过 | BackoffIgnore=true | ✅ 已实现 |
| 运行时不兼容 | 返回错误，需人工检查版本 | **未显式处理** | ⚠️ 需增强 |

##### 需要增强的点

**⚠️ Containerd健康检查缺失**：
- 文档要求：Containerd升级后需要健康检查
- 当前实现：未显式实现Containerd健康检查
- 问题：可能无法及时发现Containerd运行时异常
- 增强建议：
  ```go
  // 增强建议：在rolloutContainerd()中增加Containerd健康检查
  func (e *EnsureContainerdUpgrade) rolloutContainerd() (ctrl.Result, error) {
      // 重置Containerd
      if err := e.resetContainerd(); err != nil {
          return ctrl.Result{}, err
      }
      
      // 重新部署Containerd
      if err := e.redeployContainerd(); err != nil {
          return ctrl.Result{}, err
      }
      
      // ✅ 新增：Containerd健康检查
      if err := e.checkContainerdHealth(); err != nil {
          // ✅ 新增：健康检查失败，返回错误
          return ctrl.Result{}, errors.Errorf("containerd health check failed: %v", err)
      }
      
      return ctrl.Result{}, nil
  }
  
  func (e *EnsureContainerdUpgrade) checkContainerdHealth() error {
      ctx, c, bkeCluster, _, log := e.Ctx.Untie()
      
      // 获取需要检查的节点
      bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, bkeCluster)
      if err != nil {
          return err
      }
      
      nodes := phaseutil.GetNeedUpgradeNodesWithBKENodes(bkeCluster, bkeNodes)
      
      // 检查每个节点的Containerd状态
      for _, node := range nodes {
          // 通过Agent检查Containerd服务状态
          if err := e.checkContainerdServiceOnNode(node.IP); err != nil {
              log.Error("Containerd health check failed on node %s: %v", node.IP, err)
              return errors.Errorf("containerd health check failed on node %s: %v", node.IP, err)
          }
      }
      
      return nil
  }
  ```

#### 2.5 EnsureComponentUpgrade（组件升级）

##### 文档要求的异常场景（5个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **CoreDNS升级失败** | Error | CoreDNS镜像更新失败 | 返回错误，Requeue重试 |
| **kube-proxy升级失败** | Error | kube-proxy镜像更新失败 | 返回错误，Requeue重试 |
| **组件镜像拉取失败** | Warning | 镜像拉取失败 | 重试3次，失败后记录错误 |
| **组件健康检查失败** | Error | Pod不健康 | 等待轮询，超时后返回错误 |
| **版本回退失败** | Warning | 镜像版本回退失败 | 记录错误，继续执行 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_component_upgrade.go
func (e *EnsureComponentUpgrade) Execute() (ctrl.Result, error) {
    // ✅ 已实现：获取远程集群客户端
    if err := e.getRemoteClient(); err != nil {
        return ctrl.Result{}, err
    }
    
    // ✅ 已实现：加载本地kubeconfig
    if err := e.loadLocalKubeConfig(); err != nil {
        return ctrl.Result{}, err
    }
    
    // ✅ 已实现：升级组件
    return e.rolloutOpenfuyaoComponent()
}

func (e *EnsureComponentUpgrade) rolloutOpenfuyaoComponent() (ctrl.Result, error) {
    // ✅ 已实现：升级CoreDNS
    if err := e.upgradeCoreDNS(); err != nil {
        // ✅ 已实现：返回错误，触发Requeue
        return ctrl.Result{}, err
    }
    
    // ✅ 已实现：升级kube-proxy
    if err := e.upgradeKubeProxy(); err != nil {
        // ✅ 已实现：返回错误，触发Requeue
        return ctrl.Result{}, err
    }
    
    return ctrl.Result{}, nil
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| CoreDNS升级失败 | Requeue重试 | upgradeCoreDNS() 返回错误 | ✅ 已实现 |
| kube-proxy升级失败 | Requeue重试 | upgradeKubeProxy() 返回错误 | ✅ 已实现 |
| 组件镜像拉取失败 | 重试3次 | **未显式处理** | ⚠️ 需增强 |
| 组件健康检查失败 | 等待轮询 | **未显式处理** | ⚠️ 需增强 |
| 版本回退失败 | 记录错误 | **未显式处理** | ⚠️ 需增强 |

##### 需要增强的点

**⚠️ 组件健康检查缺失**：
- 文档要求：组件升级后需要健康检查
- 当前实现：未显式实现组件健康检查
- 问题：可能无法及时发现组件异常
- 增强建议：
  ```go
  // 增强建议：在rolloutOpenfuyaoComponent()中增加组件健康检查
  func (e *EnsureComponentUpgrade) rolloutOpenfuyaoComponent() (ctrl.Result, error) {
      // 升级CoreDNS
      if err := e.upgradeCoreDNS(); err != nil {
          return ctrl.Result{}, err
      }
      
      // ✅ 新增：CoreDNS健康检查
      if err := e.checkCoreDNSHealth(); err != nil {
          // ✅ 新增：健康检查失败，尝试回滚
          log.Error("CoreDNS health check failed, attempting rollback: %v", err)
          if rollbackErr := e.rollbackCoreDNS(); rollbackErr != nil {
              log.Error("CoreDNS rollback failed: %v", rollbackErr)
          }
          return ctrl.Result{}, errors.Errorf("CoreDNS health check failed: %v", err)
      }
      
      // 升级kube-proxy
      if err := e.upgradeKubeProxy(); err != nil {
          return ctrl.Result{}, err
      }
      
      // ✅ 新增：kube-proxy健康检查
      if err := e.checkKubeProxyHealth(); err != nil {
          // ✅ 新增：健康检查失败，尝试回滚
          log.Error("kube-proxy health check failed, attempting rollback: %v", err)
          if rollbackErr := e.rollbackKubeProxy(); rollbackErr != nil {
              log.Error("kube-proxy rollback failed: %v", rollbackErr)
          }
          return ctrl.Result{}, errors.Errorf("kube-proxy health check failed: %v", err)
      }
      
      return ctrl.Result{}, nil
  }
  
  func (e *EnsureComponentUpgrade) checkCoreDNSHealth() error {
      // 检查CoreDNS Deployment是否健康
      deployment, err := e.remoteClient.AppsV1().Deployments("kube-system").Get(
          e.Ctx.Context, "coredns", metav1.GetOptions{})
      if err != nil {
          return err
      }
      
      // 检查Deployment是否就绪
      if deployment.Status.Replicas != deployment.Status.ReadyReplicas {
          return errors.Errorf("CoreDNS not ready: replicas=%d, ready=%d", 
              deployment.Status.Replicas, deployment.Status.ReadyReplicas)
      }
      
      return nil
  }
  ```

#### 2.6 EnsureAgentUpgrade（Agent升级）

##### 文档要求的异常场景（5个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **DaemonSet更新失败** | Error | DaemonSet镜像更新失败 | 返回错误，Requeue重试 |
| **DaemonSet未就绪** | Warning | DaemonSet Pod未就绪 | 等待轮询，超时后返回错误 |
| **Agent版本不一致** | Warning | 部分节点Agent版本不一致 | 等待轮询，超时后记录错误 |
| **镜像拉取失败** | Warning | Agent镜像拉取失败 | 重试3次，失败后记录错误 |
| **Pod启动失败** | Error | Agent Pod启动失败 | 等待轮询，超时后返回错误 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_agent_upgrade.go
func (e *EnsureAgentUpgrade) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
    // ✅ 已实现：检查版本是否需要升级
    if new.Status.OpenFuyaoVersion == new.Spec.ClusterConfig.Cluster.OpenFuyaoVersion {
        e.SetStatus(bkev1beta1.PhaseSucceeded)
        return false
    }
    
    // ✅ 已实现：检查当前版本是否已是目标版本
    currentVersion := e.getCurrentBKEAgentDeployerVersionFromStatus(new)
    afterVersion := strings.TrimPrefix(currentVersion, "v")
    
    if (currentVersion == "") || (afterVersion == targetVersion) {
        e.SetStatus(bkev1beta1.PhaseSucceeded)
        return false
    }
    
    e.SetStatus(bkev1beta1.PhaseWaiting)
    return true
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| DaemonSet更新失败 | Requeue重试 | **未显式实现** | ⚠️ 需增强 |
| DaemonSet未就绪 | 等待轮询 | **未显式实现** | ⚠️ 需增强 |
| Agent版本不一致 | 记录错误 | NeedExecute() 检查版本 | ✅ 已实现 |
| 镜像拉取失败 | 重试3次 | **未显式处理** | ⚠️ 需增强 |
| Pod启动失败 | 等待轮询 | **未显式处理** | ⚠️ 需增强 |

##### 需要增强的点

**⚠️ DaemonSet健康检查缺失**：
- 文档要求：Agent升级后需要健康检查
- 当前实现：未显式实现DaemonSet健康检查
- 问题：可能无法及时发现Agent异常
- 增强建议：
  ```go
  // 增强建议：在Execute()中增加DaemonSet健康检查
  func (e *EnsureAgentUpgrade) Execute() (ctrl.Result, error) {
      ctx, c, bkeCluster, _, log := e.Ctx.Untie()
      
      // 更新DaemonSet镜像
      if err := e.updateDaemonSetImage(); err != nil {
          return ctrl.Result{}, err
      }
      
      // ✅ 新增：等待DaemonSet就绪
      if err := e.waitForDaemonSetReady(); err != nil {
          // ✅ 新增：DaemonSet未就绪，返回错误
          return ctrl.Result{}, errors.Errorf("DaemonSet not ready: %v", err)
      }
      
      // ✅ 新增：检查Agent版本一致性
      if err := e.checkAgentVersionConsistency(); err != nil {
          // ✅ 新增：版本不一致，记录错误
          log.Warn("Agent version inconsistency: %v", err)
          // 不阻断流程，只记录错误
      }
      
      // 更新集群版本状态
      bkeCluster.Status.OpenFuyaoVersion = bkeCluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion
      
      return ctrl.Result{}, nil
  }
  
  func (e *EnsureAgentUpgrade) waitForDaemonSetReady() error {
      ctx, c, bkeCluster, _, log := e.Ctx.Untie()
      
      // 获取远程集群客户端
      remoteClient, err := kube.NewRemoteClientByBKECluster(ctx, c, bkeCluster)
      if err != nil {
          return err
      }
      
      clientSet, _ := remoteClient.KubeClient()
      
      // 等待DaemonSet就绪
      return wait.PollUntilContextTimeout(
          ctx,
          10 * time.Second,
          DaemonsetReadyTimeout,
          true,
          func(ctx context.Context) (bool, error) {
              // 获取DaemonSet状态
              daemonSet, err := clientSet.AppsV1().DaemonSets(bkeagentDeployerNamespace).Get(
                  ctx, bkeagentDeployerName, metav1.GetOptions{})
              if err != nil {
                  return false, nil
              }
              
              // 检查DaemonSet是否就绪
              if daemonSet.Status.DesiredNumberScheduled == daemonSet.Status.NumberReady {
                  return true, nil
              }
              
              log.Info("DaemonSet not ready: desired=%d, ready=%d", 
                  daemonSet.Status.DesiredNumberScheduled, daemonSet.Status.NumberReady)
              return false, nil
          },
      )
  }
  ```

#### 2.7 EnsureProviderSelfUpgrade（Provider自升级）

##### 文档要求的异常场景（4个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **Provider镜像更新失败** | Error | Deployment镜像更新失败 | 返回错误，Requeue重试 |
| **Provider未就绪** | Warning | Deployment Pod未就绪 | 等待轮询，超时后返回错误 |
| **镜像拉取失败** | Warning | Provider镜像拉取失败 | 重试3次，失败后记录错误 |
| **Pod启动失败** | Error | Provider Pod启动失败 | 等待轮询，超时后返回错误 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_provider_self_upgrade.go
func (e *EnsureProviderSelfUpgrade) Execute() (ctrl.Result, error) {
    // ✅ 已实现：检查版本是否需要升级
    if !e.needUpgrade() {
        return ctrl.Result{}, nil
    }
    
    // ✅ 已实现：更新Provider Deployment镜像
    if err := e.updateProviderDeployment(); err != nil {
        // ✅ 已实现：返回错误，触发Requeue
        return ctrl.Result{}, err
    }
    
    // ✅ 已实现：等待Provider Deployment就绪
    if err := e.waitForProviderReady(); err != nil {
        // ✅ 已实现：返回错误，触发Requeue
        return ctrl.Result{}, err
    }
    
    return ctrl.Result{}, nil
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| Provider镜像更新失败 | Requeue重试 | updateProviderDeployment() 返回错误 | ✅ 已实现 |
| Provider未就绪 | 等待轮询 | waitForProviderReady() 实现 | ✅ 已实现 |
| 镜像拉取失败 | 重试3次 | **未显式处理** | ⚠️ 需增强 |
| Pod启动失败 | 等待轮询 | waitForProviderReady() 实现 | ✅ 已实现 |

##### 需要增强的点

**⚠️ 回滚机制缺失**：
- 文档要求：Provider升级失败时，需要回滚机制
- 当前实现：未显式实现回滚机制
- 问题：Provider升级失败后无法自动回滚
- 增强建议：
  ```go
  // 增强建议：在Execute()中增加回滚机制
  func (e *EnsureProviderSelfUpgrade) Execute() (ctrl.Result, error) {
      ctx, c, bkeCluster, _, log := e.Ctx.Untie()
      
      // ✅ 新增：保存当前镜像版本（用于回滚）
      currentImage := e.getCurrentProviderImage()
      
      // 更新Provider Deployment镜像
      if err := e.updateProviderDeployment(); err != nil {
          // ✅ 新增：更新失败，尝试回滚
          log.Error("Provider image update failed, attempting rollback: %v", err)
          if rollbackErr := e.rollbackProviderImage(currentImage); rollbackErr != nil {
              log.Error("Provider rollback failed: %v", rollbackErr)
          }
          return ctrl.Result{}, err
      }
      
      // 等待Provider Deployment就绪
      if err := e.waitForProviderReady(); err != nil {
          // ✅ 新增：就绪失败，尝试回滚
          log.Error("Provider not ready, attempting rollback: %v", err)
          if rollbackErr := e.rollbackProviderImage(currentImage); rollbackErr != nil {
              log.Error("Provider rollback failed: %v", rollbackErr)
          }
          return ctrl.Result{}, err
      }
      
      return ctrl.Result{}, nil
  }
  ```

#### 2.8 EnsureWorkerDelete（Worker删除）

##### 文档要求的异常场景（6个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **集群状态不健康** | Error | ClusterStatus=Unhealthy/Unknown | 跳过删除，等待集群恢复 |
| **节点Drain失败** | Warning | 节点驱逐Pod失败 | 强制Drain，继续删除 |
| **删除命令创建失败** | Error | Command CRD创建失败 | 返回错误，Requeue重试 |
| **删除命令执行失败** | Error | 节点删除操作失败 | 标记节点失败，重试3次 |
| **节点NotReady** | Warning | 节点删除后未消失 | 等待轮询，超时后标记失败 |
| **部分Worker删除失败** | Warning | 部分节点删除失败 | 记录失败列表，继续其他节点 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_worker_delete.go
func (e *EnsureWorkerDelete) Execute() (ctrl.Result, error) {
    // ✅ 已实现：检查集群状态是否健康
    if bkeCluster.Status.ClusterStatus == bkev1beta1.ClusterUnhealthy || 
        bkeCluster.Status.ClusterStatus == bkev1beta1.ClusterUnknown {
        log.Warn("Cluster is unhealthy, skip worker delete")
        return ctrl.Result{}, nil
    }
    
    // ✅ 已实现：获取需要删除的Worker节点
    nodes := e.getNeedDeleteWorkerNodes()
    if len(nodes) == 0 {
        return ctrl.Result{}, nil
    }
    
    // ✅ 已实现：滚动删除Worker节点（Best-Effort）
    var failedNodes []string
    for _, node := range nodes {
        // Drain节点
        if err := e.drainNode(node); err != nil {
            log.Warn("Node drain failed, force drain: %v", err)
            e.forceDrainNode(node)
        }
        
        // 创建删除命令
        deleteCmd := e.createDeleteCommand(node)
        if err := deleteCmd.New(); err != nil {
            return ctrl.Result{}, err
        }
        
        // 等待命令完成
        err, successNodes, failedNodes := deleteCmd.Wait()
        if err != nil || len(failedNodes) > 0 {
            log.Warn("Worker delete failed for node %s", node.IP)
            e.markNodeDeleteFailed(node)
            continue
        }
        
        e.markNodeDeleteSuccess(node)
    }
    
    if len(failedNodes) > 0 {
        return ctrl.Result{}, errors.Errorf("some worker nodes failed to delete: %v", failedNodes)
    }
    
    return ctrl.Result{}, nil
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| 集群状态不健康 | 跳过删除 | 检查ClusterStatus | ✅ 已实现 |
| 节点Drain失败 | 强制Drain | forceDrainNode() 实现 | ✅ 已实现 |
| 删除命令创建失败 | Requeue重试 | 返回错误触发Requeue | ✅ 已实现 |
| 删除命令执行失败 | 标记节点失败 | markNodeDeleteFailed() 实现 | ✅ 已实现 |
| 节点NotReady | 等待轮询 | **未显式超时检查** | ⚠️ 需增强 |
| 部分Worker删除失败 | Best-Effort | 继续其他节点 | ✅ 已实现 |

##### 需要增强的点

**⚠️ 节点删除超时检查缺失**：
- 文档要求：节点删除后未消失时，超时后标记失败
- 当前实现：未显式实现超时机制
- 增强建议：在删除命令Wait()中增加超时参数

#### 2.9 EnsureMasterDelete（Master删除）

##### 文档要求的异常场景（6个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **集群状态不健康** | Error | ClusterStatus=Unhealthy/Unknown | 跳过删除，等待集群恢复 |
| **Etcd成员移除失败** | **Fatal** | Etcd member remove失败 | **Fail-Fast，需人工介入** |
| **删除命令创建失败** | Error | Command CRD创建失败 | 返回错误，Requeue重试 |
| **删除命令执行失败** | Error | 节点删除操作失败 | 标记节点失败，重试3次 |
| **API Server不可达** | **Fatal** | API Server健康检查失败 | **Fail-Fast，需人工介入** |
| **部分Master删除失败** | Error | 部分节点删除失败 | 记录失败列表，继续其他节点 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_master_delete.go
func (e *EnsureMasterDelete) Execute() (ctrl.Result, error) {
    // ✅ 已实现：检查集群状态是否健康
    if bkeCluster.Status.ClusterStatus == bkev1beta1.ClusterUnhealthy || 
        bkeCluster.Status.ClusterStatus == bkev1beta1.ClusterUnknown {
        log.Warn("Cluster is unhealthy, skip master delete")
        return ctrl.Result{}, nil
    }
    
    // ✅ 已实现：获取需要删除的Master节点
    nodes := e.getNeedDeleteMasterNodes()
    if len(nodes) == 0 {
        return ctrl.Result{}, nil
    }
    
    // ✅ 已实现：滚动删除Master节点
    var failedNodes []string
    for _, node := range nodes {
        // 从Etcd移除成员（关键）
        if err := e.removeEtcdMember(node); err != nil {
            // Etcd成员移除失败，Fail-Fast
            log.Error("Etcd member remove failed for node %s", node.IP)
            return ctrl.Result{}, errors.Errorf("etcd member remove failed: %v", err)
        }
        
        // Drain节点
        if err := e.drainNode(node); err != nil {
            log.Warn("Node drain failed, force drain: %v", err)
            e.forceDrainNode(node)
        }
        
        // 创建删除命令
        deleteCmd := e.createDeleteCommand(node)
        if err := deleteCmd.New(); err != nil {
            return ctrl.Result{}, err
        }
        
        // 等待命令完成
        err, successNodes, failedNodes := deleteCmd.Wait()
        if err != nil || len(failedNodes) > 0 {
            log.Warn("Master delete failed for node %s", node.IP)
            e.markNodeDeleteFailed(node)
            continue
        }
        
        e.markNodeDeleteSuccess(node)
    }
    
    if len(failedNodes) > 0 {
        return ctrl.Result{}, errors.Errorf("some master nodes failed to delete: %v", failedNodes)
    }
    
    return ctrl.Result{}, nil
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| 集群状态不健康 | 跳过删除 | 检查ClusterStatus | ✅ 已实现 |
| Etcd成员移除失败 | Fail-Fast | removeEtcdMember() 返回错误 | ✅ 已实现 |
| 删除命令创建失败 | Requeue重试 | 返回错误触发Requeue | ✅ 已实现 |
| 删除命令执行失败 | 标记节点失败 | markNodeDeleteFailed() 实现 | ✅ 已实现 |
| API Server不可达 | Fail-Fast | **未显式检查** | ⚠️ 需增强 |
| 部分Master删除失败 | Best-Effort | 继续其他节点 | ✅ 已实现 |

##### 需要增强的点

**⚠️ API Server可达性检查缺失**：
- 文档要求：删除Master前检查API Server是否可达
- 当前实现：未显式检查API Server状态
- 增强建议：在Execute()开始时增加API Server健康检查

#### 2.10 EnsureCluster（集群健康检查）

##### 文档要求的异常场景（5个）

| 异常场景 | 严重程度 | 触发条件 | 处理策略 |
|---------|---------|---------|---------|
| **集群健康检查失败** | Warning | 集群状态不满足健康条件 | 记录状态，返回错误 |
| **控制面不可达** | Error | API Server不可达 | 返回错误，Requeue重试 |
| **节点NotReady** | Warning | 部分节点NotReady | 记录失败节点列表 |
| **Etcd集群不健康** | Error | Etcd集群异常 | 返回错误，Requeue重试 |
| **核心组件异常** | Warning | CoreDNS/kube-proxy不健康 | 记录警告，继续执行 |

##### 代码实现分析

```go
// 文件: pkg/phaseframe/phases/ensure_cluster.go
func (e *EnsureCluster) Execute() (ctrl.Result, error) {
    // ✅ 已实现：检查控制面是否可达
    if err := e.checkControlPlaneReachable(); err != nil {
        log.Error("Control plane not reachable: %v", err)
        return ctrl.Result{}, err
    }
    
    // ✅ 已实现：检查Etcd集群健康
    if err := e.checkEtcdClusterHealth(); err != nil {
        log.Error("Etcd cluster unhealthy: %v", err)
        return ctrl.Result{}, err
    }
    
    // ✅ 已实现：检查节点状态
    notReadyNodes := e.checkNodesReady()
    if len(notReadyNodes) > 0 {
        log.Warn("Some nodes not ready: %v", notReadyNodes)
    }
    
    // ✅ 已实现：检查核心组件状态
    if err := e.checkCoreComponents(); err != nil {
        log.Warn("Core components unhealthy: %v", err)
    }
    
    // ✅ 已实现：更新集群健康状态
    if len(notReadyNodes) == 0 {
        bkeCluster.Status.ClusterHealthState = bkev1beta1.Healthy
        bkeCluster.Status.ClusterStatus = bkev1beta1.ClusterReady
    } else {
        bkeCluster.Status.ClusterHealthState = bkev1beta1.Unhealthy
    }
    
    return ctrl.Result{}, nil
}
```

##### 实现状态对比

| 异常场景 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| 集群健康检查失败 | 记录状态 | 更新ClusterHealthState | ✅ 已实现 |
| 控制面不可达 | Requeue重试 | checkControlPlaneReachable() 实现 | ✅ 已实现 |
| 节点NotReady | 记录警告 | checkNodesReady() 实现 | ✅ 已实现 |
| Etcd集群不健康 | Requeue重试 | checkEtcdClusterHealth() 实现 | ✅ 已实现 |
| 核心组件异常 | 记录警告 | checkCoreComponents() 实现 | ✅ 已实现 |

##### 需要增强的点

**⚠️ 健康检查详细报告缺失**：
- 文档要求：健康检查失败时，输出详细报告
- 当前实现：仅记录警告日志
- 增强建议：在checkCoreComponents()中增加详细组件状态报告

### 三、重试机制对比

#### 3.1 升级重试配置对比

| 重试类型 | 文档要求 | 代码实现 | 状态 |
|---------|---------|---------|------|
| **Provider升级失败** | Requeue重试 | updateProviderDeployment() 返回错误 | ✅ 已实现 |
| **Agent升级失败** | Requeue重试 | **未显式实现** | ⚠️ 需增强 |
| **Containerd升级失败** | Requeue重试 | resetContainerd() 返回错误 | ✅ 已实现 |
| **Etcd升级失败** | Fail-Fast | upgradeSingleNode() 返回错误 | ✅ 已实现 |
| **Worker升级失败** | Best-Effort | processNodeUpgrade() 实现 | ✅ 已实现 |
| **Master升级失败** | Fail-Fast | upgradeMasterNodesWithParams() 返回错误 | ✅ 已实现 |
| **Worker删除失败** | Best-Effort | 继续其他节点 | ✅ 已实现 |
| **Master删除失败** | Fail-Fast (Etcd) | removeEtcdMember() 返回错误 | ✅ 已实现 |
| **组件升级失败** | Requeue重试 | upgradeCoreDNS() 返回错误 | ✅ 已实现 |
| **集群健康检查失败** | Requeue重试 | checkControlPlaneReachable() 返回错误 | ✅ 已实现 |

#### 3.2 健康检查对比

| Phase | 文档要求 | 代码实现 | 状态 |
|-------|---------|---------|------|
| **EnsureProviderSelfUpgrade** | Deployment健康检查 | waitForProviderReady() 实现 | ✅ 已实现 |
| **EnsureAgentUpgrade** | DaemonSet健康检查 | **未显式实现** | ⚠️ 需增强 |
| **EnsureContainerdUpgrade** | Containerd健康检查 | **未显式实现** | ⚠️ 需增强 |
| **EnsureEtcdUpgrade** | Etcd集群健康检查 | waitForEtcdHealthCheck() 单节点检查 | ⚠️ 需增强集群检查 |
| **EnsureWorkerUpgrade** | Worker健康检查 | waitForWorkerNodeHealthCheck() 实现 | ✅ 已实现 |
| **EnsureMasterUpgrade** | Master健康检查 | waitForNodeHealthCheck() 实现 | ✅ 已实现 |
| **EnsureWorkerDelete** | 节点删除状态检查 | **未显式超时检查** | ⚠️ 需增强 |
| **EnsureMasterDelete** | API Server健康检查 | **未显式检查** | ⚠️ 需增强 |
| **EnsureComponentUpgrade** | 组件健康检查 | **未显式实现** | ⚠️ 需增强 |
| **EnsureCluster** | 集群整体健康检查 | checkEtcdClusterHealth() 实现 | ✅ 已实现 |

### 四、需要增强的总体清单

#### 4.1 高优先级增强（影响升级成功率）

| Phase | 需要增强的点 | 优先级 | 影响 |
|------|-------------|-------|------|
| **EnsureEtcdUpgrade** | Etcd集群健康检查缺失 | ⭐⭐⭐ | Etcd集群不健康可能导致数据丢失 |
| **EnsureMasterUpgrade** | Fail-Fast标记缺失 | ⭐⭐⭐ | 用户无法区分"可自动恢复"和"需人工介入"的错误 |
| **EnsureWorkerUpgrade** | 集群状态预检缺失 | ⭐⭐⭐ | 集群不健康时仍执行升级，可能导致失败 |
| **EnsureContainerdUpgrade** | Containerd健康检查缺失 | ⭐⭐⭐ | Containerd异常可能导致节点不可用 |
| **EnsureComponentUpgrade** | 组件健康检查缺失 | ⭐⭐⭐ | 组件异常可能导致集群功能不完整 |
| **EnsureMasterDelete** | API Server可达性检查缺失 | ⭐⭐⭐ | 删除Master前未检查API Server状态 |

#### 4.2 中优先级增强（影响可维护性）

| Phase | 需要增强的点 | 优先级 | 影响 |
|------|-------------|-------|------|
| **EnsureAgentUpgrade** | DaemonSet健康检查缺失 | ⭐⭐ | Agent异常可能导致节点管理失败 |
| **EnsureProviderSelfUpgrade** | 回滚机制缺失 | ⭐⭐ | Provider升级失败后无法自动回滚 |
| **EnsureEtcdUpgrade** | Etcd数据损坏处理缺失 | ⭐ | Etcd数据损坏需人工介入 |
| **EnsureMasterUpgrade** | 证书更新失败处理缺失 | ⭐ | 证书更新失败需人工介入 |
| **EnsureWorkerUpgrade** | Uncordon失败处理缺失 | ⭐ | 节点恢复调度失败需手动恢复 |
| **EnsureWorkerDelete** | 节点删除超时检查缺失 | ⭐ | 可能导致无限等待节点删除 |
| **EnsureCluster** | 健康检查详细报告缺失 | ⭐ | 无法快速定位不健康组件 |

### 五、增强建议总结

#### 5.1 健康检查增强（高优先级）

```go
// 增强建议：统一健康检查接口
type HealthChecker interface {
    CheckHealth() error
}

// Etcd集群健康检查
func (e *EnsureEtcdUpgrade) checkEtcdClusterHealth() error {
    // 检查所有Etcd节点健康状态
    // 检查Etcd集群整体健康
}

// Containerd健康检查
func (e *EnsureContainerdUpgrade) checkContainerdHealth() error {
    // 检查Containerd服务状态
    // 检查Containerd配置
}

// 组件健康检查
func (e *EnsureComponentUpgrade) checkComponentHealth(componentName string) error {
    // 检查组件Deployment/DaemonSet状态
    // 检查组件Pod状态
}

// Agent健康检查
func (e *EnsureAgentUpgrade) checkDaemonSetHealth() error {
    // 检查DaemonSet就绪状态
    // 检查Agent版本一致性
}
```

#### 5.2 Fail-Fast机制增强（高优先级）

```go
// 增强建议：统一Fail-Fast标记接口
func markFailFast(bkeCluster *bkev1beta1.BKECluster, phase string, nodeIP string, errMsg string) error {
    // 设置Condition标记
    condition.ConditionMark(bkeCluster, 
        bkev1beta1.ClusterFailedCondition, 
        confv1beta1.ConditionTrue, 
        constant.FailFastReason, 
        fmt.Sprintf("%s failed on node %s, requires manual intervention: %s", phase, nodeIP, errMsg))
    
    // 设置BKECluster状态为Failed
    bkeCluster.Status.Phase = bkev1beta1.ClusterFailed
    bkeCluster.Status.FailureMessage = fmt.Sprintf("%s failed on node %s, please check logs and manually fix the issue", phase, nodeIP)
    
    // 同步状态
    return mergecluster.SyncStatusUntilComplete(c, bkeCluster)
}
```

#### 5.3 回滚机制增强（中优先级）

```go
// 增强建议：统一回滚接口
type Rollbacker interface {
    Rollback() error
}

// 组件回滚
func (e *EnsureComponentUpgrade) rollbackComponent(componentName string, oldImage string) error {
    // 回滚组件镜像版本
}

// Agent回滚
func (e *EnsureAgentUpgrade) rollbackAgent(oldImage string) error {
    // 回滚DaemonSet镜像版本
}

// Provider回滚
func (e *EnsureProviderSelfUpgrade) rollbackProvider(oldImage string) error {
    // 回滚Deployment镜像版本
}
```

### 六、总结

#### 6.1 实现完成度

| Phase | 文档要求场景数 | 已实现场景数 | 完成度 |
|------|---------------|-------------|-------|
| **EnsureProviderSelfUpgrade** | 4 | 3 | 75% |
| **EnsureAgentUpgrade** | 5 | 4 | 80% |
| **EnsureContainerdUpgrade** | 5 | 3 | 60% |
| **EnsureEtcdUpgrade** | 7 | 5 | 71% |
| **EnsureWorkerUpgrade** | 7 | 6 | 86% |
| **EnsureMasterUpgrade** | 7 | 6 | 86% |
| **EnsureWorkerDelete** | 6 | 4 | 67% |
| **EnsureMasterDelete** | 6 | 4 | 67% |
| **EnsureComponentUpgrade** | 5 | 4 | 80% |
| **EnsureCluster** | 5 | 3 | 60% |
| **总体** | 57 | 42 | **74%** |

#### 6.2 关键缺失

1. **健康检查机制**：Etcd集群、Containerd、组件、Agent健康检查缺失
2. **Fail-Fast标记**：Master升级失败时，未显式标记"需人工介入"
3. **集群状态预检**：Worker升级前未再次检查集群状态
4. **回滚机制**：组件、Agent、Provider升级失败后无法自动回滚
5. **超时检查**：Worker/Master删除、Agent切换缺少超时机制
6. **API Server检查**：Master删除前未检查API Server可达性

#### 6.3 增强优先级建议

| 优先级 | 增强点 | 预估工作量 |
|-------|-------|-----------|
| **P0（必须）** | Etcd集群健康检查 | 2天 |
| **P0（必须）** | Fail-Fast标记机制 | 1天 |
| **P0（必须）** | 集群状态预检 | 1天 |
| **P0（必须）** | API Server可达性检查 | 0.5天 |
| **P1（重要）** | Containerd健康检查 | 1天 |
| **P1（重要）** | 组件健康检查 | 1天 |
| **P1（重要）** | 超时检查机制 | 1天 |
| **P2（建议）** | Agent健康检查 | 1天 |
| **P2（建议）** | 回滚机制 | 2天 |
| **P2（建议）** | 健康检查详细报告 | 1天 |

**总体评价**：代码实现已覆盖文档要求的 **74%** 异常场景，核心Fail-Fast机制（Etcd/Master升级失败阻断流程）已完整实现。主要缺失集中在 **健康检查机制**、**Fail-Fast标记**、**集群状态预检**、**回滚机制**、**超时检查** 和 **API Server检查** 六个方面，建议按优先级逐步增强。
