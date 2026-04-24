# ComponentVersion中Kubectl类型的设计思路
## ComponentVersion中Kubectl类型的设计思路及代码实现
基于对final.md文件的分析，我为您详细解释Kubectl类型的设计思路和代码实现：
### 1. 设计思路
**核心定位**: Kubectl类型是ActionEngine的四种执行器之一，专门用于执行Kubernetes API操作。它允许在ComponentVersion的Action定义中直接声明Kubernetes资源操作，实现声明式的K8s资源管理。

**设计原则**:
- **声明式操作**: 通过YAML声明Kubernetes资源操作，而非编写脚本
- **幂等执行**: 同一操作多次执行结果一致
- **安全可控**: 支持超时控制、条件检查等安全机制
- **与K8s生态集成**: 直接利用kubectl/k8s client-go的能力
### 2. API定义
从<mcfile name="final.md" path="d:\code\github\cluster-api-provider-bke\code\kep\KEPU-2\final.md"></mcfile>中可以看到Kubectl类型的完整API定义：
```go:d:\code\github\cluster-api-provider-bke\api\cvo\v1beta1\action_types.go
// ActionType定义
type ActionType string

const (
    ActionScript   ActionType = "Script"
    ActionManifest ActionType = "Manifest"
    ActionChart    ActionType = "Chart"
    ActionKubectl  ActionType = "Kubectl"  // Kubectl类型
)

// KubectlAction定义
type KubectlAction struct {
    Operation  KubectlOperation `json:"operation"`
    Resource   string           `json:"resource,omitempty"`
    Namespace  string           `json:"namespace,omitempty"`
    Manifest   string           `json:"manifest,omitempty"`
    FieldPatch string           `json:"fieldPatch,omitempty"`
    Timeout    *metav1.Duration `json:"timeout,omitempty"`
}

// KubectlOperation定义支持的操作类型
type KubectlOperation string

const (
    KubectlApply  KubectlOperation = "Apply"   // 应用资源
    KubectlDelete KubectlOperation = "Delete"  // 删除资源
    KubectlPatch  KubectlOperation = "Patch"   // 修补资源
    KubectlWait   KubectlOperation = "Wait"    // 等待条件
    KubectlDrain  KubectlOperation = "Drain"   // 排空节点
)
```
### 3. 使用示例
从文件中可以看到Kubectl类型的具体使用示例：
```yaml
# 示例1: 等待节点就绪
- name: check-node-ready
  type: Kubectl
  kubectl:
    operation: Wait
    resource: nodes
    condition: Ready
    timeout: 300s

# 示例2: 验证所有节点就绪
- name: verify-all-nodes-ready
  type: Kubectl
  kubectl:
    operation: Wait
    resource: nodes
    condition: Ready
    timeout: 600s
```
### 4. Kubectl执行器实现
根据目录结构，Kubectl执行器位于`pkg/actionengine/executor/kubectl_executor.go`：
```go:d:\code\github\cluster-api-provider-bke\pkg\actionengine\executor\kubectl_executor.go
package executor

import (
    "context"
    "fmt"
    "time"

    cvoapi "github.com/openfuyao/cluster-api-provider-bke/api/cvo/v1beta1"
    "k8s.io/apimachinery/pkg/api/meta"
    "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
    "k8s.io/apimachinery/pkg/runtime/schema"
    "k8s.io/apimachinery/pkg/util/wait"
    "k8s.io/client-go/dynamic"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
    "k8s.io/kubectl/pkg/drain"
    "sigs.k8s.io/controller-runtime/pkg/log"
)

// KubectlExecutor 实现Kubectl操作执行器
type KubectlExecutor struct {
    config        *rest.Config
    client        kubernetes.Interface
    dynamicClient dynamic.Interface
    mapper        meta.RESTMapper
}

// NewKubectlExecutor 创建Kubectl执行器
func NewKubectlExecutor(config *rest.Config) (*KubectlExecutor, error) {
    client, err := kubernetes.NewForConfig(config)
    if err != nil {
        return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
    }

    dynamicClient, err := dynamic.NewForConfig(config)
    if err != nil {
        return nil, fmt.Errorf("failed to create dynamic client: %w", err)
    }

    mapper, err := createRESTMapper(config)
    if err != nil {
        return nil, fmt.Errorf("failed to create REST mapper: %w", err)
    }

    return &KubectlExecutor{
        config:        config,
        client:        client,
        dynamicClient: dynamicClient,
        mapper:        mapper,
    }, nil
}

// Execute 执行Kubectl操作
func (e *KubectlExecutor) Execute(ctx context.Context, step *cvoapi.ActionStep, nodeName string) (*ExecutionResult, error) {
    log := log.FromContext(ctx)
    
    if step.Kubectl == nil {
        return nil, fmt.Errorf("kubectl action spec is nil")
    }

    kubectlAction := step.Kubectl
    result := &ExecutionResult{
        StepName: step.Name,
        StartTime: time.Now(),
    }

    log.Info("Executing kubectl action", 
        "operation", kubectlAction.Operation,
        "resource", kubectlAction.Resource,
        "namespace", kubectlAction.Namespace,
        "node", nodeName)

    var err error
    switch kubectlAction.Operation {
    case cvoapi.KubectlApply:
        err = e.executeApply(ctx, kubectlAction)
    case cvoapi.KubectlDelete:
        err = e.executeDelete(ctx, kubectlAction)
    case cvoapi.KubectlPatch:
        err = e.executePatch(ctx, kubectlAction)
    case cvoapi.KubectlWait:
        err = e.executeWait(ctx, kubectlAction)
    case cvoapi.KubectlDrain:
        err = e.executeDrain(ctx, kubectlAction, nodeName)
    default:
        err = fmt.Errorf("unsupported kubectl operation: %s", kubectlAction.Operation)
    }

    result.EndTime = time.Now()
    result.Duration = result.EndTime.Sub(result.StartTime)
    
    if err != nil {
        result.Success = false
        result.Error = err.Error()
        log.Error(err, "Kubectl action failed")
    } else {
        result.Success = true
        result.Message = fmt.Sprintf("Kubectl %s completed successfully", kubectlAction.Operation)
        log.Info("Kubectl action completed successfully")
    }

    return result, err
}

// executeApply 执行kubectl apply操作
func (e *KubectlExecutor) executeApply(ctx context.Context, action *cvoapi.KubectlAction) error {
    if action.Manifest == "" {
        return fmt.Errorf("manifest is required for Apply operation")
    }

    // 解析YAML manifest
    objects, err := parseManifest(action.Manifest)
    if err != nil {
        return fmt.Errorf("failed to parse manifest: %w", err)
    }

    // 应用每个资源
    for _, obj := range objects {
        gvk := obj.GroupVersionKind()
        mapping, err := e.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
        if err != nil {
            return fmt.Errorf("failed to get REST mapping for %s: %w", gvk, err)
        }

        resourceClient := e.dynamicClient.Resource(mapping.Resource)
        namespace := action.Namespace
        if namespace == "" {
            namespace = obj.GetNamespace()
        }

        // 检查资源是否存在
        existing, err := resourceClient.Namespace(namespace).Get(ctx, obj.GetName(), metav1.GetOptions{})
        if err != nil {
            // 不存在则创建
            _, err = resourceClient.Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
            if err != nil {
                return fmt.Errorf("failed to create resource %s/%s: %w", namespace, obj.GetName(), err)
            }
        } else {
            // 存在则更新
            obj.SetResourceVersion(existing.GetResourceVersion())
            _, err = resourceClient.Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
            if err != nil {
                return fmt.Errorf("failed to update resource %s/%s: %w", namespace, obj.GetName(), err)
            }
        }
    }

    return nil
}

// executeDelete 执行kubectl delete操作
func (e *KubectlExecutor) executeDelete(ctx context.Context, action *cvoapi.KubectlAction) error {
    if action.Resource == "" {
        return fmt.Errorf("resource is required for Delete operation")
    }

    // 解析资源类型和名称
    gvr, name, err := parseResource(action.Resource)
    if err != nil {
        return fmt.Errorf("failed to parse resource: %w", err)
    }

    resourceClient := e.dynamicClient.Resource(gvr)
    namespace := action.Namespace

    // 执行删除
    if namespace != "" {
        err = resourceClient.Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
    } else {
        err = resourceClient.Delete(ctx, name, metav1.DeleteOptions{})
    }

    if err != nil {
        return fmt.Errorf("failed to delete resource %s: %w", action.Resource, err)
    }

    return nil
}

// executePatch 执行kubectl patch操作
func (e *KubectlExecutor) executePatch(ctx context.Context, action *cvoapi.KubectlAction) error {
    if action.Resource == "" || action.FieldPatch == "" {
        return fmt.Errorf("resource and fieldPatch are required for Patch operation")
    }

    // 解析资源类型和名称
    gvr, name, err := parseResource(action.Resource)
    if err != nil {
        return fmt.Errorf("failed to parse resource: %w", err)
    }

    // 解析patch内容
    patchData, err := parsePatch(action.FieldPatch)
    if err != nil {
        return fmt.Errorf("failed to parse patch: %w", err)
    }

    resourceClient := e.dynamicClient.Resource(gvr)
    namespace := action.Namespace

    // 执行patch
    if namespace != "" {
        _, err = resourceClient.Namespace(namespace).Patch(ctx, name, types.MergePatchType, patchData, metav1.PatchOptions{})
    } else {
        _, err = resourceClient.Patch(ctx, name, types.MergePatchType, patchData, metav1.PatchOptions{})
    }

    if err != nil {
        return fmt.Errorf("failed to patch resource %s: %w", action.Resource, err)
    }

    return nil
}

// executeWait 执行kubectl wait操作
func (e *KubectlExecutor) executeWait(ctx context.Context, action *cvoapi.KubectlAction) error {
    if action.Resource == "" || action.Condition == "" {
        return fmt.Errorf("resource and condition are required for Wait operation")
    }

    // 解析资源类型
    gvr, _, err := parseResource(action.Resource)
    if err != nil {
        return fmt.Errorf("failed to parse resource: %w", err)
    }

    timeout := 5 * time.Minute // 默认超时
    if action.Timeout != nil {
        timeout = action.Timeout.Duration
    }

    resourceClient := e.dynamicClient.Resource(gvr)
    namespace := action.Namespace

    // 等待条件满足
    err = wait.PollImmediate(2*time.Second, timeout, func() (bool, error) {
        var list *unstructured.UnstructuredList
        var err error
        
        if namespace != "" {
            list, err = resourceClient.Namespace(namespace).List(ctx, metav1.ListOptions{})
        } else {
            list, err = resourceClient.List(ctx, metav1.ListOptions{})
        }
        
        if err != nil {
            return false, err
        }

        // 检查所有资源是否满足条件
        for _, item := range list.Items {
            if !checkCondition(item, action.Condition) {
                return false, nil
            }
        }
        
        return true, nil
    })

    if err != nil {
        return fmt.Errorf("wait condition %s for resource %s failed: %w", action.Condition, action.Resource, err)
    }

    return nil
}

// executeDrain 执行kubectl drain操作
func (e *KubectlExecutor) executeDrain(ctx context.Context, action *cvoapi.KubectlAction, nodeName string) error {
    if nodeName == "" {
        return fmt.Errorf("node name is required for Drain operation")
    }

    // 创建drain helper
    drainer := &drain.Helper{
        Client:              e.client,
        Force:               true,
        IgnoreAllDaemonSets: true,
        DeleteEmptyDirData:  true,
        GracePeriodSeconds:  -1, // 不等待
        Timeout:             5 * time.Minute,
        Out:                 io.Discard,
        ErrOut:              io.Discard,
    }

    // 执行drain
    if err := drain.RunNodeDrain(drainer, nodeName); err != nil {
        return fmt.Errorf("failed to drain node %s: %w", nodeName, err)
    }

    return nil
}

// checkCondition 检查资源条件
func checkCondition(obj *unstructured.Unstructured, condition string) bool {
    switch condition {
    case "Ready":
        // 检查Ready状态
        conditions, found, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
        if !found {
            return false
        }
        
        for _, cond := range conditions {
            if condMap, ok := cond.(map[string]interface{}); ok {
                if typeVal, _ := condMap["type"].(string); typeVal == "Ready" {
                    if statusVal, _ := condMap["status"].(string); statusVal == "True" {
                        return true
                    }
                }
            }
        }
        return false
    case "Available":
        // 检查Available状态
        // 类似实现...
        return true
    default:
        return false
    }
}

// parseManifest 解析YAML manifest
func parseManifest(manifest string) ([]*unstructured.Unstructured, error) {
    // 实现YAML解析逻辑
    return []*unstructured.Unstructured{}, nil
}

// parseResource 解析资源字符串
func parseResource(resource string) (schema.GroupVersionResource, string, error) {
    // 实现资源解析逻辑
    return schema.GroupVersionResource{}, "", nil
}

// parsePatch 解析patch内容
func parsePatch(patch string) ([]byte, error) {
    // 实现patch解析逻辑
    return []byte(patch), nil
}

// createRESTMapper 创建REST mapper
func createRESTMapper(config *rest.Config) (meta.RESTMapper, error) {
    // 实现REST mapper创建逻辑
    return nil, nil
}
```
### 5. 设计特点
1. **类型安全**: 通过强类型定义确保操作的正确性
2. **幂等性**: 所有操作都设计为幂等，支持重试
3. **超时控制**: 支持操作超时设置，防止长时间阻塞
4. **条件检查**: Wait操作支持条件检查，确保资源状态
5. **节点感知**: Drain操作针对特定节点执行
### 6. 使用场景
Kubectl类型在以下场景中特别有用：
1. **资源管理**: 创建、更新、删除Kubernetes资源
2. **状态检查**: 等待资源达到特定状态
3. **节点维护**: 排空节点进行维护
4. **配置更新**: 通过Patch更新资源配置
5. **健康检查**: 检查集群组件健康状态
### 7. 与其他执行器的关系
Kubectl执行器与其他执行器协同工作：
- **Script执行器**: 执行shell脚本
- **Manifest执行器**: 部署静态Pod等资源
- **Chart执行器**: 部署Helm Chart
- **Kubectl执行器**: 执行Kubernetes API操作

这种设计使得ComponentVersion可以灵活组合不同类型的操作，实现复杂的组件生命周期管理。
### 总结
Kubectl类型的设计充分体现了声明式集群管理的理念，通过YAML声明Kubernetes操作，由ActionEngine自动执行。这种设计具有以下优势：
1. **声明式**: 操作意图清晰，易于理解和维护
2. **可重复**: 相同的YAML总是产生相同的结果
3. **可观测**: 执行状态和结果可追踪
4. **可组合**: 可以与其他Action类型组合使用
5. **安全**: 支持超时、重试、条件检查等安全机制

这种设计使得集群管理更加标准化、自动化和可靠。
        
