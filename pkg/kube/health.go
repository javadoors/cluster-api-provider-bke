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

package kube

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/mfutil"
	labelhelper "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/label"
)

func (c *Client) CheckClusterHealth(cluster *bkev1beta1.BKECluster, currentVersion string, bkeNodes bkev1beta1.BKENodes) error {
	log := c.Log
	// check cluster health
	nodeLi, err := c.ListNodes(nil)
	if err != nil {
		return err
	}
	if currentVersion == "" {
		currentVersion = cluster.Spec.ClusterConfig.Cluster.KubernetesVersion
	}
	var errs []error
	for _, node := range nodeLi.Items {
		nodeIP := GetNodeIP(&node)
		if bkeNodes.GetNodeStateNeedSkip(nodeIP) {
			log.Debugf("node %q (IP: %s) health check skipped due to needskip=true", node.Name, nodeIP)
			continue
		}

		if NodeReady(&node) {
			bkeNodes.SetNodeStateWithMessage(GetNodeIP(&node), confv1beta1.NodeReady, "")
		}

		if err := c.NodeHealthCheck(&node, currentVersion, log); err != nil {
			bkeNodes.SetNodeStateWithMessage(GetNodeIP(&node), confv1beta1.NodeNotReady, err.Error())
			log.Debugf("node %q health check failed: %v", node.Name, err)
			errs = append(errs, errors.Errorf("node %q health check failed: %v", node.Name, err))
		}
	}

	if err = c.CheckAllComponentsHealth(cluster, log); err != nil {
		errs = append(errs, err)
		return kerrors.NewAggregate(errs)
	}

	if len(errs) > 0 {
		return kerrors.NewAggregate(errs)
	}

	log.Infof("cluster %q health check pass", cluster.Name)
	return nil
}

func (c *Client) CheckComponentHealth(node *corev1.Node) error {
	var errs []error
	for _, component := range mfutil.GetControlPlaneComponents() {
		pod, err := c.GetPod(metav1.NamespaceSystem, StaticPodName(component, node.Name))
		if err != nil {
			errs = append(errs, errors.Errorf("get pod %s/%s failed: %v", metav1.NamespaceSystem, StaticPodName(component, node.Name), err))
			continue
		}
		if pod.Status.Phase != corev1.PodRunning {
			errs = append(errs, errors.Errorf("pod %s/%s is not in running phase, current phase is %q", metav1.NamespaceSystem, StaticPodName(component, node.Name), pod.Status.Phase))
		}
	}
	return kerrors.NewAggregate(errs)
}

// ComponentCheck 定义需要检查的命名空间和对应的 Pod 前缀
type ComponentCheck struct {
	Namespace string
	Prefixes  []string
}

type AddonCheck struct {
	Addon      string
	Components []ComponentCheck
}

// 必须安装的扩展件
var neededAddons = []string{
	"kubeproxy",
	"calico",
	"coredns",
}

// 额外安装的扩展件
var extraAddonComponents = []AddonCheck{
	{
		Addon: "cluster-api",
		Components: []ComponentCheck{
			{
				Namespace: "cluster-system",
				Prefixes: []string{
					"capi-controller-manager",
					"bke-controller-manager"},
			},
		},
	},
	{
		Addon: "openfuyao-system-controller",
		Components: []ComponentCheck{
			{
				Namespace: "kube-system",
				Prefixes:  []string{"metrics-server-"},
			},
			{
				Namespace: "ingress-nginx",
				Prefixes:  []string{"ingress-nginx-controller"},
			},
			{
				Namespace: "monitoring",
				Prefixes: []string{
					"alertmanager-main-",
					"blackbox-exporter-",
					"kube-state-metrics-",
					"node-exporter-",
					"prometheus-k8s-",
					"prometheus-operator-",
				},
			},
			{
				Namespace: "openfuyao-system",
				Prefixes: []string{
					"application-management-service-",
					"console-service-",
					"console-website-",
					"local-harbor-", // 匹配所有 local-harbor- 开头的 Pod
					"marketplace-service-",
					"monitoring-service-",
					"oauth-server-",
					"oauth-webhook-",
					"plugin-management-service-",
					"user-management-operator-",
					"web-terminal-service-",
				},
			},
			{
				Namespace: "openfuyao-system-controller",
				Prefixes:  []string{"openfuyao-system-controller-"},
			},
		},
	},
}

// 全局配置：定义需要检查的组件及其命名空间和前缀
var neededComponentChecks = []ComponentCheck{
	{
		Namespace: "kube-system",
		Prefixes: []string{
			"calico-kube-controllers",
			"calico-node",
			"coredns",
			"etcd-",
			"kube-apiserver-",
			"kube-controller-manager-",
			"kube-proxy-",
			"kube-scheduler-",
		},
	},
}

func checkItemContains(checkItem string, neededAddons []string) bool {
	for _, item := range neededAddons {
		if item == checkItem {
			return true
		}
	}
	return false
}

