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
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	apierrors2 "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/controller-runtime/pkg/client"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkeaddon "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/addon"
	bkeinit "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	bkenet "gopkg.openfuyao.cn/cluster-api-provider-bke/common/utils/net"
	metricrecord "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/metrics/record"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/log"
	v "gopkg.openfuyao.cn/cluster-api-provider-bke/version"
)

const (
	keyValueSeparatorCount     = 2  // for parseNodeSelector key=value splitting
	bocOperatorTimeoutMinutes  = 30 // timeout for bocoperator addon in minutes
	bocOperatorIntervalSeconds = 30 // interval for bocoperator addon in seconds
)

type Task struct {
	Name     string
	FilePath string
	Param    map[string]interface{}
	// IgnoreError: if true, continue to apply the remaining yaml ignoring errors
	IgnoreError bool
	// Block: if true, wait for the task to complete
	Block    bool
	Timeout  time.Duration
	Interval time.Duration
	Operate  bkeaddon.AddonOperate
	recorder *AddonRecorder
}

type AddonRecorder struct {
	AddonName    string
	AddonVersion string
	AddonObjects []*AddonObject
}

type AddonObject struct {
	Name      string
	Kind      string
	NameSpace string
}

func NewAddonRecorder(addonT *bkeaddon.AddonTransfer) *AddonRecorder {
	return &AddonRecorder{
		AddonName:    addonT.Addon.Name,
		AddonVersion: addonT.Addon.Version,
		AddonObjects: []*AddonObject{},
	}
}

func (recorder *AddonRecorder) Record(obj *unstructured.Unstructured) {
	recorder.AddonObjects = append(recorder.AddonObjects, NewAddonObject(obj))
}

func NewAddonObject(obj *unstructured.Unstructured) *AddonObject {
	return &AddonObject{
		Name:      obj.GetName(),
		Kind:      obj.GetKind(),
		NameSpace: obj.GetNamespace(),
	}
}

func NewTask(name, filePath string, param map[string]interface{}) *Task {
	return &Task{
		Name:        name,
		Param:       param,
		FilePath:    filePath,
		IgnoreError: false,
		Block:       true,
	}
}

func (t *Task) SetWaiter(wait bool, timeout time.Duration, interval time.Duration) *Task {
	t.Block = wait
	t.Timeout = timeout
	t.Interval = interval
	return t
}

func (t *Task) AddRepo(repo string) *Task {
	if t.Param == nil {
		t.Param = make(map[string]interface{})
	}
	t.Param["repo"] = repo
	return t
}

func (t *Task) SetOperate(operate bkeaddon.AddonOperate) *Task {
	t.Operate = operate
	return t
}

func (t *Task) RegisAddonRecorder(recorder *AddonRecorder) *Task {
	t.recorder = recorder
	return t
}

func (c *Client) InstallAddon(bkeCluster *bkev1beta1.BKECluster, addonT *bkeaddon.AddonTransfer, addonRecorder *AddonRecorder, localClient client.Client, bkeNodes bkenode.Nodes) error {
	var err error
	defer metricrecord.AddonInstallRecord(bkeCluster, addonT.Addon.Name, addonT.Addon.Version, err)()

	addon := addonT.Addon.DeepCopy()
	bkeConfig := bkeCluster.Spec.ClusterConfig
	cfg := bkeinit.BkeConfig(*bkeConfig)

	if addon.Type == bkeaddon.ChartAddon {
		return c.installChartAddon(addon, addonT.Operate, bkeCluster.Namespace, cfg, localClient)
	}
	return c.installYamlAddon(addon, addonT, bkeCluster, cfg, addonRecorder, bkeNodes)
}

func (c *Client) installYamlAddon(addon *confv1beta1.Product, addonT *bkeaddon.AddonTransfer,
	bkeCluster *bkev1beta1.BKECluster, cfg bkeinit.BkeConfig, addonRecorder *AddonRecorder, bkeNodes bkenode.Nodes) error {
	files, err := c.getAddonYamlFiles(addon)
	if err != nil {
		return err
	}

	if addonT.Operate == bkeaddon.RemoveAddon {
		sort.Sort(sort.Reverse(sort.StringSlice(files)))
	} else {
		sort.Strings(files)
	}

	repo := cfg.ImageRepo()
	param, err := c.prepareAddonParameters(bkeCluster, addon, files, repo, bkeNodes)
	if err != nil {
		return err
	}

	applyConfig := &addonApplyConfig{
		addon:         addon,
		addonT:        addonT,
		files:         files,
		param:         param,
		repo:          repo,
		addonRecorder: addonRecorder,
	}

	return c.applyAddonFiles(applyConfig)
}

