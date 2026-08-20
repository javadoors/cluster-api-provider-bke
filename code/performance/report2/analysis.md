# bke-cluster-create2 安装部署性能分析

> **日志文件**：`bke-cluster-create2.log`
> **集群规模**：3 Master + 63 Worker = 66 节点
> **架构**：linux/arm64
> **Kubernetes 版本**：v1.34.3-of.1
> **总耗时**：21 分 15 秒（18:11:21 → 18:32:36）

---

## 1. 整体时间线

| 阶段 | 开始时间 | 结束时间 | 耗时 | 占比 |
|------|---------|---------|------|------|
| 启动 + AesDecrypt 循环 | 18:11:21 | 18:11:21 | <1s | - |
| **Submit cluster-api yaml + API 限流** | 18:11:21 | 18:19:58 | **8m37s** | **40.5%** |
| BKEAgent 推送 (66 节点) | 18:20:01 | 18:21:42 | 1m41s | 7.9% |
| 节点环境初始化 (66 节点) | 18:22:09 | 18:25:09 | 3m0s | 14.1% |
| 额外脚本安装 | 18:25:10 | 18:25:11 | 1s | - |
| ClusterAPI 对象创建 | 18:25:12 | 18:25:14 | 2s | - |
| 负载均衡配置 (3 Master) | 18:25:34 | 18:26:35 | 1m1s | 4.8% |
| Master 初始化 (1 节点) | 18:26:37 | 18:27:14 | 37s | 2.9% |
| Master 加入 (2 节点) | 18:27:19 | 18:28:27 | 1m8s | 5.4% |
| Worker 加入 (63 节点) | 18:28:29 | 18:30:01 | 1m32s | 7.2% |
| Addon 部署 (含 calico 重试) | 18:30:04 | 18:32:22 | 2m18s | 10.8% |
| 后置处理 + 健康检查 | 18:32:24 | 18:32:36 | 12s | 0.9% |
| **总计** | 18:11:21 | 18:32:36 | **21m15s** | 100% |

---

## 2. 瓶颈分析

### 2.1 瓶颈 1：API 客户端限流（最严重，占 40.5%）

**现象**：`18:11:21` 到 `18:19:58`，约 8 分 37 秒浪费在 client-side throttling。

**日志证据**（lines 204-253）：
```
Waited for 1.1973968s due to client-side throttling, not priority and fairness
Waited for 2.79670416s due to client-side throttling, not priority and fairness
Waited for 4.19752324s due to client-side throttling, not priority and fairness
Waited for 5.79745005s due to client-side throttling, not priority and fairness
Waited for 7.39765425s due to client-side throttling, not priority and fairness
...（重复约 50 次）
```

**根因**：`Submit cluster-api yaml to the cluster` 阶段，controller 向管理集群 API Server 提交 CAPI 相关 CRD 资源时，K8s client-go 的 QPS/Burst 配置过低（默认 QPS=5, Burst=10），导致每个 API 请求都需要等待限流。约 50 个 API 请求，每个等待 1-8 秒。

**优化建议**：
- 提高 client-go 的 QPS 和 Burst 配置（建议 QPS=50, Burst=100）
- 或使用批量提交方式减少 API 调用次数

---

### 2.2 瓶颈 2：AesDecrypt/AesEncrypt 循环（代码缺陷）

**现象**：lines 1-202 全部是重复的 `AesDecrypt` 失败 + `AesEncrypt` 成功循环，约 200 行日志，全部在同一秒内（18:11:21）。

**日志证据**：
```
AesDecrypt: attempting legacy format decryption
AesDecrypt: invalid legacy ciphertext length: 6
AesEncrypt: password encrypted successfully with new format
（重复约 66 次，对应 66 个节点的密码）
```

**根因**：代码对每个节点密码先尝试 legacy 格式解密，失败后再用新格式加密。66 个节点 × 3 个密码字段 = 约 200 次无效解密尝试。虽然耗时 <1s，但产生大量无效日志，且反映代码逻辑缺陷。

**优化建议**：
- 修复 AesDecrypt 逻辑，先判断格式再解密，避免无效尝试
- 或缓存已解密的密码，避免重复解密

---

### 2.3 瓶颈 3：Calico Addon 部署重试（浪费 ~1m44s）

**现象**：`18:30:05` 开始部署 calico，但直到 `18:31:49` 才重新开始部署，中间有 1m44s 的空档。

**日志证据**：
```
18:30:05 - start to create addon "calico"
18:31:49 - start to reconcile addon for target cluster (重新触发)
18:31:49 - start to create addon "calico"
18:32:21 - create calico/v3.31.3 success
```

**根因**：calico 首次部署可能因为 CRD 未就绪或网络问题失败，触发了 reconcile 重试。重试间隔约 1m44s。

**优化建议**：
- 在部署 calico 前确保 CRD 已就绪（增加 pre-check）
- 缩短重试间隔或增加重试前的等待逻辑

---

### 2.4 瓶颈 4：节点环境初始化（3 分钟，合理但可优化）

**现象**：`18:22:09` 到 `18:25:09`，66 个节点环境初始化耗时 3 分钟。

**日志证据**：
```
18:22:09 - Start check and init node env for k8s, total=66
18:25:09 - handleSuccessNodes finished, newNodes=66
```

**分析**：3 分钟完成 66 个节点的环境初始化，平均每个节点约 2.7 秒。考虑到需要执行内核参数配置、swap 关闭、防火墙配置、时间同步、hosts 写入、容器运行时安装等操作，这个时间基本合理。

**优化建议**：
- 可考虑将节点环境初始化拆分为多个并行阶段
- 容器运行时安装（containerd）可以提前预加载镜像

---

### 2.5 瓶颈 5：BKEAgent 推送（1m41s，合理）

**现象**：`18:20:01` 到 `18:21:42`，66 个节点 Agent 推送耗时 1m41s。

**分析**：平均每个节点约 1.5 秒，考虑到需要 SSH 连接、传输二进制文件、创建 systemd 服务、启动服务，这个时间基本合理。

**优化建议**：
- 可考虑使用 P2P 分发或预置 Agent 镜像减少传输时间

---

## 3. 优化优先级总结

| 优先级 | 瓶颈 | 当前耗时 | 优化后预期 | 节省时间 |
|--------|------|---------|-----------|---------|
| **P0** | API 客户端限流 | 8m37s | <30s | **~8m** |
| P1 | Calico 部署重试 | 2m18s | <30s | **~1m48s** |
| P2 | AesDecrypt 循环 | <1s | <0.1s | ~0.9s |
| P2 | 节点环境初始化 | 3m | 2m | ~1m |
| P3 | BKEAgent 推送 | 1m41s | 1m | ~40s |

**如果解决 P0 瓶颈（API 限流），总时间可从 21m15s 降至约 12m30s，提升约 40%。**

---

## 4. 其他发现

1. **PostProcess 空转**：lines 640-709 遍历 66 个节点检查 postprocess 配置，全部返回 "no postprocess config"。虽然耗时仅 1s，但产生 70 行无效日志。建议在没有 postprocess 配置时直接跳过遍历。

2. **Worker 加入并行度良好**：63 个 Worker 节点在 1m32s 内全部加入成功，说明 CAPI MachineDeployment 的并行扩缩容机制工作正常。

3. **Master 串行加入**：2 个 Master 节点是串行加入的（n3 完成后 n1 才开始），这是 CAPI KCP 的默认行为（保证 etcd 集群稳定性），合理。

---

**分析日期**：2026-08-20  
**分析人**：openFuyao Team
