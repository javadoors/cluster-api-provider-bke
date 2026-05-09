# prompt
设计基于ClusterVersion、ReleaseImage、UpgradePath资源及控制器的方案，支持openFuyao集群版本升级（参考kep5.md）
1. ClusterVersion、ReleaseImage借鉴openshift的方案
2. bke-manifests提供ComponentVersion信息
   a. ComponentVersion支持两种类型，叶子组件与组合组件，叶子组件对应单一组件(如api-server),组合组件包含多个组件(如k8s包含etcd/controller-mananger/scheduler/etcd)
      - 设计ComponentVersion的数据结构(必有字段name与version)，支持叶子与组合组件两种类型，每种类型区分是否是inline模式，以支持调用phase中的代码实
	  - 设计ComponentVersion的组合组件包含子组件的基本元数据信息(名称，版本)，以支持版本兼容性检查
	  - 设计ComponentVersion的兼容性约束
	  - 设计ComponentVersion的依赖约束
	  - 设计ComponentVersion的升级策略(支持串行、并行、批量等)，仅出给设计，暂不进行代码实现
   b. EnsureProviderSelfUpgrade、EnsureAgentUpgrade、EnsureComponentUpgrade、EnsureEtcdUpgrade各phase在bke-manifests重构为组件yaml清单,model=""
   c. 其它以代码实现的phase中增加Version()等接口，以支持能够映射到ComponentVersion，同时在bke-manifests中注册
      - 代码实现的phase统一注册到ComponentFactory中，通过(name,version)进行索引，Model=inline
	  - 完成ComponentFactory的设计及所有phase的重构设计
	  - 在bke-manifests中注册时与其它组件采用一致的目录结构
3. 各资源间的关联关系，1个ReleaseImage对应1个ClusterVersion，ReleaseImage的组件来源于bke-manifests(ComponentVersion)
4. ClusterVersion对应整个版本的概念
5. ReleaseImage对应发布版本清单
   - ReleaseImage包含安装的组件列表
   - ReleaseImage包含升级的组件列表
   - 样例：
     ```yaml
	 version: "v2.6.0"
	 install:
       components:
         - name: kubernetes
           version: v1.29.0
         - name: etcd
           version: v3.5.12
         - name: bkeagent
           version: v2.6.0
	 upgrade:
       components:
         - name: provider-upgrade
           version: v1.2.0
         - name: component-upgrade
           version: v1.1.0
    ```
6. bke-manifests(ComponentVersion)对应组件清单（含多个版本），没有现在bke-manifests的目录设计，组件版本目录下新增ComponentVersion的元数据信息
   - ReleaseImage通过组件(名称，版本)到bke-manifests下定位到组件获取元数据信息
7. 设计资源属性及其关联关系及组件间兼容性检查，实现对应的控制器业务逻辑
8. 提供升级路径的设计方案，及组件间兼容性检查的设计方案
   - 汇集所有的ComponentVersion组件列表
   - 组件列表包含组合组件与叶子组件及子组件的全部
   - 给出兼容性的算法设计
9. ReleaseImage、UpgradePath的来源设计
   - ReleaseImage可通过oci镜像获取，每个版本一个镜像，细化设计方案(给出样例,yaml格式)
   - UpgradePath可通过oci镜像获取，只保留一个最新镜像即可，细化设计方案(给出样例,yaml格式)
10. 升级流程：
   a. ReleaseImage中包含安装与升级的所有组件
   b. 根据ReleaseImage中的组件及组件的依赖组成DAG
   c. BKECluster控制器中先判断是安装还是升级场景，再调用安装与升级的DAG进行安装与升级的调用
   d. 在各phase中，做如下的是否进行升级的逻辑判断：
      - 根据ClusterVersion目标版本找到目标ReleaseImage，由目标ReleaseImage找到bke-manifests(ComponentVersion)中的对应组件。
      - 根据ClusterVersion找到当前ReleaseImage，由当前ReleaseImage找到当前bke-manifests(ComponentVersion)中的对应组件。
      - 比较目标组件与当前组件版本，如果有变更则进行升级业务逻辑 
   e. 整个的调谐逻辑还放在BKECluster控制器中，但要保证与ClusterVersion控制器的协同(如发现ClusterVersion目标版本变更，触发BKECluster控制器的升级流程)
11. 设计各控制调谐器，给出作用，各控制器间关系，设计思路，代码实现等。
12. 给出工作量评估(按照普通开发者估计)，增加相关的架构图，流程图等
13. 要求架构清晰，组件可独立演进，不耦合。
14. 保证重构能够平滑升级，并给出对应的方案设计。
15. 不要重新定义ShouldUpgrade()接口，复用NeedExecute()接口,或在NeedExecute()中调用ShouldUpgrade()接口，补全此处设计
16. bke-manifests中支持定义组件类型为引用代码中的各phase的代码实现(各phase默认版本为v1.0),在ReleaseImage中进行引用(ReleaseImage包含全量的phase)
    - 给出各phase的整改设计
17. 重构完成后，如何从旧版本升级到重构后版本的设计方案
    - 旧版本没有ClusterVersion、ReleaseImage、UpgradePath及新增控制器
	- 能够从旧版本平滑升级到新版本
18. 给出异常场景、性能、安全与可扩展性的设计
    - 支持可商业化使用	
19. 请按照k8s KEP的规范输出对应的提案（目标、范围、约束、场景等不要遗漏）

1. UpgradePath的oci镜像因为是latest，需要持续监控对应镜像的digest是否有变更，有变更需要重新获取最新的latest的镜像，请增加此方面的设计
2. UpgradePath的oci镜像,对应一个CR，主要是方便用户查看，而不是多个CR
3. 每条升级路径可设计一个数据结构对应
4. UpgradePath理论上是一个图，在路径查找过程中通过图来查找，增加此方面的设计思路与算法设计，并同步重构upgradePathStore的设计
5. 增加一种新场景的支持：
   - 新版本一些资源(如configMap,Secret)在旧版本中不存在，需要在新版本的升级前创建这些资源
   - 请设计一种扩展机制，支持这种场景，并给出对应的设计方案