func (c *Client) getAddonYamlFiles(addon *confv1beta1.Product) ([]string, error) {
	addonDir := filepath.Join(constant.K8sManifestsDir, addon.Name, addon.Version)
	if _, err := os.Stat(addonDir); os.IsNotExist(err) {
		return nil, errors.Errorf("addon dir %q not exist,err: %v", addonDir, err)
	}

	var files []string
	err := filepath.Walk(addonDir, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".yaml") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to traverse addon dir %q", addonDir)
	}
	if len(files) == 0 {
		return nil, errors.Errorf("no yaml file found in dir %q", addonDir)
	}
	return files, nil
}

func (c *Client) prepareAddonParameters(bkeCluster *bkev1beta1.BKECluster, addon *confv1beta1.Product, files []string, repo string, bkeNodes bkenode.Nodes) (map[string]map[string]interface{}, error) {
	commonParam, err := getCommonParamFromBKECluster(bkeCluster, bkeNodes)
	if err != nil {
		return nil, err
	}

	commonParam, err = c.enhanceCommonParamForSpecialAddons(bkeCluster, addon, commonParam, bkeNodes)
	if err != nil {
		return nil, err
	}

	filesBaseNames := make([]string, len(files))
	for i, file := range files {
		filesBaseNames[i] = strings.Split(filepath.Base(file), ".")[0]
	}

	param := prepareAddonParam(addon.Param, filesBaseNames, repo)
	if err := normalizeNodeSelector(param); err != nil {
		return nil, err
	}

	for i := range files {
		fileName := filesBaseNames[i]
		param[fileName] = mergeParam(commonParam, param[fileName])
	}

	return param, nil
}

func (c *Client) enhanceCommonParamForSpecialAddons(bkeCluster *bkev1beta1.BKECluster, addon *confv1beta1.Product, commonParam map[string]interface{}, bkeNodes bkenode.Nodes) (map[string]interface{}, error) {
	if commonParam == nil {
		commonParam = make(map[string]interface{})
	}

	if addon.Name == "bocoperator" {
		bocParam, err := getBocDefaultParam(bkeCluster, addon, bkeNodes)
		if err != nil {
			return nil, err
		}

		portalClusterToken, err := c.getPortalK8sToken()
		if err != nil {
			return nil, err
		}
		bocParam["portalClusterToken"] = portalClusterToken
		commonParam = mergeParam(commonParam, bocParam)
	}

	if addon.Name == "cluster-api" && addon.Param["manage"] == "true" {
		portalClusterToken, err := c.getPortalK8sToken()
		if err != nil {
			return nil, err
		}
		commonParam["clusterToken"] = portalClusterToken
		commonParam["nodes"] = convertNodesToManageTemplateData(bkeNodes)
	}

	if addon.Name == "fabric" {
		fabricParam, err := parseFabricParam(addon)
		if err != nil {
			return nil, err
		}
		addon.Param = fabricParam
	}
	// nodelocaldns 组件需要额外的参数
	if addon.Name == "nodelocaldns" {
		commonParam["domain"] = bkeCluster.Spec.ClusterConfig.Cluster.Networking.DNSDomain
		proxymode := bkeCluster.Spec.ClusterConfig.CustomExtra["proxyMode"]
		if proxymode == "iptables" {
			commonParam["DNSserver"] = commonParam["dnsIP"]
			commonParam["clusterDNS"] = "__PILLAR__CLUSTER__DNS__"
		} else {
			if proxymode == "ipvs" {
				commonParam["DNSserver"] = ""
				commonParam["clusterDNS"] = commonParam["dnsIP"]
			}
		}
	}

	return commonParam, nil
}

