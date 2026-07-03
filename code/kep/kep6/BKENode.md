# BKENode中的Labels与k8s中Node的Lables是相同的吗？

**不相同，是两个不同层面的概念，且流向是单向的。**

## 两个 Labels 的区别

| 维度 | BKENode.Spec.Labels | K8s Node.ObjectMeta.Labels |
|------|---------------------|---------------------------|
| **类型** | `[]Label`（`[]struct{Key, Value}`） | `map[string]string` |
| **来源** | 用户在 BKECluster/BKENode CR 中声明 | K8s kubelet 自动设置 + BKE 推送 |
| **作用** | 声明"我要给 K8s Node 打什么标签" | K8s 调度/选择器使用 |

## 标签流向（单向）

```
BKECluster.Spec.ClusterConfig.Cluster.Labels (全局标签)
                                                ├──> mergeLabels() ──> K8s Node labels
BKENode.Spec.Labels (节点级标签)                 │        (via setNodeLabel)
                                                │
BKENode.ObjectMeta.Labels ──────────────────────┘──> 关联 BKENode 与 BKECluster
  ["cluster.x-k8s.io/cluster-name"]                  (用于 Watch/List/校验)
```

## 关键逻辑

1. **合并规则**（`ensure_cluster.go:491-502`）：节点级 `Spec.Labels` 优先于全局 `Cluster.Labels`，相同 key 时节点级覆盖全局
2. **推送方向**（`ensure_cluster.go:510-577`）：通过 hostname 匹配 K8s Node，调用 `Nodes().Update()` 写入 K8s Node labels
3. **唯一反向读取**（`phaseutil/util.go:616-629`）：`GetNodeRolesFromK8sNode` 仅读取 `node-role.kubernetes.io/*` 来判断 master/worker 角色，不回写 `BKENode.Spec.Labels`

**结论**：`BKENode.Spec.Labels` 是"意图声明"（我要给 K8s Node 打什么标签），不是 K8s Node labels 的镜像。K8s Node 上可能有 BKE 不知道的标签（如 kubelet 自动添加的），这些不会同步回 BKENode。
