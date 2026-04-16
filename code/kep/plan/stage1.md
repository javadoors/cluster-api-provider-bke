
#  阶段一：基础层 — 统一错误处理 + CRD 扩展 详细设计
## 阶段一：基础层 — 统一错误处理 + CRD 扩展 详细设计
### 一、当前错误处理问题分析
通过分析代码，当前错误处理存在以下问题：

| 问题 | 位置 | 影响 |
|------|------|------|
| 错误类型无区分 | [bkecluster_controller.go:123](file:///d:/code/github/cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go#L123) `handleClusterError` 仅区分 NotFound 和其他 | 无法实现差异化重试策略 |
| RequeueAfter 硬编码 | [ensure_worker_delete.go:374](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_delete.go#L374) 等处 | 不同错误场景使用相同重试间隔 |
| Phase 执行错误聚合粗糙 | [phase_flow.go:329](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L329) `kerrors.NewAggregate` | 无法区分可恢复/不可恢复错误 |
| 错误信息丢失上下文 | 各 Phase Execute 中 `errors.Wrapf` | 缺少结构化的错误原因和推荐操作 |
| 无统一重试策略 | Controller 层直接返回 error | controller-runtime 使用默认 backoff |
### 二、ReconcileError 类型系统设计
#### 2.1 错误类型定义
新建文件 `pkg/errors/errors.go`：
```go
package errors

import (
	"fmt"
	"time"

	"github.com/pkg/errors"
)

type ReconcileErrorType string

const (
	ReconcileErrorTypeTransient  ReconcileErrorType = "Transient"
	ReconcileErrorTypePermanent  ReconcileErrorType = "Permanent"
	ReconcileErrorTypeConflict   ReconcileErrorType = "Conflict"
	ReconcileErrorTypeDependency ReconcileErrorType = "Dependency"
)

type ReconcileError struct {
	errType     ReconcileErrorType
	message     string
	cause       error
	retryAfter  time.Duration
	reason      string
	phaseName   string
}

func (e *ReconcileError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.errType, e.message, e.cause)
	}
	return fmt.Sprintf("[%s] %s", e.errType, e.message)
}

func (e *ReconcileError) Unwrap() error {
	return e.cause
}

func (e *ReconcileError) Type() ReconcileErrorType {
	return e.errType
}

func (e *ReconcileError) RetryAfter() time.Duration {
	return e.retryAfter
}

func (e *ReconcileError) Reason() string {
	return e.reason
}

func (e *ReconcileError) PhaseName() string {
	return e.phaseName
}

func IsTransientError(err error) bool {
	var reconcileErr *ReconcileError
	if errors.As(err, &reconcileErr) {
		return reconcileErr.Type() == ReconcileErrorTypeTransient
	}
	return false
}

func IsPermanentError(err error) bool {
	var reconcileErr *ReconcileError
	if errors.As(err, &reconcileErr) {
		return reconcileErr.Type() == ReconcileErrorTypePermanent
	}
	return false
}

func IsConflictError(err error) bool {
	var reconcileErr *ReconcileError
	if errors.As(err, &reconcileErr) {
		return reconcileErr.Type() == ReconcileErrorTypeConflict
	}
	return false
}

func IsDependencyError(err error) bool {
	var reconcileErr *ReconcileError
	if errors.As(err, &reconcileErr) {
		return reconcileErr.Type() == ReconcileErrorTypeDependency
	}
	return false
}

func GetRetryAfter(err error) time.Duration {
	var reconcileErr *ReconcileError
	if errors.As(err, &reconcileErr) {
		return reconcileErr.RetryAfter()
	}
	return 0
}

func NewTransient(message string, opts ...ReconcileErrorOption) *ReconcileError {
	return newReconcileError(ReconcileErrorTypeTransient, message, opts...)
}

func NewPermanent(message string, opts ...ReconcileErrorOption) *ReconcileError {
	return newReconcileError(ReconcileErrorTypePermanent, message, opts...)
}

func NewConflict(message string, opts ...ReconcileErrorOption) *ReconcileError {
	return newReconcileError(ReconcileErrorTypeConflict, message, opts...)
}

func NewDependency(message string, opts ...ReconcileErrorOption) *ReconcileError {
	return newReconcileError(ReconcileErrorTypeDependency, message, opts...)
}

func WrapTransient(cause error, message string, opts ...ReconcileErrorOption) *ReconcileError {
	opts = append(opts, WithCause(cause))
	return newReconcileError(ReconcileErrorTypeTransient, message, opts...)
}

func WrapPermanent(cause error, message string, opts ...ReconcileErrorOption) *ReconcileError {
	opts = append(opts, WithCause(cause))
	return newReconcileError(ReconcileErrorTypePermanent, message, opts...)
}

func WrapConflict(cause error, message string, opts ...ReconcileErrorOption) *ReconcileError {
	opts = append(opts, WithCause(cause))
	return newReconcileError(ReconcileErrorTypeConflict, message, opts...)
}

func WrapDependency(cause error, message string, opts ...ReconcileErrorOption) *ReconcileError {
	opts = append(opts, WithCause(cause))
	return newReconcileError(ReconcileErrorTypeDependency, message, opts...)
}

func newReconcileError(errType ReconcileErrorType, message string, opts ...ReconcileErrorOption) *ReconcileError {
	re := &ReconcileError{
		errType: errType,
		message: message,
	}
	for _, opt := range opts {
		opt(re)
	}
	if re.retryAfter == 0 {
		re.retryAfter = defaultRetryAfter(errType)
	}
	return re
}

func defaultRetryAfter(errType ReconcileErrorType) time.Duration {
	switch errType {
	case ReconcileErrorTypeTransient:
		return 30 * time.Second
	case ReconcileErrorTypeConflict:
		return 5 * time.Second
	case ReconcileErrorTypeDependency:
		return 60 * time.Second
	default:
		return 0
	}
}

type ReconcileErrorOption func(*ReconcileError)

func WithRetryAfter(d time.Duration) ReconcileErrorOption {
	return func(e *ReconcileError) {
		e.retryAfter = d
	}
}

func WithCause(cause error) ReconcileErrorOption {
	return func(e *ReconcileError) {
		e.cause = cause
	}
}

func WithReason(reason string) ReconcileErrorOption {
	return func(e *ReconcileError) {
		e.reason = reason
	}
}

func WithPhaseName(phaseName string) ReconcileErrorOption {
	return func(e *ReconcileError) {
		e.phaseName = phaseName
	}
}
```
#### 2.2 错误类型语义说明
| 错误类型 | 含义 | 默认重试间隔 | 典型场景 |
|----------|------|-------------|---------|
| **Transient** | 临时性错误，自动重试可恢复 | 30s | 网络超时、API Server 暂时不可用、Agent 未就绪 |
| **Permanent** | 永久性错误，需人工介入 | 0（不重试） | 配置无效、版本不兼容、证书过期 |
| **Conflict** | 资源冲突，短时间后重试 | 5s | Status 更新冲突、资源版本冲突 |
| **Dependency** | 依赖未满足，等待后重试 | 60s | 前置 Phase 未完成、BKENode 未就绪 |
#### 2.3 Controller 层错误处理集成
修改 [bkecluster_controller.go](file:///d:/code/github/cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go) 中的 `handleClusterError` 和 `Reconcile` 方法：

**当前代码**（[bkecluster_controller.go:123-128](file:///d:/code/github/cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go#L123)）：
```go
func (r *BKEClusterReconciler) handleClusterError(err error) (ctrl.Result, error) {
    if apierrors.IsNotFound(err) {
        return ctrl.Result{}, nil
    }
    return ctrl.Result{}, err
}
```
**重构后**：
```go
func (r *BKEClusterReconciler) handleReconcileError(err error) (ctrl.Result, error) {
    if err == nil {
        return ctrl.Result{}, nil
    }

    if apierrors.IsNotFound(err) {
        return ctrl.Result{}, nil
    }

    if bkeerrors.IsPermanentError(err) {
        return ctrl.Result{}, nil
    }

    if bkeerrors.IsConflictError(err) {
        return ctrl.Result{RequeueAfter: bkeerrors.GetRetryAfter(err)}, nil
    }

    if bkeerrors.IsTransientError(err) {
        return ctrl.Result{RequeueAfter: bkeerrors.GetRetryAfter(err)}, nil
    }

    if bkeerrors.IsDependencyError(err) {
        return ctrl.Result{RequeueAfter: bkeerrors.GetRetryAfter(err)}, nil
    }

    if apierrors.IsConflict(err) {
        return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
    }

    return ctrl.Result{}, err
}
```
#### 2.4 Phase 层错误转换适配
在 Phase 执行中，将现有 `errors.Wrapf` 转换为结构化错误。以 `ensure_master_init.go` 为例，展示适配模式（不修改所有 Phase，仅提供适配层）：
```go
// 在 pkg/phaseframe/phases 中新增 errors_adapter.go
package phases

import (
	bkeerrors "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func classifyError(err error, phaseName string) error {
	if err == nil {
		return nil
	}

	var reconcileErr *bkeerrors.ReconcileError
	if errors.As(err, &reconcileErr) {
		return err
	}

	if apierrors.IsConflict(err) {
		return bkeerrors.WrapConflict(err, "resource conflict",
			bkeerrors.WithPhaseName(phaseName))
	}

	if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) {
		return bkeerrors.WrapTransient(err, "api server timeout",
			bkeerrors.WithPhaseName(phaseName))
	}

	if apierrors.IsServiceUnavailable(err) {
		return bkeerrors.WrapTransient(err, "service unavailable",
			bkeerrors.WithPhaseName(phaseName))
	}

	return bkeerrors.WrapTransient(err, "unexpected error",
		bkeerrors.WithPhaseName(phaseName))
}
```
### 三、CRD 扩展设计
#### 3.1 BKEClusterSpec 扩展
在 [bkecluster_spec.go](file:///d:/code/github/cluster-api-provider-bke/api/bkecommon/v1beta1/bkecluster_spec.go) 中扩展：

**新增类型定义**：
```go
type InfrastructureMode string

const (
	InfrastructureModeIPI InfrastructureMode = "IPI"
	InfrastructureModeUPI InfrastructureMode = "UPI"
)

type UserProvidedInfrastructure struct {
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint"`

	LoadBalancer *UserProvidedLoadBalancer `json:"loadBalancer,omitempty"`

	Etcd *UserProvidedEtcd `json:"etcd,omitempty"`

	NetworkConfig *UserProvidedNetworkConfig `json:"networkConfig,omitempty"`
}

type UserProvidedLoadBalancer struct {
	Endpoint string `json:"endpoint"`

	AuthSecretRef *AuthSecretRef `json:"authSecretRef,omitempty"`
}

type UserProvidedEtcd struct {
	Endpoints []string `json:"endpoints"`

	CASecretRef *AuthSecretRef `json:"caSecretRef,omitempty"`
}

type UserProvidedNetworkConfig struct {
	CNIPlugin string `json:"cniPlugin,omitempty"`
}
```

**BKEClusterSpec 新增字段**：
```go
type BKEClusterSpec struct {
    // ... 现有字段保持不变 ...

    // InfrastructureMode defines the infrastructure provisioning mode.
    // IPI: Installer-Provisioned Infrastructure (default)
    // UPI: User-Provisioned Infrastructure
    // +optional
    // +kubebuilder:default:=IPI
    // +kubebuilder:validation:Enum=IPI;UPI
    InfrastructureMode InfrastructureMode `json:"infrastructureMode,omitempty"`

    // UserProvidedInfrastructure defines the user-provided infrastructure configuration.
    // Only used when InfrastructureMode is UPI.
    // +optional
    UserProvidedInfrastructure *UserProvidedInfrastructure `json:"userProvidedInfrastructure,omitempty"`
}
```
#### 3.2 BKEClusterStatus 扩展
在 [bkecluster_status.go](file:///d:/code/github/cluster-api-provider-bke/api/bkecommon/v1beta1/bkecluster_status.go) 中扩展：
```go
type BKEClusterStatus struct {
    // ... 现有字段保持不变 ...

    // UpgradeStatus tracks the current upgrade operation details.
    // +optional
    UpgradeStatus *UpgradeStatus `json:"upgradeStatus,omitempty"`

    // ObservedGeneration is the latest generation observed by the controller.
    // +optional
    ObservedGeneration int64 `json:"observedGeneration,omitempty"`

    // LastReconcileError records the last reconcile error if any.
    // +optional
    LastReconcileError *ReconcileErrorStatus `json:"lastReconcileError,omitempty"`
}

type UpgradeStatus struct {
    // SourceVersion is the version before upgrade
    // +optional
    SourceVersion string `json:"sourceVersion,omitempty"`

    // TargetVersion is the target version of the upgrade
    // +optional
    TargetVersion string `json:"targetVersion,omitempty"`

    // Phase indicates the current upgrade phase
    // +optional
    Phase UpgradePhase `json:"phase,omitempty"`

    // StartTime is when the upgrade started
    // +optional
    StartTime *metav1.Time `json:"startTime,omitempty"`

    // CompletedPhases tracks which upgrade phases have completed
    // +optional
    CompletedPhases []UpgradePhaseDetail `json:"completedPhases,omitempty"`
}

type UpgradePhase string

const (
    UpgradePhasePreCheck    UpgradePhase = "PreCheck"
    UpgradePhaseEtcd        UpgradePhase = "Etcd"
    UpgradePhaseControlPlane UpgradePhase = "ControlPlane"
    UpgradePhaseWorker      UpgradePhase = "Worker"
    UpgradePhaseAddon       UpgradePhase = "Addon"
    UpgradePhasePostCheck   UpgradePhase = "PostCheck"
)

type UpgradePhaseDetail struct {
    Name      UpgradePhase  `json:"name"`
    Status    BKEClusterPhaseStatus `json:"status"`
    StartTime *metav1.Time  `json:"startTime,omitempty"`
    EndTime   *metav1.Time  `json:"endTime,omitempty"`
    Message   string        `json:"message,omitempty"`
}

type ReconcileErrorStatus struct {
    Type      ReconcileErrorType `json:"type"`
    Message   string             `json:"message,omitempty"`
    Reason    string             `json:"reason,omitempty"`
    PhaseName string             `json:"phaseName,omitempty"`
    Timestamp metav1.Time        `json:"timestamp,omitempty"`
}

type ReconcileErrorType string

const (
    ReconcileErrorTypeTransient  ReconcileErrorType = "Transient"
    ReconcileErrorTypePermanent  ReconcileErrorType = "Permanent"
    ReconcileErrorTypeConflict   ReconcileErrorType = "Conflict"
    ReconcileErrorTypeDependency ReconcileErrorType = "Dependency"
)
```
### 四、兼容性保障设计
#### 4.1 InfrastructureMode 默认值处理
```go
func (s *BKEClusterSpec) GetInfrastructureMode() InfrastructureMode {
	if s.InfrastructureMode == "" {
		return InfrastructureModeIPI
	}
	return s.InfrastructureMode
}

func (s *BKEClusterSpec) IsUPI() bool {
	return s.GetInfrastructureMode() == InfrastructureModeUPI
}
```
#### 4.2 Phase NeedExecute 兼容
在 [base.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/base.go) 的 `DefaultNeedExecute` 中增加 UPI 模式判断：
```go
func (b *BasePhase) DefaultNeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	if !b.checkCommonNeedExecute(new) {
		return false
	}

	if !clusterutil.IsBKECluster(new) && !clusterutil.FullyControlled(new) {
		return false
	}

	if new.Spec.IsUPI() {
		return b.upiNeedExecute(new)
	}

	return true
}

func (b *BasePhase) upiNeedExecute(new *bkev1beta1.BKECluster) bool {
	upiSkippedPhases := confv1beta1.BKEClusterPhases{
		EnsureNodesEnvName,
		EnsureLoadBalanceName,
		EnsureClusterAPIObjName,
	}
	return !b.Name().In(upiSkippedPhases)
}
```
### 五、实现步骤
| 步骤 | 内容 | 涉及文件 | 风险 |
|------|------|---------|------|
| 1 | 创建 `pkg/errors/errors.go` | 新文件 | 无 |
| 2 | 创建 `pkg/errors/errors_test.go` | 新文件 | 无 |
| 3 | 修改 Controller 错误处理 | `controllers/capbke/bkecluster_controller.go` | 低 — 仅修改错误处理函数 |
| 4 | 扩展 BKEClusterSpec | `api/bkecommon/v1beta1/bkecluster_spec.go` | 低 — 新增可选字段，默认值保持兼容 |
| 5 | 扩展 BKEClusterStatus | `api/bkecommon/v1beta1/bkecluster_status.go` | 低 — 新增可选字段 |
| 6 | 添加 Phase 层错误适配器 | `pkg/phaseframe/phases/errors_adapter.go` | 无 — 新文件 |
| 7 | 运行 `make generate` 更新 CRD | 自动生成 | 需验证 CRD 兼容性 |
| 8 | 运行现有测试验证 | 全项目 | 需全部通过 |
### 六、验证标准
1. **功能等价**：所有现有 Phase 执行路径不变，错误行为不变
2. **默认兼容**：`InfrastructureMode` 默认为 `IPI`，现有集群无需修改
3. **错误分类**：`ReconcileError` 可正确分类所有错误类型
4. **重试策略**：不同错误类型使用不同 `RequeueAfter`
5. **测试覆盖**：`pkg/errors/errors_test.go` 覆盖所有错误类型构造和判断

