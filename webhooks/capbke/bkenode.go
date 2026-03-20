/*
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 */

package capbke

import (
	"context"
	"fmt"
	"reflect"

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	bkevalidate "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/validation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/common/security"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/nodeutil"
)

var bkeNodeLog = log.Named("BKENode")

// bkeNodeGR is the GroupResource for BKENode, derived from BKENodeGVK
var bkeNodeGR = schema.GroupResource{Group: confv1beta1.BKENodeGVK.Group, Resource: "bkenodes"}

// BKENode implements webhook for BKENode CRD
type BKENode struct {
	Client client.Client
}

func (webhook *BKENode) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&confv1beta1.BKENode{}).
		WithValidator(webhook).
		WithDefaulter(webhook).
		Complete()
}

//+kubebuilder:webhook:verbs=create;update,path=/mutate-bke-bocloud-com-v1beta1-bkenode,mutating=true,failurePolicy=fail,sideEffects=None,groups=bke.bocloud.com,resources=bkenodes,versions=v1beta1,name=mbkenode.kb.io,admissionReviewVersions=v1
//+kubebuilder:webhook:verbs=create;update;delete,path=/validate-bke-bocloud-com-v1beta1-bkenode,mutating=false,failurePolicy=fail,sideEffects=None,groups=bke.bocloud.com,resources=bkenodes,versions=v1beta1,name=vbkenode.kb.io,admissionReviewVersions=v1

var _ webhook.CustomDefaulter = &BKENode{}
var _ webhook.CustomValidator = &BKENode{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (webhook *BKENode) Default(ctx context.Context, obj runtime.Object) error {
	bkeNode, ok := obj.(*confv1beta1.BKENode)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a BKENode but got a %T", obj))
	}

	bkeNodeLog.Debugf("Defaulting BKENode %s/%s", bkeNode.Namespace, bkeNode.Name)

	// 设置默认端口
	if bkeNode.Spec.Port == "" {
		bkeNode.Spec.Port = "22"
	}

	// 加密密码
	if bkeNode.Spec.Password != "" {
		encryptedPassword, err := encryptPasswordIfNeeded(bkeNode.Spec.Password)
		if err != nil {
			bkeNodeLog.Warnf("Failed to encrypt password for node %s: %v", bkeNode.Spec.IP, err)
		} else {
			bkeNode.Spec.Password = encryptedPassword
		}
	}

	return nil
}

// encryptPasswordIfNeeded 如果密码未加密则进行加密
func encryptPasswordIfNeeded(password string) (string, error) {
	// 尝试解密，如果成功说明已经加密
	_, err := security.AesDecrypt(password)
	if err == nil {
		// 已加密，返回原始密码
		return password, nil
	}

	// 未加密，进行加密
	encrypted, err := security.AesEncrypt(password)
	if err != nil {
		return password, err
	}
	return encrypted, nil
}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (webhook *BKENode) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	bkeNode, ok := obj.(*confv1beta1.BKENode)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected a BKENode but got a %T", obj))
	}

	bkeNodeLog.Debugf("Validating BKENode create %s/%s", bkeNode.Namespace, bkeNode.Name)

	// 验证节点配置
	if err := webhook.validateBKENodeSpec(bkeNode); err != nil {
		return nil, err
	}

	// 验证节点 IP 唯一性
	if err := webhook.validateNodeIPUnique(ctx, bkeNode); err != nil {
		return nil, err
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (webhook *BKENode) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldBKENode, ok := oldObj.(*confv1beta1.BKENode)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected a BKENode but got a %T", oldObj))
	}
	newBKENode, ok := newObj.(*confv1beta1.BKENode)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected a BKENode but got a %T", newObj))
	}

	bkeNodeLog.Debugf("Validating BKENode update %s/%s", newBKENode.Namespace, newBKENode.Name)

	// 验证节点配置
	if err := webhook.validateBKENodeSpec(newBKENode); err != nil {
		return nil, err
	}

	// IP 不允许变更
	if oldBKENode.Spec.IP != newBKENode.Spec.IP {
		return nil, apierrors.NewForbidden(
			bkeNodeGR,
			newBKENode.Name,
			errors.New("BKENode IP cannot be changed after creation"),
		)
	}

	// 如果节点正在操作中，限制某些字段的变更
	if err := webhook.validateNodeStateTransition(oldBKENode, newBKENode); err != nil {
		return nil, err
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (webhook *BKENode) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	bkeNode, ok := obj.(*confv1beta1.BKENode)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected a BKENode but got a %T", obj))
	}

	bkeNodeLog.Debugf("Validating BKENode delete %s/%s", bkeNode.Namespace, bkeNode.Name)

	// 检查节点是否处于可删除状态
	if err := webhook.validateNodeDeletable(bkeNode); err != nil {
		return nil, err
	}

	return nil, nil
}

