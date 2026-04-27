# etcd 逐节点升级 + 首节点备份方案

## 1. 核心设计思路

| 设计要点 | 说明 |
|---------|------|
| **节点角色识别** | 通过模板变量识别节点角色（master/etcd）和节点序号 |
| **首节点判断** | 通过 `{{.NodeRoles}}` 或自定义标签判断是否为首节点 |
| **备份条件执行** | 只在首节点执行备份步骤 |
| **逐节点串行化** | 通过 ActionStrategy.ExecutionMode = Serial 实现逐节点执行 |
| **健康检查门控** | 前一个节点健康检查通过后才执行下一个节点 |

## 2. 标准实现方案

### 2.1 ComponentVersion 完整定义

```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: etcd-v3.5.12
spec:
  componentName: etcd
  scope: Node
  dependencies:
    - componentName: certs
      phase: Install
    - componentName: nodesEnv
      phase: Install
  nodeSelector:
    roles: [etcd, master]
  
  versions:
    - version: v3.5.12
      # 安装动作
      installAction:
        preCheck:
          steps:
            - name: check-etcd-data-dir
              type: Script
              script: |
                if [ ! -d "{{.EtcdConfig.DataDir}}" ]; then
                  echo "Etcd data dir not found"
                  exit 1
                fi
              timeout: 10s
        
        # 核心：逐节点串行执行策略
        strategy:
          executionMode: Serial  # 串行执行，逐节点
          batchSize: 1          # 每次1个节点
          batchInterval: 60s    # 节点间间隔60s
          waitForCompletion: true  # 等待前一个节点完成
        
        steps:
          # 步骤1：仅在第一个 etcd 节点执行备份
          - name: backup-etcd-on-first-node
            type: Script
            # 条件判断：只在第一个 etcd 节点执行
            condition: |
              {{if and (index .NodeRoles "etcd") (eq .NodeIndex 0)}}true{{else}}false{{end}}
            script: |
              # 备份 etcd 数据
              ETCDCTL_API=3 etcdctl snapshot save {{.EtcdConfig.DataDir}}/snapshot-$(date +%Y%m%d-%H%M%S).db \
                --endpoints=https://127.0.0.1:2379 \
                --cacert={{.CertificatesDir}}/etcd/ca.crt \
                --cert={{.CertificatesDir}}/etcd/server.crt \
                --key={{.CertificatesDir}}/etcd/server.key
              
              # 备份配置文件
              cp /etc/etcd/etcd.conf {{.EtcdConfig.DataDir}}/etcd.conf.backup-$(date +%Y%m%d-%H%M%S)
            timeout: 300s
            nodeSelector:
              roles: [etcd]
          
          # 步骤2：停止 etcd 服务
          - name: stop-etcd-service
            type: Script
            script: |
              systemctl stop etcd
              sleep 5
            timeout: 30s
          
          # 步骤3：备份二进制文件
          - name: backup-etcd-binary
            type: Script
            script: |
              if [ -f /usr/local/bin/etcd ]; then
                cp /usr/local/bin/etcd /usr/local/bin/etcd.backup-$(date +%Y%m%d-%H%M%S)
                cp /usr/local/bin/etcdctl /usr/local/bin/etcdctl.backup-$(date +%Y%m%d-%H%M%S)
              fi
            timeout: 30s
          
          # 步骤4：更新 etcd 二进制
          - name: update-etcd-binary
            type: Script
            script: |
              # 从本地或远程获取二进制
              cp /tmp/etcd-{{.Version}}/etcd /usr/local/bin/
              cp /tmp/etcd-{{.Version}}/etcdctl /usr/local/bin/
              chmod +x /usr/local/bin/etcd
              chmod +x /usr/local/bin/etcdctl
            timeout: 60s
          
          # 步骤5：启动 etcd 服务
          - name: start-etcd-service
            type: Script
            script: |
              systemctl daemon-reload
              systemctl start etcd
            timeout: 60s
          
          # 步骤6：等待 etcd 成员加入集群
          - name: wait-etcd-member-ready
            type: Script
            script: |
              # 等待 etcd 服务启动并加入集群
              for i in {1..30}; do
                if ETCDCTL_API=3 etcdctl endpoint health \
                  --endpoints=https://127.0.0.1:2379 \
                  --cacert={{.CertificatesDir}}/etcd/ca.crt \
                  --cert={{.CertificatesDir}}/etcd/server.crt \
                  --key={{.CertificatesDir}}/etcd/server.key 2>&1 | grep -q "healthy"; then
                  echo "Etcd member is healthy"
                  exit 0
                fi
                sleep 5
              done
              echo "Etcd member failed to become healthy"
              exit 1
            timeout: 180s
        
        postCheck:
          steps:
            - name: verify-etcd-cluster-health
              type: Script
              script: |
                # 验证整个 etcd 集群健康
                ETCDCTL_API=3 etcdctl endpoint health --cluster \
                  --endpoints=https://{{.NodeIP}}:2379 \
                  --cacert={{.CertificatesDir}}/etcd/ca.crt \
                  --cert={{.CertificatesDir}}/etcd/server.crt \
                  --key={{.CertificatesDir}}/etcd/server.key
              timeout: 60s
      
      # 升级动作（从 v3.5.11 升级）
      upgradeFrom:
        - fromVersion: v3.5.11
          upgradeAction:
            preCheck:
              steps:
                - name: check-current-etcd-version
                  type: Script
                  script: |
                    CURRENT=$(etcdctl version | head -1 | awk '{print $2}')
                    if [ "$CURRENT" != "v3.5.11" ]; then
                      echo "Current version $CURRENT is not v3.5.11"
                      exit 1
                    fi
                  timeout: 30s
            
            # 逐节点串行策略
            strategy:
              executionMode: Serial
              batchSize: 1
              batchInterval: 90s  # 升级间隔更长
              waitForCompletion: true
            
            steps:
              # 步骤1：仅在第一个 etcd 节点执行备份
              - name: backup-etcd-cluster-on-first-node
                type: Script
                condition: |
                  {{if and (index .NodeRoles "etcd") (eq .NodeIndex 0)}}true{{else}}false{{end}}
                script: |
                  # 备份整个 etcd 集群数据
                  ETCDCTL_API=3 etcdctl snapshot save {{.EtcdConfig.DataDir}}/upgrade-snapshot-$(date +%Y%m%d-%H%M%S).db \
                    --endpoints=https://127.0.0.1:2379 \
                    --cacert={{.CertificatesDir}}/etcd/ca.crt \
                    --cert={{.CertificatesDir}}/etcd/server.crt \
                    --key={{.CertificatesDir}}/etcd/server.key
                  
                  # 列出所有 etcd 成员
                  ETCDCTL_API=3 etcdctl member list \
                    --endpoints=https://127.0.0.1:2379 \
                    --cacert={{.CertificatesDir}}/etcd/ca.crt \
                    --cert={{.CertificatesDir}}/etcd/server.crt \
                    --key={{.CertificatesDir}}/etcd/server.key > {{.EtcdConfig.DataDir}}/members-backup-$(date +%Y%m%d-%H%M%S).txt
                timeout: 300s
              
              # 步骤2：停止 etcd
              - name: stop-etcd
                type: Script
                script: |
                  systemctl stop etcd
                  sleep 10
                timeout: 30s
              
              # 步骤3：备份旧版本
              - name: backup-old-version
                type: Script
                script: |
                  cp /usr/local/bin/etcd /usr/local/bin/etcd-v3.5.11
                  cp /usr/local/bin/etcdctl /usr/local/bin/etcdctl-v3.5.11
                timeout: 30s
              
              # 步骤4：替换二进制
              - name: replace-etcd-binary
                type: Script
                script: |
                  cp /tmp/etcd-v3.5.12/etcd /usr/local/bin/
                  cp /tmp/etcd-v3.5.12/etcdctl /usr/local/bin/
                  chmod +x /usr/local/bin/etcd
                  chmod +x /usr/local/bin/etcdctl
                timeout: 60s
              
              # 步骤5：启动 etcd
              - name: start-etcd
                type: Script
                script: |
                  systemctl start etcd
                timeout: 60s
              
              # 步骤6：等待节点健康
              - name: wait-node-healthy
                type: Script
                script: |
                  for i in {1..60}; do
                    if ETCDCTL_API=3 etcdctl endpoint health \
                      --endpoints=https://127.0.0.1:2379 \
                      --cacert={{.CertificatesDir}}/etcd/ca.crt \
                      --cert={{.CertificatesDir}}/etcd/server.crt \
                      --key={{.CertificatesDir}}/etcd/server.key 2>&1 | grep -q "healthy"; then
                      echo "Etcd node healthy"
                      exit 0
                    fi
                    sleep 5
                  done
                  echo "Etcd node not healthy after 5 minutes"
                  exit 1
                timeout: 300s
            
            postCheck:
              steps:
                - name: verify-cluster-health
                  type: Script
                  script: |
                    # 验证所有 etcd 节点健康
                    ETCDCTL_API=3 etcdctl endpoint health --cluster \
                      --endpoints=https://{{.ControlPlaneEndpoint}}:2379 \
                      --cacert={{.CertificatesDir}}/etcd/ca.crt \
                      --cert={{.CertificatesDir}}/etcd/server.crt \
                      --key={{.CertificatesDir}}/etcd/server.key
                  timeout: 60s
      
      # 健康检查
      healthCheck:
        steps:
          - name: check-etcd-version
            type: Script
            script: |
              etcdctl version | grep -q "v3.5.12"
            timeout: 30s
            interval: 5s
          
          - name: check-etcd-service
            type: Script
            script: |
              systemctl is-active etcd
            expectedOutput: "active"
            timeout: 10s
            interval: 2s
          
          - name: check-etcd-endpoint
            type: Script
            script: |
              ETCDCTL_API=3 etcdctl endpoint health \
                --endpoints=https://127.0.0.1:2379 \
                --cacert={{.CertificatesDir}}/etcd/ca.crt \
                --cert={{.CertificatesDir}}/etcd/server.crt \
                --key={{.CertificatesDir}}/etcd/server.key
            timeout: 10s
            interval: 5s
```

