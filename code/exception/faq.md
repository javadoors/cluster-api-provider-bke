# Command生命周期状态机

# Phase状态机

# BKECluster状态机

# BKEMachine 状态机

# 节点状态
标记系统
从[bkecluster_consts.go:235-244](file:////cluster-api-provider-bke/api/capbke/v1beta1/bkecluster_consts.go#L235-L244)可以看到：
```go
const (
    NodeAgentPushedFlag = 1 << iota  // 1 << 0 = 1
    NodeAgentReadyFlag               // 1 << 1 = 2
    NodeEnvFlag                      // 1 << 2 = 4
    NodeBootFlag                     // 1 << 3 = 8
    NodeHAFlag                       // 1 << 4 = 16
    MasterInitFlag                   // 1 << 5 = 32
    NodeDeletingFlag                 // 1 << 6 = 64
    NodeFailedFlag                   // 1 << 7 = 128 (关键！)
    NodeStateNeedRecord              // 1 << 8 = 256
    NodePostProcessFlag              // 1 << 9 = 512
)
```