// validateBKENodeSpec 验证 BKENode 规格
func (webhook *BKENode) validateBKENodeSpec(bkeNode *confv1beta1.BKENode) error {
	// 转换为 Node 类型进行验证
	n := bkenode.Node(bkeNode.ToNode())
	if err := bkevalidate.ValidateSingleNode(n); err != nil {
		return apierrors.NewBadRequest(fmt.Sprintf("invalid BKENode spec: %v", err))
	}
	return nil
}

// validateNodeIPUnique 验证节点 IP 在集群中唯一
func (webhook *BKENode) validateNodeIPUnique(ctx context.Context, bkeNode *confv1beta1.BKENode) error {
	clusterName := bkeNode.Labels[nodeutil.ClusterNameLabel]
	if clusterName == "" {
		return apierrors.NewBadRequest("BKENode must have cluster.x-k8s.io/cluster-name label")
	}

	// 获取同一集群下的所有 BKENode
	bkeNodeList := &confv1beta1.BKENodeList{}
	if err := webhook.Client.List(ctx, bkeNodeList,
		client.InNamespace(bkeNode.Namespace),
		client.MatchingLabels{nodeutil.ClusterNameLabel: clusterName},
	); err != nil {
		return apierrors.NewInternalError(err)
	}

	// 检查 IP 是否重复
	for _, existingNode := range bkeNodeList.Items {
		if existingNode.Name == bkeNode.Name {
			continue
		}
		if existingNode.Spec.IP == bkeNode.Spec.IP {
			return apierrors.NewConflict(
				bkeNodeGR,
				bkeNode.Name,
				fmt.Errorf("node with IP %s already exists in cluster %s", bkeNode.Spec.IP, clusterName),
			)
		}
	}

	return nil
}

// validateNodeStateTransition 验证节点状态转换
func (webhook *BKENode) validateNodeStateTransition(oldNode, newNode *confv1beta1.BKENode) error {
	// 如果节点正在操作中（Pending 或 Upgrading），不允许修改角色
	if oldNode.Status.State == confv1beta1.NodePending || oldNode.Status.State == confv1beta1.NodeUpgrading {
		if !reflect.DeepEqual(oldNode.Spec.Role, newNode.Spec.Role) {
			return apierrors.NewForbidden(
				bkeNodeGR,
				newNode.Name,
				errors.New("cannot change node role while node is in pending or upgrading state"),
			)
		}
	}

	return nil
}

// validateNodeDeletable 验证节点是否可删除
func (webhook *BKENode) validateNodeDeletable(bkeNode *confv1beta1.BKENode) error {
	// 正在操作中（Pending 或 Upgrading）的节点不能删除
	if bkeNode.Status.State == confv1beta1.NodePending {
		return apierrors.NewForbidden(
			bkeNodeGR,
			bkeNode.Name,
			errors.New("cannot delete node while in pending state"),
		)
	}

	// 正在升级中的节点不能删除
	if bkeNode.Status.State == confv1beta1.NodeUpgrading {
		return apierrors.NewForbidden(
			bkeNodeGR,
			bkeNode.Name,
			errors.New("cannot delete node while upgrading"),
		)
	}

	return nil
}