type addonApplyConfig struct {
	addon         *confv1beta1.Product
	addonT        *bkeaddon.AddonTransfer
	files         []string
	param         map[string]map[string]interface{}
	repo          string
	addonRecorder *AddonRecorder
}

func (c *Client) applyAddonFiles(config *addonApplyConfig) error {
	filesBaseNames := c.extractFileBaseNames(config.files)

	for i, file := range config.files {
		fileName := filesBaseNames[i]
		task := c.createAddonTask(config, fileName, file)

		if err := c.ApplyYaml(task); err != nil {
			return c.handleApplyError(err, config.addon, file, config.addonT.Operate)
		}
	}
	return nil
}

func (c *Client) extractFileBaseNames(files []string) []string {
	filesBaseNames := make([]string, len(files))
	for i, file := range files {
		filesBaseNames[i] = strings.Split(filepath.Base(file), ".")[0]
	}
	return filesBaseNames
}

func (c *Client) createAddonTask(config *addonApplyConfig, fileName, file string) *Task {
	task := NewTask(config.addon.Name, file, config.param[fileName]).
		AddRepo(config.repo).
		SetWaiter(config.addon.Block, bkeinit.DefaultAddonTimeout, bkeinit.DefaultAddonInterval).
		SetOperate(config.addonT.Operate).
		RegisAddonRecorder(config.addonRecorder)

	if config.addonT.Operate == bkeaddon.CreateAddon && config.addon.Name == "bocoperator" {
		task.SetWaiter(config.addon.Block, bocOperatorTimeoutMinutes*time.Minute, bocOperatorIntervalSeconds*time.Second)
	}

	return task
}

func (c *Client) handleApplyError(err error, addon *confv1beta1.Product, file string, operate bkeaddon.AddonOperate) error {
	if operate == bkeaddon.RemoveAddon {
		c.Log.Warnf("(ignore)failed to remove addon %s/%s, err: %v", addon.Name, addon.Version, err)
		return nil
	}

	if apierrors2.IsNoMatchError(err) {
		c.Log.Errorf("The addon %s/%s yaml file contains resources that are not supported in the target cluster，err: %v", addon.Name, addon.Version, err)
	} else {
		c.Log.Warnf("failed to apply yaml %q, err: %v", file, err)
	}
	return err
}

func (c *Client) getPortalK8sToken() (string, error) {
	return c.NewK8sToken()
}

func getBocDefaultParam(bkeCluster *bkev1beta1.BKECluster, addon *confv1beta1.Product, bkeNodes bkenode.Nodes) (map[string]interface{}, error) {
	param := make(map[string]interface{})

	bkeConfig := bkeinit.BkeConfig(*bkeCluster.Spec.ClusterConfig)

	masterNodes := bkeNodes.Master().Decrypt()
	param["masterSshIp"] = masterNodes[0].IP
	param["masterSshPort"] = masterNodes[0].Port
	param["masterSshUser"] = masterNodes[0].Username
	param["masterSshPwd"] = masterNodes[0].Password

	param["pipeLineServerIp"] = masterNodes[0].IP
	param["pipeLineServerPort"] = masterNodes[0].Port
	param["pipeLineServerUser"] = masterNodes[0].Username
	param["pipeLineServerPwd"] = masterNodes[0].Password
	param["pipeLineServerHostname"] = masterNodes[0].Hostname

	if ip, ok := bkeConfig.CustomExtra["pipelineServer"]; ok && ip != "" {
		if !bkenet.ValidIP(ip) {
			return nil, errors.Errorf("invalid pipelineServer IP %q", ip)
		}
		pipeLineNodes := masterNodes.Filter(bkenode.FilterOptions{"IP": ip})
		if len(pipeLineNodes) == 0 || len(pipeLineNodes) != 1 {
			log.Warnf("invalid pipelineServer IP %q, use master 0", ip)
		} else {
			param["pipeLineServerIp"] = pipeLineNodes[0].IP
			param["pipeLineServerPort"] = pipeLineNodes[0].Port
			param["pipeLineServerUser"] = pipeLineNodes[0].Username
			param["pipeLineServerPwd"] = pipeLineNodes[0].Password
			param["pipeLineServerHostname"] = pipeLineNodes[0].Hostname
		}
	}

	param["isLocalDB"] = "true"
	param["bocVersion"] = "v4.0"

	masterServersJson, err := json.Marshal(masterNodes)
	if err != nil {
		return nil, err
	}
	param["masterServers"] = string(masterServersJson)

	param["dbHost"] = bkeCluster.Spec.ControlPlaneEndpoint.Host

	tmp := make(map[string]interface{})
	for k, v := range addon.Param {
		tmp[k] = v
	}
	return mergeParam(param, tmp), nil
}