// CheckAllComponentsHealth check all components health
func (c *Client) CheckAllComponentsHealth(cluster *bkev1beta1.BKECluster, log *zap.SugaredLogger) error {
	var errs []error
	for _, check := range neededComponentChecks {
		if err := c.processComponentCheck(check); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return kerrors.NewAggregate(errs)
	}

	addons := cluster.Spec.ClusterConfig.Addons

	for _, addon := range addons {
		if checkItemContains(addon.Name, neededAddons) {
			continue
		}
		// 判断 Addon 是否在 extraAddonComponents 中（需要校验的 Addon）
		_, needCheck := findAddonComponent(addon.Name)
		if !needCheck {
			// 2. 不在 extraAddonComponents 中 → 自动跳过（用户自定义 Addon 无需校验）
			log.Debugf("addon %q is not in extraAddonComponents, skip health check", addon.Name)
			continue
		}
		// 仅对「在 extraAddonComponents 中定义的 Addon」执行校验
		if err := c.processAddonComponentCheck(addon.Name); err != nil {
			errs = append(errs, err)
		}
	}
	return kerrors.NewAggregate(errs)
}

func (c *Client) processComponentCheck(check ComponentCheck) error {
	pods, err := c.getPods(check.Namespace)
	if err != nil {
		return fmt.Errorf("list pods in %s failed: %v", check.Namespace, err)
	}

	var errs []error
	for _, prefix := range check.Prefixes {
		if err := c.verifyComponentPods(pods, prefix, check.Namespace); err != nil {
			errs = append(errs, err)
		}
	}
	return kerrors.NewAggregate(errs)
}

func findAddonComponent(addon string) (*AddonCheck, bool) {
	for _, addonComponent := range extraAddonComponents {
		if addon == addonComponent.Addon {
			return &addonComponent, true
		}
	}
	return nil, false
}

func (c *Client) processAddonComponentCheck(addon string) error {
	addonComponent, found := findAddonComponent(addon)
	if !found {
		return fmt.Errorf("addon(%v) not in extra addons(%v)", addon, extraAddonComponents)
	}

	var errs []error
	for _, component := range addonComponent.Components {
		if err := c.processComponentCheck(component); err != nil {
			errs = append(errs, err)
		}
	}
	return kerrors.NewAggregate(errs)
}

func (c *Client) getPods(namespace string) ([]corev1.Pod, error) {
	list, err := c.ClientSet.CoreV1().Pods(namespace).List(c.Ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *Client) verifyComponentPods(pods []corev1.Pod, prefix string, namespace string) error {
	var errs []error
	matched := filterPodsWithPrefix(pods, prefix)
	if len(matched) == 0 {
		return fmt.Errorf("no pods with prefix '%s' in %s", prefix, namespace)
	}

	// if coredns pod, one coredns pod is ok should be ok.
	if prefix == "coredns" {
		for _, pod := range matched {
			if pod.Status.Phase == corev1.PodRunning {
				return kerrors.NewAggregate(errs)
			}
		}
		errs = append(errs, fmt.Errorf("pod %s/%s status: %s", matched[0].Namespace, matched[0].Name, matched[0].Status.Phase))
		return kerrors.NewAggregate(errs)
	}

	for _, pod := range matched {
		if pod.Status.Phase != corev1.PodRunning {
			errs = append(errs, fmt.Errorf("pod %s/%s status: %s", pod.Namespace, pod.Name, pod.Status.Phase))
		}
	}
	return kerrors.NewAggregate(errs)
}

func filterPodsWithPrefix(pods []corev1.Pod, prefix string) []corev1.Pod {
	filtered := make([]corev1.Pod, 0)
	for _, pod := range pods {
		if strings.HasPrefix(pod.Name, prefix) {
			filtered = append(filtered, pod)
		}
	}
	return filtered
}

func (c *Client) NodeHealthCheck(node *corev1.Node, expectVersion string, log *zap.SugaredLogger) error {
	return c.nodeHealthCheck(node, expectVersion, log, c.CheckComponentHealth)
}

// 统一的健康检查基础函数
func (c *Client) nodeHealthCheck(
	node *corev1.Node,
	expectVersion string,
	log *zap.SugaredLogger,
	componentCheckFunc func(*corev1.Node) error,
) error {
	// Step 1: 检查节点就绪状态
	if err := checkNodeReady(node); err != nil {
		return err
	}

	// Step 2: 检查节点版本
	if err := checkNodeVersion(node, expectVersion); err != nil {
		return err
	}

	// Step 3: 主节点组件检查
	if labelhelper.IsMasterNode(node) {
		if err := componentCheckFunc(node); err != nil {
			return err
		}
	}

	log.Debugf("node %q health status pass check", node.Name)
	return nil
}

// 检查节点就绪状态
func checkNodeReady(node *corev1.Node) error {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status != corev1.ConditionTrue {
			return fmt.Errorf("node %s is not ready", node.Name)
		}
	}
	return nil
}

// 检查节点版本
func checkNodeVersion(node *corev1.Node, expectVersion string) error {
	if expectVersion == "" {
		return nil
	}
	if node.Status.NodeInfo.KubeletVersion != expectVersion {
		return fmt.Errorf("node %q version %q is not match bkeCluster KubernetesVersion %q",
			node.Name, node.Status.NodeInfo.KubeletVersion, expectVersion)
	}
	return nil
}

func StaticPodName(component, nodeName string) string {
	return fmt.Sprintf("%s-%s", component, nodeName)
}

func NodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
