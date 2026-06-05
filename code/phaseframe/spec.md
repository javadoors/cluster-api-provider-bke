# BKECluster
## BasePhase
### 通用执行条件
- 对于删除的BKECluster，不执行（DeletionTimestamp）
- 对于暂停的BKECluster，不执行（Spec.Pause || annotations.HasPaused()）
- 对于DryRun的BKECluster，不执行（Spec.DryRun）
- 不健康的BKECluster不执行(Status.ClusterHealthState)
- 对于不是BKECluster，并且没有完全控制的，不执行(??)
### 