func getCommonParamFromBKECluster(bkeCluster *bkev1beta1.BKECluster, bkeNodes bkenode.Nodes) (map[string]interface{}, error) {
	param := initializeDefaultParams()

	if bkeCluster == nil {
		return param, nil
	}

	if bkeCluster.Spec.ClusterConfig == nil {
		return param, nil
	}

	bkeConfig := bkeinit.BkeConfig(*bkeCluster.Spec.ClusterConfig)

	// 设置镜像仓库参数
	setImageRepoParams(&param, bkeConfig)

	// 设置HTTP仓库参数
	setHTTPRepoParams(&param, bkeConfig)

	// 设置NTP服务器参数
	setNTPServer(&param, bkeConfig)

	// 设置Agent健康监听端口参数
	setAgentHealthPort(&param, bkeConfig)

	// 设置NFS参数
	setNFSParams(&param, bkeCluster)

	// 设置命名空间
	param["namespace"] = bkeCluster.Namespace

	// 设置网络参数
	setNetworkParams(&param, bkeConfig, bkeCluster)

	// 设置etcd端点
	// Note: bkeNodes is now passed as parameter instead of using cluster.GetNodesData
	setEtcdEndpoints(&param, bkeNodes)

	// 设置节点副本数
	setNodeReplicas(&param, bkeNodes)

	// 设置附加组件特定参数
	setAddonParams(&param, bkeConfig)

	// 设置DNS IP
	if err := setDNSIP(&param); err != nil {
		return nil, err
	}

	// 设置etcd IP列表
	setEtcdIPs(&param, bkeNodes)

	// 设置Kubelet数据根目录
	setKubeletDataRoot(&param, bkeConfig)

	// 设置Docker数据根目录
	setDockerDataRoot(&param, bkeConfig)

	// 设置Kubernetes版本
	setK8sVersion(&param, bkeConfig)

	return param, nil
}

// initializeDefaultParams 初始化默认参数
func initializeDefaultParams() map[string]interface{} {
	param := make(map[string]interface{})
	param["replicas"] = 1
	param["kubeConfigDir"] = "/etc/kubernetes"
	param["ipModVersion"] = "ipv4"
	param["apiServerSrcPort"] = bkeinit.DefaultAPIBindPort
	param["podSubnet"] = bkeinit.DefaultPodSubnet
	param["serviceSubnet"] = bkeinit.DefaultServicesSubnet
	param["dnsIP"] = bkeinit.DefaultClusterDNSIP
	param["dnsDomain"] = bkeinit.DefaultServiceDNSDomain
	param["ingressReplicas"] = 1
	param["etcdEndpoints"] = ""
	param["version"] = v.Version
	param["kubeletDataRoot"] = bkeinit.DefaultKubeletRootDir
	param["dockerDataRoot"] = bkeinit.DefaultCRIDockerDataRootDir
	param["k8sVersion"] = bkeinit.DefaultKubernetesVersion
	param["masterReplicas"] = "1"
	param["workerReplicas"] = "1"
	param["namespace"] = "cluster-system"
	param["clusterNetworkMode"] = ""
	param["product"] = "boc"
	return param
}

// setImageRepoParams 设置镜像仓库参数
func setImageRepoParams(param *map[string]interface{}, bkeConfig bkeinit.BkeConfig) {
	(*param)["imageRepo"] = fmt.Sprintf("%s:%s", bkeConfig.Cluster.ImageRepo.Domain, bkeConfig.Cluster.ImageRepo.Port)
	(*param)["imageRepoDomain"] = bkeConfig.Cluster.ImageRepo.Domain
	(*param)["imageRepoPort"] = bkeConfig.Cluster.ImageRepo.Port
	(*param)["imageRepoIp"] = bkeConfig.Cluster.ImageRepo.Ip
	(*param)["imageRepoPrefix"] = bkeConfig.Cluster.ImageRepo.Prefix
}