## 3. 关键技术点解析

### 3.1 节点序号与首节点判断

**模板变量扩展**：

```go
// TemplateContext 扩展
type TemplateContext struct {
    // ... 现有变量
    
    // 节点序号相关
    NodeIndex     int      `json:"nodeIndex"`     // 当前节点在节点列表中的索引（0-based）
    NodeTotal     int      `json:"nodeTotal"`     // 总节点数
    IsFirstNode   bool     `json:"isFirstNode"`   // 是否为第一个节点
    IsLastNode    bool     `json:"isLastNode"`    // 是否为最后一个节点
    
    // etcd 集群相关
    EtcdEndpoints []string `json:"etcdEndpoints"` // 所有 etcd 节点地址
    EtcdLeader    string   `json:"etcdLeader"`    // 当前 etcd leader 地址
}
```

**节点排序规则**：
1. 优先按 `etcd` 角色排序
2. 其次按 `master` 角色排序
3. 最后按节点名称字典序排序

### 3.2 ActionStrategy 串行执行策略

```go
type ActionStrategy struct {
    // 执行模式
    ExecutionMode ExecutionMode `json:"executionMode,omitempty"`
    
    // 批处理大小（串行模式下为1）
    BatchSize int `json:"batchSize,omitempty"`
    
    // 批次间间隔
    BatchInterval *metav1.Duration `json:"batchInterval,omitempty"`
    
    // 是否等待前一个批次完成
    WaitForCompletion bool `json:"waitForCompletion,omitempty"`
    
    // 失败策略
    FailurePolicy FailurePolicy `json:"failurePolicy,omitempty"`
}

type ExecutionMode string

const (
    ExecutionParallel ExecutionMode = "Parallel"  // 并行执行
    ExecutionSerial   ExecutionMode = "Serial"    // 串行执行（逐节点）
    ExecutionRolling  ExecutionMode = "Rolling"   // 滚动执行
)
```

