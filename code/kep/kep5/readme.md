# prompt
设计基于ClusterVersion、ReleaseImage资源及控制器的方案，支持openFuyao集群版本升级
1. ClusterVersion、ReleaseImage借鉴openshift的方案
2. bke-manifests提供ComponentVersion信息
3. 各资源间的关联关系，1个ReleaseImage对应1个ClusterVersion，ReleaseImage的组件来源于bke-manifests(ComponentVersion)
4. ClusterVersion对应整个版本的概念
5. ReleaseImage对应发布版本清单
6. bke-manifests(ComponentVersion)对应组件清单（含多个版本）
7. 设计资源属性及其关联关系，实现对应的控制器业务逻辑
9. 升级流程：
   a. 根据ClusterVersion目标版本找到目标ReleaseImage，由目标ReleaseImage找到bke-manifests(ComponentVersion)中的对应组件。
   b. 根据ClusterVersion找到当前ReleaseImage，由当前ReleaseImage找到当前bke-manifests(ComponentVersion)中的对应组件。
   c. 比较目标组件与当前组件版本，如果有变更则进行升级业务逻辑 
   d. 涉及的phase包含：EnsureProviderSelfUpgrade、EnsureAgentUpgrade、EnsureComponentUpgrade、EnsureEtcdUpgrade
   e. 整个的调谐逻辑还放在BKECluster控制器中，但要保证与ClusterVersion控制器的协同(如发现ClusterVersion目标版本变更，触发BKECluster控制器的升级流程)
10. 设计各控制调谐器，给出作用，各控制器间关系，设计思路，代码实现等。
11. 给出工作量评估，增加相关的架构图，流程图等
12. 要求架构清晰，组件可独立演进，不耦合。
13. 保证重构能够平滑升级，并给出对应的方案设计。
14. 请按照k8s KEP的规范输出对应的提案（目标、范围、约束、场景等不要遗漏）
