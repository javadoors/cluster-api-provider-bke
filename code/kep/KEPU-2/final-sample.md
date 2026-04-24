# sample
// ... existing code ...
## 14. 验收标准
1. **YAML 声明验收**：16 个组件全部通过 YAML 声明安装/升级/卸载，无组件特定 Go 代码
2. **安装验收**：从零创建集群，ActionEngine 解释 YAML 完成安装
3. **升级验收**：修改 ClusterVersion 版本，触发旧版本卸载 + 新版本安装
4. **单组件升级验收**：修改 ComponentVersion 版本，仅升级该组件
5. **扩缩容验收**：添加/移除节点，NodeConfig 自动创建/删除
6. **回滚验收**：升级失败后自动执行 rollbackAction
7. **模板验收**：模板变量正确渲染，条件表达式正确评估
8. **兼容性验收**：Feature Gate 关闭时旧 PhaseFlow 正常运行

## 15. 使用声明样例
本章节展示如何使用声明式 YAML 进行集群的安装、升级与扩缩容操作。所有操作均通过 YAML 声明，由 ActionEngine 解释执行。
### 15.1 全新集群安装样例
#### 15.1.1 创建 ClusterVersion
```yaml
apiVersion: cvo.bke.io/v1beta1
kind: ClusterVersion
metadata:
  name: production-cluster
spec:
  desiredVersion: v1.29.0
  releaseImageRef:
    name: bke-release-v1.29.0
    namespace: default
  upgradeStrategy:
    autoRollback: true
    maxRetries: 3
  nodeConfigSelector:
    matchLabels:
      cluster: production
```
#### 15.1.2 创建 ReleaseImage
```yaml
apiVersion: cvo.bke.io/v1beta1
kind: ReleaseImage
metadata:
  name: bke-release-v1.29.0
spec:
  version: v1.29.0
  components:
    - name: kubernetes
      componentVersionRef:
        name: kubernetes-v1.29.0
    - name: etcd
      componentVersionRef:
        name: etcd-v3.5.12
    - name: containerd
      componentVersionRef:
        name: containerd-v1.7.2
    - name: calico
      componentVersionRef:
        name: calico-v3.26.0
    - name: coredns
      componentVersionRef:
        name: coredns-v1.10.1
  compatibility:
    kubernetesVersion: "1.29"
    etcdVersion: "3.5.12"
    containerdVersion: "1.7.2"
```
#### 15.1.3 创建 ComponentVersion（以 Kubernetes 为例）
```yaml
apiVersion: cvo.bke.io/v1beta1
kind: ComponentVersion
metadata:
  name: kubernetes-v1.29.0
spec:
  componentName: kubernetes
  scope: Node
  nodeSelector:
    role: master
  versions:
    - version: v1.29.0
      installAction:
        strategy:
          type: Serial
        steps:
          - name: pre-check
            type: Script
            source:
              inline: |
                # 检查节点资源
                if [[ $(free -g | awk '/^Mem:/ {print $2}') -lt 8 ]]; then
                  echo "ERROR: Insufficient memory"
                  exit 1
                fi
          - name: install-kubeadm
            type: Script
            source:
              configMap:
                name: kubeadm-scripts
                key: install.sh
          - name: init-cluster
            type: Script
            source:
              inline: |
                kubeadm init --config /etc/kubernetes/kubeadm-config.yaml
            condition: "{{.Node.role}} == master"
      upgradeAction:
        strategy:
          type: Rolling
          batchSize: 1
          waitForCompletion: true
        steps:
          - name: drain-node
            type: Kubectl
            source:
              inline: |
                kubectl drain {{.Node.name}} --ignore-daemonsets --delete-emptydir-data
          - name: upgrade-kubelet
            type: Script
            source:
              inline: |
                apt-get update
                apt-get install -y kubelet=1.29.0-00 kubectl=1.29.0-00
                systemctl restart kubelet
          - name: uncordon-node
            type: Kubectl
            source:
              inline: |
                kubectl uncordon {{.Node.name}}
      healthCheck:
        type: Script
        source:
          inline: |
            kubectl get node {{.Node.name}} -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' | grep -q True
        intervalSeconds: 30
        timeoutSeconds: 10
```
### 15.2 集群版本升级样例
#### 15.2.1 升级 ClusterVersion
```yaml
# 将 ClusterVersion 的 desiredVersion 从 v1.29.0 修改为 v1.30.0
apiVersion: cvo.bke.io/v1beta1
kind: ClusterVersion
metadata:
  name: production-cluster
spec:
  desiredVersion: v1.30.0  # 修改此字段触发升级
  releaseImageRef:
    name: bke-release-v1.30.0  # 引用新版本 ReleaseImage
    namespace: default
  upgradeStrategy:
    autoRollback: true
    maxRetries: 3
```
#### 15.2.2 创建新版本 ReleaseImage
```yaml
apiVersion: cvo.bke.io/v1beta1
kind: ReleaseImage
metadata:
  name: bke-release-v1.30.0
spec:
  version: v1.30.0
  components:
    - name: kubernetes
      componentVersionRef:
        name: kubernetes-v1.30.0
    - name: etcd
      componentVersionRef:
        name: etcd-v3.5.13
    - name: containerd
      componentVersionRef:
        name: containerd-v1.7.3
    # ... 其他组件
```
#### 15.2.3 定义升级路径
```yaml
apiVersion: cvo.bke.io/v1beta1
kind: UpgradePath
metadata:
  name: v1.29.0-to-v1.30.0
spec:
  fromRelease: bke-release-v1.29.0
  toRelease: bke-release-v1.30.0
  blocked: false
  preCheck:
    - name: etcd-backup-check
      type: Script
      source:
        inline: |
          # 检查 etcd 备份是否完成
          etcdctl snapshot status /var/lib/etcd/backup.db
  steps:
    - component: etcd
      order: 1
      description: "先升级 etcd 到 3.5.13"
    - component: containerd
      order: 2
      description: "再升级 containerd 到 1.7.3"
    - component: kubernetes
      order: 3
      description: "最后升级 Kubernetes 到 1.30.0"
```
### 15.3 节点扩缩容样例
#### 15.3.1 节点扩容（添加工作节点）
```yaml
# 1. 创建 NodeConfig 定义新节点
apiVersion: cvo.bke.io/v1beta1
kind: NodeConfig
metadata:
  name: worker-node-4
spec:
  address: 192.168.1.104
  labels:
    role: worker
    cluster: production
  taints: []
  credentials:
    sshKeySecret:
      name: node-ssh-key
      key: id_rsa

# 2. ClusterVersion 会自动发现匹配的节点并调度组件安装
# 无需修改 ClusterVersion，系统会自动处理
```
#### 15.3.2 节点缩容（移除工作节点）
```yaml
# 1. 标记节点进入维护模式
apiVersion: cvo.bke.io/v1beta1
kind: NodeConfig
metadata:
  name: worker-node-3
spec:
  address: 192.168.1.103
  labels:
    role: worker
    cluster: production
  maintenanceMode: true  # 标记为维护模式

# 2. 系统会自动执行以下操作：
#    - 排空节点（drain）
#    - 卸载组件
#    - 从集群中移除

# 3. 删除 NodeConfig 以完成缩容
# kubectl delete nodeconfig worker-node-3
```
#### 15.3.3 控制平面节点扩容
```yaml
# 添加新的控制平面节点
apiVersion: cvo.bke.io/v1beta1
kind: NodeConfig
metadata:
  name: master-3
spec:
  address: 192.168.1.203
  labels:
    role: master
    cluster: production
  taints:
    - key: node-role.kubernetes.io/master
      effect: NoSchedule
  credentials:
    sshKeySecret:
      name: master-ssh-key
      key: id_rsa

# ComponentVersion 的 nodeSelector 会自动匹配并安装控制平面组件
```
### 15.4 单组件独立升级样例
#### 15.4.1 升级 Calico 网络插件
```yaml
# 1. 创建新版本 ComponentVersion
apiVersion: cvo.bke.io/v1beta1
kind: ComponentVersion
metadata:
  name: calico-v3.27.0
spec:
  componentName: calico
  scope: Cluster
  versions:
    - version: v3.27.0
      installAction:
        strategy:
          type: Rolling
          batchSize: 2
        steps:
          - name: upgrade-calico
            type: Helm
            source:
              chart:
                repository: https://projectcalico.docs.tigera.io/charts
                name: tigera-operator
                version: v3.27.0
      upgradeAction:
        strategy:
          type: Rolling
          batchSize: 1
          waitForCompletion: true
        steps:
          - name: backup-calico-config
            type: Kubectl
            source:
              inline: |
                kubectl get configmap -n calico-system -o yaml > calico-backup.yaml
          - name: upgrade-operator
            type: Helm
            source:
              chart:
                repository: https://projectcalico.docs.tigera.io/charts
                name: tigera-operator
                version: v3.27.0

# 2. 修改 ReleaseImage 引用新版本
apiVersion: cvo.bke.io/v1beta1
kind: ReleaseImage
metadata:
  name: bke-release-v1.29.0-calico-upgrade
spec:
  version: v1.29.0
  components:
    - name: kubernetes
      componentVersionRef:
        name: kubernetes-v1.29.0
    - name: calico
      componentVersionRef:
        name: calico-v3.27.0  # 引用新版本
    # ... 其他组件保持不变
```
### 15.5 升级回滚样例
#### 15.5.1 自动回滚配置
```yaml
apiVersion: cvo.bke.io/v1beta1
kind: ClusterVersion
metadata:
  name: production-cluster
spec:
  desiredVersion: v1.30.0
  releaseImageRef:
    name: bke-release-v1.30.0
    namespace: default
  upgradeStrategy:
    autoRollback: true  # 启用自动回滚
    rollbackWindowSeconds: 3600  # 1小时内可自动回滚
    maxRetries: 2
    failureThreshold: 3  # 3个组件失败触发回滚
```
#### 15.5.2 手动触发回滚
```yaml
# 将 ClusterVersion 的 desiredVersion 改回旧版本
apiVersion: cvo.bke.io/v1beta1
kind: ClusterVersion
metadata:
  name: production-cluster
spec:
  desiredVersion: v1.29.0  # 改回旧版本触发回滚
  releaseImageRef:
    name: bke-release-v1.29.0
    namespace: default
```
### 15.6 高级使用场景
#### 15.6.1 金丝雀发布（Canary Deployment）
```yaml
apiVersion: cvo.bke.io/v1beta1
kind: ComponentVersion
metadata:
  name: app-service-v2.0.0
spec:
  componentName: app-service
  scope: Node
  nodeSelector:
    app: canary
  versions:
    - version: v2.0.0
      installAction:
        strategy:
          type: Rolling
          batchSize: 1
          maxUnavailable: 0
        steps:
          - name: deploy-canary
            type: Kubectl
            source:
              inline: |
                kubectl apply -f app-service-v2-canary.yaml
      healthCheck:
        type: Script
        source:
          inline: |
            # 检查金丝雀版本指标
            curl -f http://{{.Node.name}}:8080/health
            # 验证业务指标
            check_business_metrics.sh
```
#### 15.6.2 蓝绿部署（Blue-Green Deployment）
```yaml
apiVersion: cvo.bke.io/v1beta1
kind: ComponentVersion
metadata:
  name: frontend-blue-green
spec:
  componentName: frontend
  scope: Cluster
  versions:
    - version: blue
      installAction:
        steps:
          - name: deploy-blue
            type: Kubectl
            source:
              inline: |
                kubectl apply -f frontend-blue.yaml
    - version: green
      installAction:
        steps:
          - name: deploy-green
            type: Kubectl
            source:
              inline: |
                kubectl apply -f frontend-green.yaml
          - name: switch-traffic
            type: Kubectl
            source:
              inline: |
                kubectl patch service frontend -p '{"spec":{"selector":{"version":"green"}}}'
```
### 15.7 最佳实践
1. **版本管理**
   - 为每个组件版本创建独立的 ComponentVersion CR
   - 使用语义化版本控制（SemVer）
   - 维护版本兼容性矩阵