// setHTTPRepoParams 设置HTTP仓库参数
func setHTTPRepoParams(param *map[string]interface{}, bkeConfig bkeinit.BkeConfig) {
	(*param)["httpRepo"] = bkeConfig.YumRepo()
	(*param)["httpRepoDomain"] = bkeConfig.Cluster.HTTPRepo.Domain
	(*param)["httpRepoPort"] = bkeConfig.Cluster.HTTPRepo.Port
	(*param)["httpRepoIp"] = bkeConfig.Cluster.HTTPRepo.Ip
	(*param)["httpRepoPrefix"] = bkeConfig.Cluster.HTTPRepo.Prefix
}

// setNTPServer 设置NTP服务器参数
func setNTPServer(param *map[string]interface{}, bkeConfig bkeinit.BkeConfig) {
	(*param)["ntpServer"] = bkeConfig.Cluster.NTPServer
}

// setAgentHealthPort 设置Agent健康端口参数
func setAgentHealthPort(param *map[string]interface{}, bkeConfig bkeinit.BkeConfig) {
	(*param)["agentHealthPort"] = bkeConfig.Cluster.AgentHealthPort
}

// setNFSParams 设置NFS参数
func setNFSParams(param *map[string]interface{}, bkeCluster *bkev1beta1.BKECluster) {
	(*param)["nfsServer"] = bkeCluster.Spec.ClusterConfig.CustomExtra["nfsServer"]
	(*param)["nfsRootDir"] = bkeCluster.Spec.ClusterConfig.CustomExtra["nfsRootDir"]
	(*param)["nfsVersion"] = bkeCluster.Spec.ClusterConfig.CustomExtra["nfsVersion"]
}

// setNetworkParams 设置网络参数
func setNetworkParams(param *map[string]interface{}, bkeConfig bkeinit.BkeConfig, bkeCluster *bkev1beta1.BKECluster) {
	if bkeConfig.Cluster.Networking.PodSubnet != "" {
		(*param)["podSubnet"] = bkeConfig.Cluster.Networking.PodSubnet
	}
	if bkeConfig.Cluster.Networking.ServiceSubnet != "" {
		(*param)["serviceSubnet"] = bkeConfig.Cluster.Networking.ServiceSubnet
	}
	if bkeConfig.Cluster.Networking.DNSDomain != "" {
		(*param)["dnsDomain"] = bkeConfig.Cluster.Networking.DNSDomain
	}
	if bkeCluster.Spec.ControlPlaneEndpoint.Host != "" {
		(*param)["apiServerSrcHost"] = bkeCluster.Spec.ControlPlaneEndpoint.Host
	}
	if bkeCluster.Spec.ControlPlaneEndpoint.Port != 0 {
		(*param)["apiServerSrcPort"] = bkeCluster.Spec.ControlPlaneEndpoint.Port
	}
}

// setEtcdEndpoints 设置etcd端点
func setEtcdEndpoints(param *map[string]interface{}, bkeNodes bkenode.Nodes) {
	var etcdEndpointsLi []string
	for _, node := range bkeNodes.Etcd() {
		tmp := fmt.Sprintf("https://%s:2379", node.IP)
		etcdEndpointsLi = append(etcdEndpointsLi, tmp)
	}
	(*param)["etcdEndpoints"] = strings.Join(etcdEndpointsLi, ",")
}

// setNodeReplicas 设置节点副本数
func setNodeReplicas(param *map[string]interface{}, bkeNodes bkenode.Nodes) {
	(*param)["masterReplicas"] = strconv.Itoa(bkeNodes.Master().Length())
	(*param)["workerReplicas"] = strconv.Itoa(bkeNodes.Worker().Length())
}