### 3.3 条件步骤执行

```yaml
- name: backup-etcd-on-first-node
  type: Script
  # 条件表达式：使用 Go template 语法
  condition: |
    {{if and (index .NodeRoles "etcd") (eq .NodeIndex 0)}}true{{else}}false{{end}}
  script: |
    # 仅在条件为 true 时执行此脚本
    echo "This is the first etcd node, performing backup..."
```

## 4. ActionEngine 执行流程

### 4.1 逐节点升级执行流程

```
┌─────────────────────────────────────────────────────────────────┐
│                    etcd 逐节点升级流程                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  1. 收集所有匹配的节点（etcd/master 角色）                        │
│     └── 按规则排序节点列表                                       │
│                                                                 │
│  2. 构建每个节点的 TemplateContext                               │
│     ├── NodeIndex = 0, 1, 2...                                  │
│     ├── IsFirstNode = (NodeIndex == 0)                          │
│     ├── IsLastNode = (NodeIndex == NodeTotal - 1)               │
│     └── EtcdEndpoints = [所有 etcd 节点地址]                     │
│                                                                 │
│  3. 按串行策略逐个节点执行                                       │
│     │                                                           │
│     ├── 节点 0 (第一个 etcd 节点)                                │
│     │   ├── 执行 backup-etcd-on-first-node (条件满足)            │
│     │   ├── 执行 stop-etcd-service                               │
│     │   ├── 执行 backup-etcd-binary                              │
│     │   ├── 执行 update-etcd-binary                              │
│     │   ├── 执行 start-etcd-service                              │
│     │   ├── 执行 wait-etcd-member-ready                          │
│     │   ├── 执行 postCheck                                       │
│     │   └── 等待 BatchInterval (60s)                             │
│     │                                                           │
│     ├── 节点 1 (第二个 etcd 节点)                                │
│     │   ├── 跳过 backup-etcd-on-first-node (条件不满足)          │
│     │   ├── 执行 stop-etcd-service                               │
│     │   ├── ... (其他步骤)                                       │
│     │   └── 等待 BatchInterval                                    │
│     │                                                           │
│     └── 节点 2 (第三个 etcd 节点)                                │
│         ├── ... (重复流程)                                       │
│         └── 所有节点完成                                         │
│                                                                 │
│  4. 更新 ComponentVersionBinding 状态                           │
│     └── phase = Healthy                                          │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## 5. 高级特性扩展

### 5.1 Leader 优先/最后升级策略

```yaml
- name: backup-etcd-on-leader
  type: Script
  condition: |
    {{if eq .NodeIP .EtcdLeader}}true{{else}}false{{end}}
  script: |
    # 仅在 etcd leader 节点执行备份
    echo "Performing backup on etcd leader node {{.NodeIP}}"