2. **升级策略**
   - 生产环境使用 Rolling 策略，设置适当的 batchSize
   - 启用 autoRollback 和健康检查
   - 定义清晰的升级路径
3. **节点管理**
   - 使用标签和选择器进行节点分组
   - 为不同角色（master/worker）定义不同的 ComponentVersion
   - 使用 maintenanceMode 进行安全缩容
4. **监控与观测**
   - 为每个组件定义 healthCheck
   - 监控 ComponentVersionBinding 状态
   - 记录升级历史

这些样例展示了声明式集群管理的核心模式，所有操作均通过 YAML 声明，由系统自动执行，实现了真正的 GitOps 工作流。
```
我已经在final.md文件中添加了完整的"## 15. 使用声明样例"章节。这个章节包含了以下内容：
1. **全新集群安装样例** - 展示了如何通过ClusterVersion、ReleaseImage和ComponentVersion CRD声明式地安装一个完整集群
2. **集群版本升级样例** - 展示了如何通过修改ClusterVersion的desiredVersion字段触发整个集群的版本升级
3. **节点扩缩容样例** - 展示了如何通过NodeConfig CRD进行节点的扩容和缩容
4. **单组件独立升级样例** - 展示了如何独立升级单个组件（如Calico网络插件）
5. **升级回滚样例** - 展示了自动回滚和手动回滚的配置方法
6. **高级使用场景** - 包括金丝雀发布和蓝绿部署模式
7. **最佳实践** - 提供了版本管理、升级策略、节点管理和监控观测的最佳实践

所有样例都遵循声明式管理原则，完全通过YAML配置，不包含任何工程特定的代码，符合用户的要求。这些样例可以直接用于理解和使用基于CRD的声明式集群版本管理系统。
        