// setAddonParams 设置附加组件参数
func setAddonParams(param *map[string]interface{}, bkeConfig bkeinit.BkeConfig) {
	// for beyondelb replicas
	for _, v := range bkeConfig.Addons {
		name := strings.ToLower(v.Name)
		if name == "beyondelb" {
			lbNodes := v.Param["lbNodes"]
			(*param)["ingressReplicas"] = len(strings.Split(lbNodes, ","))
		}
		if name == "calico" {
			(*param)["clusterNetworkMode"] = "calico"
		}
		if name == "fabric" {
			(*param)["clusterNetworkMode"] = "fabric"
		}
	}
}

// setDNSIP 设置DNS IP
func setDNSIP(param *map[string]interface{}) error {
	dnsIP, err := bkeinit.GetClusterDNSIP((*param)["serviceSubnet"].(string))
	if err != nil {
		return errors.New("failed to get cluster dns ip")
	}
	(*param)["dnsIP"] = dnsIP
	return nil
}

// setEtcdIPs 设置etcd IP列表
func setEtcdIPs(param *map[string]interface{}, bkeNodes bkenode.Nodes) {
	etcdNodes := bkeNodes.Etcd()
	var etcdEndpoints []string
	for _, node := range etcdNodes {
		etcdEndpoints = append(etcdEndpoints, node.IP)
	}
	(*param)["etcdIps"] = strings.Join(etcdEndpoints, ",")
}

// setKubeletDataRoot 设置Kubelet数据根目录
func setKubeletDataRoot(param *map[string]interface{}, bkeConfig bkeinit.BkeConfig) {
	if bkeConfig.Cluster.Kubelet != nil && bkeConfig.Cluster.Kubelet.ExtraVolumes != nil {
		for _, volume := range bkeConfig.Cluster.Kubelet.ExtraVolumes {
			if volume.Name == "kubelet-root-dir" {
				(*param)["kubeletDataRoot"] = volume.HostPath
			}
		}
	}
}

// setDockerDataRoot 设置Docker数据根目录
func setDockerDataRoot(param *map[string]interface{}, bkeConfig bkeinit.BkeConfig) {
	if bkeConfig.Cluster.ContainerRuntime.CRI == bkeinit.CRIDocker {
		if v, ok := bkeConfig.Cluster.ContainerRuntime.Param["data-root"]; ok {
			(*param)["dockerDataRoot"] = v
		}
	}
}

// setK8sVersion 设置Kubernetes版本
func setK8sVersion(param *map[string]interface{}, bkeConfig bkeinit.BkeConfig) {
	if bkeConfig.Cluster.KubernetesVersion != "" {
		(*param)["k8sVersion"] = bkeConfig.Cluster.KubernetesVersion
	}
}

// convertNodesToManageTemplateData converts bkeNodes to a slice of maps
func convertNodesToManageTemplateData(bkeNodes bkenode.Nodes) []map[string]interface{} {
	nodesData := make([]map[string]interface{}, 0, len(bkeNodes))
	for _, node := range bkeNodes {
		nodeData := map[string]interface{}{
			"hostname": node.Hostname,
			"ip":       node.IP,
			"username": node.Username,
			"password": node.Password,
			"port":     node.Port,
			"role":     node.Role,
		}
		nodesData = append(nodesData, nodeData)
	}
	return nodesData
}

// normalizeNodeSelector normalize nodeSelector param from string to map[string]string
func normalizeNodeSelector(param map[string]map[string]interface{}) error {
	for file, kv := range param {
		raw, ok := kv["nodeSelector"]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case map[string]string, map[string]interface{}:
			continue
		case string:
			selector, err := parseNodeSelector(v)
			if err != nil {
				return errors.Wrapf(err, "%s.nodeSelector", file)
			}
			kv["nodeSelector"] = selector
		default:
			return errors.Errorf("%s.nodeSelector unsupported type %T", file, v)
		}
	}
	return nil
}

// parseNodeSelector parse nodeSelector param from string to map[string]string
func parseNodeSelector(raw string) (map[string]string, error) {
	out := make(map[string]string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out, nil
	}
	if strings.HasPrefix(raw, "{") {
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", keyValueSeparatorCount)
		key := strings.TrimSpace(kv[0])
		if key == "" {
			return nil, errors.Errorf("invalid nodeSelector entry %q", pair)
		}
		val := ""
		if len(kv) == keyValueSeparatorCount {
			val = strings.TrimSpace(kv[1])
		}
		out[key] = val
	}
	return out, nil
}