```

### 5.2 升级前集群健康检查

```yaml
preCheck:
  steps:
    - name: check-all-etcd-nodes-healthy
      type: Script
      # 仅在第一个节点执行集群健康检查
      condition: |
        {{if eq .NodeIndex 0}}true{{else}}false{{end}}
      script: |
        # 检查所有 etcd 节点健康
        ETCDCTL_API=3 etcdctl endpoint health --cluster \
          --endpoints={{range $i, $ep := .EtcdEndpoints}}{{if $i}},{{end}}https://{{$ep}}:2379{{end}} \
          --cacert={{.CertificatesDir}}/etcd/ca.crt \
          --cert={{.CertificatesDir}}/etcd/server.crt \
          --key={{.CertificatesDir}}/etcd/server.key
        
        # 检查 etcd 集群成员数
        MEMBER_COUNT=$(ETCDCTL_API=3 etcdctl member list --endpoints=https://127.0.0.1:2379 ... | wc -l)
        if [ "$MEMBER_COUNT" -ne "{{.NodeTotal}}" ]; then
          echo "Etcd member count mismatch: expected {{.NodeTotal}}, got $MEMBER_COUNT"
          exit 1
        fi
      timeout: 60s
```

### 5.3 自动回滚机制

```yaml
# 定义回滚动作
rollbackAction:
  steps:
    - name: stop-etcd-for-rollback
      type: Script
      script: |
        systemctl stop etcd
      timeout: 30s
    
    - name: restore-etcd-binary
      type: Script
      script: |
        cp /usr/local/bin/etcd-v3.5.11 /usr/local/bin/etcd
        cp /usr/local/bin/etcdctl-v3.5.11 /usr/local/bin/etcdctl
      timeout: 60s
    
    - name: start-etcd-after-rollback
      type: Script
      script: |
        systemctl start etcd
      timeout: 60s
```

## 6. 实际使用示例

### 6.1 ReleaseImage 引用

```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ReleaseImage
metadata:
  name: openfuyao-v2.6.0
spec:
  version: v2.6.0
  components:
    - componentName: etcd
      version: v3.5.12
      componentVersionRef:
        name: etcd-v3.5.12
      mandatory: true
      scope: Node
      dependencies:
        - componentName: certs
        - componentName: nodesEnv
```

### 6.2 ClusterVersion 触发升级

```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ClusterVersion
metadata:
  name: my-cluster
spec:
  desiredVersion: v2.6.0
  releaseRef:
    name: openfuyao-v2.6.0
  upgradeStrategy:
    type: RollingUpdate
    maxUnavailable: 1
    autoRollback: true
```

## 7. 总结

本方案的核心特点：

| 特性 | 说明 |
|------|------|
| ✅ **逐节点串行执行** | 通过 `ActionStrategy.ExecutionMode = Serial` 实现 |
| ✅ **首节点备份** | 通过条件步骤 `{{if eq .NodeIndex 0}}` 实现 |
| ✅ **节点序号识别** | 扩展 TemplateContext 支持 `NodeIndex`、`IsFirstNode` 等变量 |
| ✅ **灵活的条件判断** | 支持 Go template 语法的条件表达式 |
| ✅ **健康检查门控** | 前一个节点健康检查通过后才执行下一个 |
| ✅ **Leader 感知** | 支持识别 etcd leader 节点并执行特殊操作 |
| ✅ **自动回滚** | 定义 `rollbackAction` 支持升级失败回滚 |

此方案完全基于 `KEPU-2` 的现有设计，无需大幅改动框架即可实现 etcd 的安全逐节点升级。
        
