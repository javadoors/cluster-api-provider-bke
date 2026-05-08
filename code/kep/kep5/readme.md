# prompt
设计基于ClusterVersion、ReleaseImage、UpgradePath资源及控制器的方案，支持openFuyao集群版本升级（参考kep5.md）
1. ClusterVersion、ReleaseImage借鉴openshift的方案
2. bke-manifests提供ComponentVersion信息
3. 各资源间的关联关系，1个ReleaseImage对应1个ClusterVersion，ReleaseImage的组件来源于bke-manifests(ComponentVersion)
4. ClusterVersion对应整个版本的概念
5. ReleaseImage对应发布版本清单
6. bke-manifests(ComponentVersion)对应组件清单（含多个版本）
7. 设计资源属性及其关联关系，实现对应的控制器业务逻辑
8. 提供升级路径的设计方案
9. ReleaseImage、UpgradePath的来源设计
   - ReleaseImage可通过oci镜像获取，每个版本一个镜像，细化设计方案(给出样例)
   - UpgradePath可通过oci镜像获取，只保留一个最新镜像即可，细化设计方案(给出样例)
10. 升级流程：
   a. BKECluster控制器中调用phase的升级逻辑不变
   b. 涉及的phase包含：EnsureProviderSelfUpgrade、EnsureAgentUpgrade、EnsureComponentUpgrade、EnsureEtcdUpgrade
   c. 在涉及的phase中，做如下的是否进行升级的逻辑判断：
      - 根据ClusterVersion目标版本找到目标ReleaseImage，由目标ReleaseImage找到bke-manifests(ComponentVersion)中的对应组件。
      - 根据ClusterVersion找到当前ReleaseImage，由当前ReleaseImage找到当前bke-manifests(ComponentVersion)中的对应组件。
      - 比较目标组件与当前组件版本，如果有变更则进行升级业务逻辑 
   d. 整个的调谐逻辑还放在BKECluster控制器中，但要保证与ClusterVersion控制器的协同(如发现ClusterVersion目标版本变更，触发BKECluster控制器的升级流程)
11. 设计各控制调谐器，给出作用，各控制器间关系，设计思路，代码实现等。
12. 给出工作量评估(按照新手估计)，增加相关的架构图，流程图等
13. 要求架构清晰，组件可独立演进，不耦合。
14. 保证重构能够平滑升级，并给出对应的方案设计。
15. 不要重新定义ShouldUpgrade()接口，复用NeedExecute()接口,或在NeedExecute()中调用ShouldUpgrade()接口，补全此处设计
16. 请按照k8s KEP的规范输出对应的提案（目标、范围、约束、场景等不要遗漏）