// parseFabricParam parse fabric param excludeIps
func parseFabricParam(addon *confv1beta1.Product) (map[string]string, error) {
	fabricParam := addon.Param

	if !hasExcludeIpsParam(fabricParam) {
		return fabricParam, nil
	}

	excludeIpsStr := fabricParam["excludeIps"]
	excludeIps, err := processExcludeIps(excludeIpsStr)
	if err != nil {
		return nil, err
	}

	fabricParam["excludeIps"] = excludeIps
	return fabricParam, nil
}

// hasExcludeIpsParam 检查是否包含excludeIps参数
func hasExcludeIpsParam(fabricParam map[string]string) bool {
	p, ok := fabricParam["excludeIps"]
	return ok && p != ""
}

// processExcludeIps 处理excludeIps参数
func processExcludeIps(excludeIpsStr string) (string, error) {
	items := strings.Split(excludeIpsStr, ",")
	var excludeIps []string

	for _, item := range items {
		validatedIP, err := processItem(item)
		if err != nil {
			return "", err
		}
		if validatedIP != "" {
			excludeIps = append(excludeIps, validatedIP)
		}
	}

	return strings.Join(excludeIps, ","), nil
}

// processItem 处理单个项目
func processItem(item string) (string, error) {
	if strings.Contains(item, "-") {
		ips, err := parseFabricExcludeIPRange(item)
		if err != nil {
			return "", err
		}
		return strings.Join(ips, ","), nil
	}

	ip := net.ParseIP(item)
	if ip != nil {
		return ip.String(), nil
	}

	return "", errors.Errorf("fabric param excludeIps contain a invalid ip %s", item)
}

func mergeParam(src, dst map[string]interface{}) map[string]interface{} {
	// Check if dst is nil, if so create a new map
	if dst == nil {
		dst = make(map[string]interface{})
	}

	// Check if src is nil, if so return dst as is
	if src == nil {
		return dst
	}

	for k, v := range src {
		if _, ok := dst[k]; !ok {
			dst[k] = v
		}
	}
	return dst
}

func prepareAddonParam(addonParam map[string]string, filesBaseNames []string, repo string) map[string]map[string]interface{} {
	param := make(map[string]map[string]interface{})
	for _, fileName := range filesBaseNames {
		param[fileName] = make(map[string]interface{})
	}
	for k, v := range addonParam {
		arg := strings.Split(k, ".")
		if len(arg) == 1 {
			for _, fileName := range filesBaseNames {
				param[fileName][k] = v
			}
		} else {
			if param[arg[0]] == nil {
				param[arg[0]] = make(map[string]interface{})
			}
			param[arg[0]][arg[1]] = v
		}
	}
	// add default param repo
	for _, v := range param {
		if _, ok := v["repo"]; !ok {
			v["repo"] = repo
		}
	}
	return param
}

func parseFabricExcludeIPRange(rangeStr string) ([]string, error) {
	ipRange := strings.Split(rangeStr, "-")

	if len(ipRange) != keyValueSeparatorCount || ipRange == nil {
		return nil, errors.Errorf("fabric param excludeIps contain a invalid ip range %s", rangeStr)
	}
	start := net.ParseIP(ipRange[0]).To4()
	end := net.ParseIP(ipRange[1]).To4()
	if start == nil || end == nil {
		return nil, errors.Errorf("fabric param excludeIps contain a invalid ip range %s", rangeStr)
	}

	ipLe := func(src, dst net.IP) bool {
		if src.Equal(dst) {
			return false
		}
		for i, value := range src {
			if value > dst[i] {
				return true
			}
		}
		return false
	}

	if ipLe(start, end) {
		tmp := start
		start = end
		end = tmp
	}

	var ips []string
	ips = append(ips, start.String())
	for ip := start; !ip.Equal(end); {
		for j := len(ip) - 1; j >= 0; j-- {
			ip[j]++
			if ip[j] > 0 {
				break
			}
		}
		ips = append(ips, ip.String())
	}
	if !utils.ContainsString(ips, end.String()) {
		ips = append(ips, end.String())
	}
	return ips, nil
}
