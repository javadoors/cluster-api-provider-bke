/******************************************************************
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

package command

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	agentv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkeagent/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	agentutils "gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
	l "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/log"
)

type BaseCommand struct {
	Ctx         context.Context
	Client      client.Client
	NameSpace   string
	Scheme      *runtime.Scheme
	OwnerObj    metav1.Object
	ClusterName string

	// RemoveAfterWait remove the command after wait
	RemoveAfterWait bool
	// Unique only one command can be stored in the cluster
	Unique bool
	// ForceRemove force remove the command
	ForceRemove bool

	// WaitTimeout the command wait timeout
	WaitTimeout time.Duration
	// WaitInterval the command wait interval
	WaitInterval time.Duration

	commandName string
	// Command the command obj
	Command *agentv1beta1.Command
}

// TimeoutCaseResult encapsulates the result of handleTimeoutCase function
type TimeoutCaseResult struct {
	Err          error
	Complete     bool
	SuccessNodes []string
	FailedNodes  []string
}

// CommandNodes encapsulates the success and failed nodes
type CommandNodes struct {
	SuccessNodes []string
	FailedNodes  []string
}

// WaitCommandResult encapsulates the result of waitCommandComplete function
type WaitCommandResult struct {
	Err          error
	Complete     bool
	SuccessNodes []string
	FailedNodes  []string
}

const (
	DefaultWaitTimeout  = 5 * time.Minute
	DefaultWaitInterval = 2 * time.Second
)

func (b *BaseCommand) validate() error {
	if b.Client == nil {
		return errors.New("client is nil")
	}
	if b.Scheme == nil {
		return errors.New("scheme is nil")
	}
	if b.NameSpace == "" {
		return errors.New("name space is empty")
	}
	return nil
}

// ValidateBkeCommand 验证BKE命令的通用函数，包括Nodes和BkeConfigName的验证
func ValidateBkeCommand(nodes interface{ Length() int }, bkeConfigName string, baseCommand *BaseCommand) error {
	if baseCommand.Client == nil {
		return errors.New("client is nil")
	}
	if lenNodes := nodes.Length(); lenNodes == 0 {
		return errors.New("env command except at least one node but got " + strconv.Itoa(lenNodes))
	}
	if bkeConfigName == "" {
		return errors.New("bkeConfigName is empty")
	}
	return baseCommand.validate()
}

// GenerateBkeConfigStr 生成BKE配置字符串
func GenerateBkeConfigStr(namespace, bkeConfigName string) string {
	return fmt.Sprintf("bkeConfig=%s:%s", namespace, bkeConfigName)
}

type Command interface {
	// Validate the command fields
	Validate() error
	// New create the command obj
	New() error
}

const (
	// BootstrapCommandNamePrefix all the bootstrap command Prefix,
	BootstrapCommandNamePrefix = "bootstrap-"

	// HACommandName the loadbalancer command name,
	HACommandName = "k8s-ha-deploy"

	// K8sEnvCommandName the k8s env command name,
	K8sEnvCommandName = "k8s-env-init"

	// K8sContainerdResetCommandName the k8s containerd reset command name,
	K8sContainerdResetCommandName = "k8s-containerd-reset"

	// K8sContainerdRedeployCommandName the k8s containerd redeploy command name,
	K8sContainerdRedeployCommandName = "k8s-containerd-redeploy"

	// K8sEnvDryRunCommandName the k8s env command name,
	K8sEnvDryRunCommandName = "k8s-env-dry-run"

	// K8sHostsCommandName the k8s hosts command name,
	K8sHostsCommandName = "k8s-hosts-generate"

	// K8sImagePrePullCommandName the k8s image pre pull command name,
	K8sImagePrePullCommandName = "k8s-image-pre-pull"

	// SwitchClusterCommandNamePrefix the switch cluster command name prefix,
	SwitchClusterCommandNamePrefix = "switch-cluster-"

	ResetNodeCommandNamePrefix = "reset-node-"

	UpgradeNodeCommandNamePrefix = "upgrade-node-"

	PingCommandNamePrefix = "ping-"

	CollectCertCommandNamePrefix = "collect-"
)

// two label used to select the Command,when reconcile
const (
	BKEClusterLabel = "bke.bocloud.com/cluster-command"
	BKEMachineLabel = "bke.bocloud.com/machine-command"

	MasterInitCommandLabel = "bke.bocloud.com/master-init-command"
	MasterJoinCommandLabel = "bke.bocloud.com/master-join-command"
	WorkerJoinCommandLabel = "bke.bocloud.com/worker-join-command"
)

const (
	// DefaultBackoffLimit max retry times
	DefaultBackoffLimit            = 3
	DefaultActiveDeadlineSecond    = 1000
	DefaultTTLSecondsAfterFinished = 600
)

// newCommand 创建命令对象
func (b *BaseCommand) newCommand(commandName, labelKey string, commandSpec *agentv1beta1.CommandSpec, customLabel ...string) error {
	if err := b.handleUniqueCommand(commandName); err != nil {
		return err
	}

	b.setCommandName(commandName)

	command := b.buildCommandObject(commandName, labelKey, commandSpec, customLabel)

	if err := b.setOwnerReference(command); err != nil {
		return err
	}

	return b.createCommand(command)
}

// handleUniqueCommand 处理唯一性命令，如果设置了Unique，则删除同名前缀的已有命令
func (b *BaseCommand) handleUniqueCommand(commandName string) error {
	if !b.Unique {
		return nil
	}

	// 去掉字符串的时间戳
	commandNamePrefix := utils.RemoveTimestamps(commandName)
	// get all the command
	commandList := &agentv1beta1.CommandList{}
	if err := b.Client.List(b.Ctx, commandList, client.InNamespace(b.NameSpace)); err != nil {
		return errors.Wrapf(err, "failed to list command in namespace %s", b.NameSpace)
	}
	// delete the command
	for _, command := range commandList.Items {
		if strings.HasPrefix(command.Name, commandNamePrefix) {
			if err := b.Client.Delete(b.Ctx, &command); err != nil {
				return errors.Wrapf(err, "failed to delete command %s", command.Name)
			}
			break
		}
	}
	return nil
}

// buildCommandObject 构建命令对象
func (b *BaseCommand) buildCommandObject(commandName, labelKey string, commandSpec *agentv1beta1.CommandSpec, customLabel []string) *agentv1beta1.Command {
	command := &agentv1beta1.Command{}
	command.SetGroupVersionKind(agentv1beta1.GroupVersion.WithKind("Command"))
	command.SetName(commandName)
	command.SetNamespace(b.NameSpace)
	command.Spec = *commandSpec.DeepCopy()

	// set labels
	labels := b.buildLabels(labelKey, customLabel)
	command.SetLabels(labels)

	return command
}

// buildLabels 构建标签映射
func (b *BaseCommand) buildLabels(labelKey string, customLabel []string) map[string]string {
	labels := map[string]string{
		labelKey: "",
	}
	if b.ClusterName != "" {
		labels[clusterv1.ClusterNameLabel] = b.ClusterName
	}
	for _, label := range customLabel {
		labels[label] = ""
	}
	return labels
}

// setOwnerReference 设置所有者引用
func (b *BaseCommand) setOwnerReference(command *agentv1beta1.Command) error {
	if b.OwnerObj == nil {
		return nil
	}
	if err := controllerutil.SetControllerReference(b.OwnerObj, command, b.Scheme); err != nil {
		return errors.Wrapf(err, "failed to set controller reference for owner %s, controlled %s", b.OwnerObj.GetName(), command.Name)
	}
	return nil
}

// createCommand 创建命令
func (b *BaseCommand) createCommand(command *agentv1beta1.Command) error {
	if err := b.Client.Create(b.Ctx, command); err != nil {
		if apierrors.IsAlreadyExists(err) {
			_, err := b.GetCommand()
			return err
		}
		return errors.Wrapf(err, "failed to create command %s", command.Name)
	}
	return nil
}

func (b *BaseCommand) setCommandName(commandName string) {
	b.commandName = commandName
}

func (b *BaseCommand) GetCommand() (*agentv1beta1.Command, error) {
	if b.commandName == "" {
		return nil, errors.New("command name is empty")
	}
	command := &agentv1beta1.Command{}
	if err := b.Client.Get(b.Ctx, client.ObjectKey{Namespace: b.NameSpace, Name: b.commandName}, command); err != nil {
		return nil, errors.Wrapf(err, "failed to get command %s", b.commandName)
	}
	b.Command = command
	return command, nil
}

func (b *BaseCommand) deleteCommand() error {
	if b.Command == nil {
		return nil
	}
	if b.ForceRemove {
		patchHelper, err := patch.NewHelper(b.Command, b.Client)
		if err == nil {
			// remove finalizer and delete
			controllerutil.RemoveFinalizer(b.Command, "command.bkeagent.bocloud.com/finalizers")
			if err := patchHelper.Patch(b.Ctx, b.Command); err != nil {
				log.Warn(constant.ReconcileErrorReason, "failed to remove finalizer: %v", err)
			}
		} else {
			log.Warn(constant.ReconcileErrorReason, "failed to create patch helper: %v", err)
		}
	}

	if err := b.Client.Delete(b.Ctx, b.Command); err != nil {
		return errors.Wrapf(err, "failed to update command %s", b.Command.Name)
	}
	return nil
}

// waitCommandComplete 等待命令完成
func (b *BaseCommand) waitCommandComplete() (error, bool, CommandNodes) {
	result := b.waitCommandCompleteWithStruct()
	return result.Err, result.Complete, CommandNodes{
		SuccessNodes: result.SuccessNodes,
		FailedNodes:  result.FailedNodes,
	}
}

// waitCommandCompleteWithStruct 等待命令完成，返回结构体
func (b *BaseCommand) waitCommandCompleteWithStruct() WaitCommandResult {
	if b.commandName == "" {
		return WaitCommandResult{
			Err:          errors.New("command name is empty"),
			Complete:     false,
			SuccessNodes: nil,
			FailedNodes:  nil,
		}
	}
	if b.WaitInterval == 0 {
		b.WaitInterval = DefaultWaitInterval
	}
	if b.WaitTimeout == 0 {
		b.WaitTimeout = DefaultWaitTimeout
	}

	var complete bool
	var successNodes []string
	var failedNodes []string

	// 等待命令完成
	err := b.waitForCommandCompletion(&complete, &successNodes, &failedNodes)

	// 处理超时情况
	if errors.Is(err, wait.ErrWaitTimeout) {
		timeoutResult := b.handleTimeoutCase(complete, successNodes, failedNodes)
		err = timeoutResult.Err
		complete = timeoutResult.Complete
		successNodes = timeoutResult.SuccessNodes
		failedNodes = timeoutResult.FailedNodes
	}

	// 如果需要等待后删除，则执行删除操作
	if b.RemoveAfterWait {
		if deleteErr := b.deleteCommand(); deleteErr != nil {
			l.Warnf("delete command %s failed: %v", b.commandName, deleteErr)
		}
	}

	return WaitCommandResult{
		Err:          err,
		Complete:     complete,
		SuccessNodes: successNodes,
		FailedNodes:  failedNodes,
	}
}

// waitForCommandCompletion 内部函数：等待命令执行完成
func (b *BaseCommand) waitForCommandCompletion(complete *bool, successNodes, failedNodes *[]string) error {
	ctxTimeout, cancel := context.WithTimeout(b.Ctx, b.WaitTimeout)
	defer cancel()

	err := wait.PollImmediateUntil(b.WaitInterval, func() (bool, error) {
		command, err := b.GetCommand()
		if err != nil {
			return false, nil
		}
		*complete, *successNodes, *failedNodes = CheckCommandStatus(command)
		if *complete {
			return true, nil
		}
		return false, nil
	}, ctxTimeout.Done())

	return err
}

// handleTimeoutCase 处理命令执行超时的情况
func (b *BaseCommand) handleTimeoutCase(originalComplete bool, originalSuccessNodes, originalFailedNodes []string) TimeoutCaseResult {
	// 超时不返回错误
	var err error
	complete := originalComplete
	successNodes := originalSuccessNodes
	failedNodes := originalFailedNodes

	command, cmdErr := b.GetCommand()
	if cmdErr != nil {
		return TimeoutCaseResult{
			Err:          cmdErr,
			Complete:     complete,
			SuccessNodes: successNodes,
			FailedNodes:  failedNodes,
		}
	}

	// 复制成功节点列表以安全地进行操作
	successNodesCopy := make([]string, len(successNodes))
	copy(successNodesCopy, successNodes)

	// 检查未完成的节点
	for key := range command.Spec.NodeSelector.MatchLabels {
		found := false
		var newSuccessNodesCopy []string

		// 检查当前键是否存在于成功节点中
		for _, node := range successNodesCopy {
			if strings.Contains(node, key) {
				found = true
				break
			} else {
				newSuccessNodesCopy = append(newSuccessNodesCopy, node)
			}
		}
		successNodesCopy = newSuccessNodesCopy

		// 如果未在成功节点中找到，添加到失败节点
		if !found {
			failedNodes = append(failedNodes, key)
		}
	}

	successNodes = successNodesCopy

	return TimeoutCaseResult{
		Err:          err,
		Complete:     complete,
		SuccessNodes: successNodes,
		FailedNodes:  failedNodes,
	}
}

// ClusterNameLabelSelectorRequirement return the label selector requirement
// Deprecated
func (b *BaseCommand) ClusterNameLabelSelectorRequirement() metav1.LabelSelectorRequirement {
	return metav1.LabelSelectorRequirement{
		Key:      agentutils.ClusterNameLabelKey,
		Operator: metav1.LabelSelectorOpIn,
		Values:   []string{b.ClusterName},
	}
}

func GenerateDefaultCommandSpec() *agentv1beta1.CommandSpec {
	return &agentv1beta1.CommandSpec{
		NodeName:                "",
		Suspend:                 false,
		Commands:                []agentv1beta1.ExecCommand{},
		BackoffLimit:            DefaultBackoffLimit,
		ActiveDeadlineSecond:    DefaultActiveDeadlineSecond,
		TTLSecondsAfterFinished: DefaultTTLSecondsAfterFinished,
		NodeSelector:            &metav1.LabelSelector{},
	}
}

// ValidateCommand validate the command
func ValidateCommand(c *agentv1beta1.Command) error {
	if c.Spec.NodeName == "" && c.Spec.NodeSelector.String() == "" {
		return errors.New("not a valid command,at least provide a node name or NodeSelector")
	}
	// todo: validate this command
	return nil
}

// CheckCommandStatus check all the command is success or not at agentv1beta1.Status
// if all command exec completed,return true , successNodes and failedNodes
// else return false
func CheckCommandStatus(c *agentv1beta1.Command) (complete bool, successNodes []string, failedNodes []string) {
	if len(c.Status) == 0 || c.Status == nil {
		return
	}

	complete = true
	if c.Spec.Suspend {
		complete = false
		return
	}
	count := 0

	for nodeName, commandStatus := range c.Status {
		count++
		switch {
		case commandStatus.Phase == agentv1beta1.CommandSuspend:
			complete = false
			return
		case commandStatus.Phase == agentv1beta1.CommandRunning:
			complete = false
			return
		case commandStatus.Phase == agentv1beta1.CommandFailed:
			failedNodes = append(failedNodes, nodeName)
		case commandStatus.Status != metav1.ConditionTrue:
			failedNodes = append(failedNodes, nodeName)
		case commandStatus.Failed > 0:
			failedNodes = append(failedNodes, nodeName)
		// todo add more case
		default:
			successNodes = append(successNodes, nodeName)
		}
	}
	if len(successNodes)+len(failedNodes) != count {
		complete = false
	}

	if count == 0 || len(c.Spec.NodeSelector.MatchLabels) != count {
		complete = false
	}

	return
}

// IsOwnerRefCommand returns a bool ,if object is owner of the command
func IsOwnerRefCommand(object metav1.Object, command agentv1beta1.Command) bool {
	for _, ref := range command.GetOwnerReferences() {
		if ref.UID == object.GetUID() {
			return true
		}
	}
	return false
}

func getNodeSelector(nodes bkenode.Nodes) *metav1.LabelSelector {
	nodeSelector := &metav1.LabelSelector{}
	for _, node := range nodes {
		metav1.AddLabelToSelector(nodeSelector, node.IP, node.IP)
	}
	return nodeSelector
}
