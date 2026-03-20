/*
 * Copyright (c) 2025 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 */

package mfutil

import (
	"bytes"
	"embed"
	_ "embed"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/pkg/errors"
	kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
	"k8s.io/kubernetes/cmd/kubeadm/app/util/etcd"

	bkeinit "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	bkenet "gopkg.openfuyao.cn/cluster-api-provider-bke/common/utils/net"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/common/versionutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/clientutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/cluster"
	bkeetcd "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/etcd"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/pkiutil"
)

//
//go:embed  tmpl/*
var f embed.FS

const (
	keepalivedBaseTmpl = `
{{ range $instance := .instances }}
{{ $instance }}
{{ end }}
`
	// CertChainFileName defines certificate ca and chain name
	CertCAAndChainFileName = "ca-chain.crt"
)

func generateRandomString(n int) string {
	result := make([]byte, n)
	rand.Seed(time.Now().UnixNano())
	for i := range result {
		result[i] = randomStringLetters[rand.Intn(len(randomStringLetters))]
	}
	return string(result)
}

type TemplateRenderOptions struct {
	Name     string            // 模板名称
	Template []byte            // 模板内容
	Data     interface{}       // 模板数据
	FilePath string            // 输出文件路径
	FuncMap  *template.FuncMap // 模板函数映射
	FileMode os.FileMode       // 文件权限
}

func (o *TemplateRenderOptions) Validate() error {
	if o.Name == "" {
		return fmt.Errorf("template name is required")
	}
	if len(o.Template) == 0 {
		return fmt.Errorf("template content is empty")
	}
	if o.FilePath == "" {
		return fmt.Errorf("output file path is required")
	}
	return nil
}

// renderTemplateAndStore renders a Go template with the given configuration and stores it to a file
func renderTemplateAndStore(options *TemplateRenderOptions) error {
	if err := options.Validate(); err != nil {
		return fmt.Errorf("invalid template render options: %w", err)
	}

	var t *template.Template
	var err error

	if options.FuncMap != nil {
		t, err = template.New(options.Name).Funcs(*options.FuncMap).Parse(string(options.Template))
		if err != nil {
			return fmt.Errorf("failed to parse template with funcMap: %w", err)
		}
	} else {
		t, err = template.New(options.Name).Parse(string(options.Template))
		if err != nil {
			return fmt.Errorf("failed to parse template: %w", err)
		}
	}

	if t == nil {
		return errors.New("template is nil after parsing")
	}

	writer, err := os.OpenFile(options.FilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, options.FileMode)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", options.FilePath, err)
	}
	defer writer.Close()

	if err := t.Execute(writer, options.Data); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	return nil
}

// renderK8sAndStore renders a Kubernetes component template and stores it
func renderK8sAndStore(c *BKEComponent, tmpl []byte, cfg interface{}, funcMap *template.FuncMap) error {
	options := &TemplateRenderOptions{
		Name:     c.Name,
		Template: tmpl,
		Data:     cfg,
		FilePath: pathForManifest(c),
		FuncMap:  funcMap,
		FileMode: utils.RwRR,
	}
	return renderTemplateAndStore(options)
}

// renderHAAndStore renders a HA component template and stores it
func renderHAAndStore(c *BKEHAComponent, tmpl []byte, cfg map[string]interface{}, filePath string, funcMap *template.FuncMap) error {
	options := &TemplateRenderOptions{
		Name:     c.Name,
		Template: tmpl,
		Data:     cfg,
		FilePath: filePath,
		FuncMap:  funcMap,
		FileMode: utils.RwRR,
	}
	return renderTemplateAndStore(options)
}

func renderAPIServer(c *BKEComponent, cfg *BootScope) error {
	tmpl, err := fs.ReadFile(f, "tmpl/k8s/kube-apiserver.yaml.tmpl")
	if err != nil {
		return err
	}
	file, err := fs.ReadFile(f, "tmpl/k8s/audit-policy.yaml")
	if err != nil {
		return err
	}
	// write the audit policy file,default to /etc/kubernetes/audit-policy.yaml
	if err := os.WriteFile(GetAuditPolicyFilePath(), file, utils.RwRR); err != nil {
		return err
	}
	return renderK8sAndStore(c, tmpl, cfg, mergeFuncMap(GlobalFuncMap(), apiServerFuncMap()))
}

func renderEtcd(c *BKEComponent, cfg *BootScope) error {
	if cfg == nil {
		return errors.New("config is nil")
	}

	// Step 1: Handle etcd cluster membership
	if err := handleEtcdMembership(cfg); err != nil {
		return err
	}

	// Step 2: Render etcd yaml
	return renderEtcdYaml(c, cfg)
}

// handleEtcdMembership handles etcd cluster initialization or node addition
func handleEtcdMembership(cfg *BootScope) error {
	etcdPeerAddress := etcd.GetPeerURL(&kubeadmapi.APIEndpoint{AdvertiseAddress: cfg.HostIP})

	// Safely get Init flag
	initValue, initExists := cfg.Extra["Init"]
	isInit := true // Default to initialization mode
	if initExists {
		initBool, ok := initValue.(bool)
		if !ok {
			return fmt.Errorf("init flag is not boolean, got: %T", initValue)
		}
		isInit = initBool
	}

	if isInit {
		// New cluster initialization
		cfg.Extra["EtcdInitialCluster"] = []etcd.Member{{Name: cfg.HostName, PeerURL: etcdPeerAddress}}
		return nil
	}

	// Add to existing cluster
	return addNodeToExistingEtcdCluster(cfg, etcdPeerAddress)
}

// addNodeToExistingEtcdCluster adds node to existing etcd cluster
func addNodeToExistingEtcdCluster(cfg *BootScope, etcdPeerAddress string) error {
	// Safely get mccs
	mccsValue, mccsExists := cfg.Extra["mccs"]
	if !mccsExists {
		return errors.New("mccs not found in config")
	}

	mccs, ok := mccsValue.([]string)
	if !ok {
		return fmt.Errorf("mccs is not []string, got: %T", mccsValue)
	}

	// Create etcd client
	client, err := clientutil.ClientSetFromManagerClusterSecret(mccs...)
	if err != nil {
		return errors.Wrapf(err, "Failed to get kubernetes client")
	}

	etcdClient, err := etcd.NewFromCluster(client, cfg.BkeConfig.Cluster.CertificatesDir)
	if err != nil {
		return errors.Wrapf(err, "Failed to create etcd client")
	}
	if etcdClient == nil {
		return errors.New("etcd client is nil")
	}

	// Handle etcd membership
	initialCluster, err := etcdClient.ListMembers()
	if err != nil {
		return errors.Wrapf(err, "Failed to list etcd members")
	}

	// Check if member already exists
	log.Infof("checking if the etcd member already exists: %s", etcdPeerAddress)
	memberExists := false
	for i := range initialCluster {
		if initialCluster[i].PeerURL == etcdPeerAddress {
			memberExists = true
			if len(initialCluster[i].Name) == 0 {
				initialCluster[i].Name = cfg.HostName
			}
			break
		}
	}

	if memberExists {
		log.Infof("etcd member already exists: %q", etcdPeerAddress)
		cfg.Extra["EtcdInitialCluster"] = initialCluster
	} else {
		log.Infof("adding etcd member: %s", etcdPeerAddress)
		newMembers, err := etcdClient.AddMember(cfg.HostName, etcdPeerAddress)
		if err != nil {
			return errors.Wrapf(err, "Failed to add etcd member")
		}
		cfg.Extra["EtcdInitialCluster"] = newMembers
		log.Infof("Updated etcd member list: %v", newMembers)
	}

	return nil
}

// renderEtcdYaml renders etcd yaml manifest
func renderEtcdYaml(c *BKEComponent, cfg *BootScope) error {
	tmpl, err := fs.ReadFile(f, "tmpl/k8s/etcd.yaml.tmpl")
	if err != nil {
		return errors.Wrapf(err, "Failed to read etcd template")
	}

	cfg.Extra["EtcdAdvertiseUrls"] = etcd.GetClientURLByIP(cfg.HostIP)

	funcMap := mergeFuncMap(GlobalFuncMap(), etcdFuncMap())
	if err = renderK8sAndStore(c, tmpl, cfg, funcMap); err != nil {
		return errors.Wrapf(err, "Failed to render etcd yaml")
	}

	return nil
}

func renderController(c *BKEComponent, cfg *BootScope) error {
	tmpl, err := fs.ReadFile(f, "tmpl/k8s/kube-controller-manager.yaml.tmpl")
	if err != nil {
		return err
	}
	return renderK8sAndStore(c, tmpl, cfg, mergeFuncMap(GlobalFuncMap(), controllerFuncMap()))
}

func renderScheduler(c *BKEComponent, cfg *BootScope) error {
	// todo 还需要处理一个文件 gpu-admission.config
	if v, ok := cfg.Extra["gpuEnable"]; ok && v.(string) == "true" {
		policyFile, err := fs.ReadFile(f, "tmpl/k8s/scheduler-policy-config.json")
		if err != nil {
			return err
		}
		if err = os.WriteFile(GetSchedulerPolicyFilePath(), policyFile, utils.RwxRxRx); err != nil {
			return err
		}
		admissionConfig, err := fs.ReadFile(f, "tmpl/k8s/gpu-admission.config")
		if err != nil {
			return err
		}
		if err = os.WriteFile(GetSchedulerAdmissionConfigFilePath(), admissionConfig, utils.RwxRxRx); err != nil {
			return err
		}
	}

	tmpl, err := fs.ReadFile(f, "tmpl/k8s/kube-scheduler.yaml.tmpl")
	if err != nil {
		return err
	}
	return renderK8sAndStore(c, tmpl, cfg, mergeFuncMap(GlobalFuncMap(), schedulerFuncMap()))
}

// HA component yaml render func
func renderHAProxy(c *BKEHAComponent, cfg map[string]interface{}) error {
	v, ok := cfg["haproxyConfigDir"]
	if !ok {
		return errors.New("haproxyConfigDir not found in config")
	}

	c.ConfPath, ok = v.(string)
	if !ok {
		return fmt.Errorf("haproxyConfigDir is not a string: %v", v)
	}

	// step1 render conf file
	tmpl, err := fs.ReadFile(f, "tmpl/haproxy/haproxy.cfg.tmpl")
	if err != nil {
		return err
	}
	if err := renderHAAndStore(c, tmpl, cfg, pathForHAManifestConf(c), nil); err != nil {
		return err
	}
	// step2 render yaml file
	tmpl, err = fs.ReadFile(f, "tmpl/haproxy/haproxy.yaml.tmpl")
	if err != nil {
		return err
	}

	if err := renderHAAndStore(c, tmpl, cfg, pathForHAManifest(c), utilFuncMap()); err != nil {
		return err
	}
	return nil
}

func renderKeepalived(c *BKEHAComponent, cfg map[string]interface{}) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}

	if err := setupConfigPath(c, cfg); err != nil {
		return err
	}

	isMasterHa, err := getIsMasterHA(cfg)
	if err != nil {
		return err
	}

	// Step 1: Render check script
	if err = renderCheckScript(c, cfg, isMasterHa); err != nil {
		return err
	}

	// Step 2: Render instance
	if err = renderKeepalivedInstance(c, cfg, isMasterHa); err != nil {
		return err
	}

	// Step 3: Get base template
	baseTmpl, err := getBaseTemplate(c, cfg, isMasterHa)
	if err != nil {
		return err
	}

	// Step 4: Render conf file
	if err = renderConfigFile(c, cfg, baseTmpl); err != nil {
		return err
	}

	// Step 5: Render yaml file
	return renderYamlFile(c, cfg)
}

// validateConfig validates the input configuration
func validateConfig(cfg map[string]interface{}) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	return nil
}

// setupConfigPath sets up the configuration path from cfg
func setupConfigPath(c *BKEHAComponent, cfg map[string]interface{}) error {
	if v, ok := cfg["keepAlivedConfigDir"]; ok {
		configDir, ok := v.(string)
		if !ok {
			return fmt.Errorf("keepAlivedConfigDir is not a string: %v", v)
		}
		c.ConfPath = configDir
	}
	return nil
}

// getIsMasterHA safely gets the isMasterHa value from config
func getIsMasterHA(cfg map[string]interface{}) (bool, error) {
	v, ok := cfg["isMasterHa"]
	if !ok {
		return false, errors.New("isMasterHa not found in config")
	}

	isMasterHa, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("isMasterHa is not a boolean: %v", v)
	}
	return isMasterHa, nil
}

// renderCheckScript renders the check script based on HA type
func renderCheckScript(c *BKEHAComponent, cfg map[string]interface{}, isMasterHa bool) error {
	var checkTmpl []byte
	var scriptName string
	var templatePath string

	if isMasterHa {
		templatePath = tmplCheckMaster
		scriptName = scriptCheckMaster
	} else {
		templatePath = tmplCheckIngress
		scriptName = scriptCheckIngress
	}

	checkTmpl, err := fs.ReadFile(f, templatePath)
	if err != nil {
		return fmt.Errorf("failed to read check script template %s: %w", templatePath, err)
	}

	log.Infof(logRenderScript, scriptName)
	return renderHAAndStore(c, checkTmpl, cfg, pathForHAManifestScript(c, scriptName), nil)
}

// renderKeepalivedInstance renders the keepalived instance configuration
func renderKeepalivedInstance(c *BKEHAComponent, cfg map[string]interface{}, isMasterHa bool) error {
	if cfg == nil {
		return fmt.Errorf("config map is nil")
	}
	cfg["instances"] = []string{}

	var templateName, logMessage string
	if isMasterHa {
		templateName = "keepalived.master.conf.tmpl"
		logMessage = logRenderMasterVIP
	} else {
		templateName = "keepalived.ingress.conf.tmpl"
		logMessage = logRenderIngressVIP
	}

	log.Infof(logMessage)

	t, err := template.New(templateName).Funcs(*keepalivedConfFuncMap()).ParseFS(f, "tmpl/keepalived/"+templateName)
	if err != nil || t == nil {
		return fmt.Errorf("failed to parse template %s: %w", templateName, err)
	}

	content := bytes.NewBuffer(nil)
	if err = t.Execute(content, cfg); err != nil {
		return fmt.Errorf("failed to execute template %s: %w", templateName, err)
	}

	// Safely append to instances
	instances, ok := cfg["instances"].([]string)
	if !ok {
		instances = []string{}
	}
	cfg["instances"] = append(instances, content.String())

	return nil
}

// getBaseTemplate gets or creates the base template
func getBaseTemplate(c *BKEHAComponent, cfg map[string]interface{}, isMasterHa bool) (*template.Template, error) {
	confPath := pathForHAManifestConf(c)

	if utils.Exists(confPath) {
		return getExistingBaseTemplate(confPath, isMasterHa)
	}

	return createNewBaseTemplate()
}

// getExistingBaseTemplate reads and modifies existing template
func getExistingBaseTemplate(confPath string, isMasterHa bool) (*template.Template, error) {
	file, err := os.OpenFile(confPath, os.O_RDWR|os.O_APPEND, utils.RwRR)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat config file: %w", err)
	}

	buffer := make([]byte, stat.Size())
	if _, err = file.Read(buffer); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Remove old instance based on HA type
	if isMasterHa {
		buffer = regexp.MustCompile(MasterKeepalivedInstanceReg).ReplaceAll(buffer, []byte(""))
	} else {
		buffer = regexp.MustCompile(IngressKeepalivedInstanceReg).ReplaceAll(buffer, []byte(""))
	}

	// Append base template
	buffer = append(buffer, []byte(keepalivedBaseTmpl)...)

	return template.New("keepalived.conf").Funcs(*keepalivedConfFuncMap()).Parse(string(buffer))
}

// createNewBaseTemplate creates a new base template from file
func createNewBaseTemplate() (*template.Template, error) {
	baseTmpl, err := template.New("keepalived.base.conf.tmpl").Funcs(*keepalivedConfFuncMap()).ParseFS(f, tmplKeepalivedBase)
	if err != nil {
		return nil, fmt.Errorf("failed to parse base template: %w", err)
	}

	if baseTmpl == nil {
		return nil, errors.New("template is nil")
	}

	return baseTmpl, nil
}

// renderConfigFile renders the main configuration file
func renderConfigFile(c *BKEHAComponent, cfg map[string]interface{}, baseTmpl *template.Template) error {
	log.Infof(logRenderConfFile)

	confPath := pathForHAManifestConf(c)
	writer, err := os.OpenFile(confPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, utils.RwRR)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer writer.Close()

	if err = baseTmpl.Execute(writer, cfg); err != nil {
		return fmt.Errorf("failed to execute base template: %w", err)
	}

	return nil
}

// renderYamlFile renders the YAML manifest file
func renderYamlFile(c *BKEHAComponent, cfg map[string]interface{}) error {
	tmpl, err := fs.ReadFile(f, tmplKeepalivedYaml)
	if err != nil {
		return fmt.Errorf("failed to read YAML template: %w", err)
	}

	funcMap := mergeFuncMap(keepalivedConfFuncMap(), utilFuncMap())
	return renderHAAndStore(c, tmpl, cfg, pathForHAManifest(c), funcMap)
}

func keepalivedConfFuncMap() *template.FuncMap {
	return &template.FuncMap{
		"randomString": generateRandomString,
		"computeWeight": func(nodes []HANode) string {
			return strconv.Itoa(weightMultiplier * len(nodes))
		},
		"isMaster": KeepalivedInstanceIsMaster,
		"priority": func(nodes []HANode) string {
			ips, err := bkenet.GetAllInterfaceIP()
			if err != nil {
				return strconv.Itoa(defaultPriority)
			}
			for i, n := range nodes {
				// 跳过master
				if i == 0 {
					continue
				}
				for _, ip := range ips {
					if strings.Contains(ip, n.IP) {
						return strconv.Itoa(defaultPriority - priorityDecrementStep*i)
					}
				}
			}
			return strconv.Itoa(defaultPriority)
		},
	}
}

func KeepalivedInstanceIsMaster(nodes []HANode) bool {
	ips, err := bkenet.GetAllInterfaceIP()
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if strings.Contains(ip, nodes[0].IP) {
			return true
		}
	}
	return false
}

func GlobalFuncMap() *template.FuncMap {
	base := &template.FuncMap{
		"imageRepo": func(cfg *BootScope) string {
			bkeCfg := bkeinit.BkeConfig(*cfg.BkeConfig)
			return bkeCfg.ImageFuyaoRepo()
		},
	}
	return mergeFuncMap(base, versionutil.K8sVersionFuncMap())
}

func utilFuncMap() *template.FuncMap {
	return &template.FuncMap{
		"randomString": generateRandomString,
	}
}

const DefaultAPIServerAuthorizationMode = "Node,RBAC"

func apiServerFuncMap() *template.FuncMap {
	return &template.FuncMap{
		"advertiseAddress": func(cfg *BootScope) string {
			if cfg.CurrentNode.APIServer != nil && cfg.CurrentNode.APIServer.Host != "" {
				return cfg.CurrentNode.APIServer.Host
			}
			return cfg.CurrentNode.IP
		},
		"etcdServers": func(cfg *BootScope) string {
			nodesData, err := cluster.GetNodesData(cfg.ClusterNamespace, cfg.ClusterName)
			if err != nil {
				return etcd.GetClientURLByIP(cfg.HostIP)
			}
			bkeNodes := bkenode.Nodes(nodesData)
			etcdNodes := bkeNodes.Etcd()
			if etcdNodes.Length() == 0 {
				return etcd.GetClientURLByIP(cfg.HostIP)
			}
			etcdEndpoints := make([]string, 0, len(etcdNodes))
			for _, n := range etcdNodes {
				etcdEndpoints = append(etcdEndpoints, etcd.GetClientURLByIP(n.IP))
			}
			return strings.Join(etcdEndpoints, ",")
		},
		"apiServerPort": func(cfg *BootScope) int32 {
			if cfg.CurrentNode.APIServer != nil && cfg.CurrentNode.APIServer.Port != 0 {
				return cfg.CurrentNode.APIServer.Port
			}
			return cfg.BkeConfig.Cluster.APIServer.Port
		},
		"imageInfo": func(cfg *BootScope) string {
			k8sVersion := strings.TrimPrefix(cfg.BkeConfig.Cluster.KubernetesVersion, "v") // 去掉前缀字符 v
			return fmt.Sprintf("%s:%s", bkeinit.DefaultAPIServerImageName, k8sVersion)
		},
		"clientCAFile": func(cfg *BootScope) string {
			caChainPath := fmt.Sprintf("%s/%s", cfg.BkeConfig.Cluster.CertificatesDir, CertCAAndChainFileName)
			if utils.Exists(caChainPath) {
				return caChainPath
			}
			return fmt.Sprintf("%s/%s", cfg.BkeConfig.Cluster.CertificatesDir, pkiutil.CACertName)
		},
		"extraArgs": func(cfg *BootScope) []string {
			if cfg.CurrentNode.APIServer != nil && cfg.CurrentNode.APIServer.ExtraArgs != nil {
				if _, ok := cfg.CurrentNode.APIServer.ExtraArgs["authorization-mode"]; !ok {
					cfg.CurrentNode.APIServer.ExtraArgs["authorization-mode"] = DefaultAPIServerAuthorizationMode
				}
				return getExtraArgs(cfg.CurrentNode.APIServer.ExtraArgs)
			}
			if _, ok := cfg.BkeConfig.Cluster.APIServer.ExtraArgs["authorization-mode"]; !ok {
				cfg.BkeConfig.Cluster.APIServer.ExtraArgs["authorization-mode"] = DefaultAPIServerAuthorizationMode
			}
			return getExtraArgs(cfg.BkeConfig.Cluster.APIServer.ExtraArgs)
		},
		"upgradeWithOpenFuyao": isUpgradeWithOpenFuyao,
	}
}

func isUpgradeWithOpenFuyao(cfg *BootScope) bool {
	log.Info("get upgradeWithOpenFuyao param")
	if _, ok := cfg.Extra["upgradeWithOpenFuyao"]; !ok {
		log.Info("not found upgradeWithOpenFuyao")
		return false
	}
	log.Info("upgradeWithOpenFuyao param is ", cfg.Extra["upgradeWithOpenFuyao"].(bool))
	return cfg.Extra["upgradeWithOpenFuyao"].(bool)
}

func etcdFuncMap() *template.FuncMap {
	return &template.FuncMap{
		"initialCluster": func(members []etcd.Member) string {
			if len(members) == 0 {
				ip, _ := bkenet.GetExternalIP()
				hostname := utils.HostName()
				return fmt.Sprintf("%s=https://%s:%d", hostname, ip, bkeetcd.EtcdListenPeerPort)
			}
			var result []string
			for _, member := range members {
				result = append(result, fmt.Sprintf("%s=%s", member.Name, member.PeerURL))
			}
			return strings.Join(result, ",")
		},
		"imageInfo": func(cfg *BootScope) string {
			var etcdVersion string
			if cfg.BkeConfig.Cluster.EtcdVersion != "" {
				etcdVersion = cfg.BkeConfig.Cluster.EtcdVersion
			} else {
				etcdVersion = bkeinit.DefaultEtcdImageTag
			}
			etcdVersion = strings.TrimPrefix(etcdVersion, "v") // 去掉前缀字符 v
			return fmt.Sprintf("%s:%s", bkeinit.DefaultEtcdImageName, etcdVersion)
		},
		"dataDir": func(cfg *BootScope) string {
			if cfg.CurrentNode.Etcd != nil && cfg.CurrentNode.Etcd.DataDir != "" {
				return cfg.CurrentNode.Etcd.DataDir
			}
			return cfg.BkeConfig.Cluster.Etcd.DataDir
		},
		"etcdAdvertiseUrls": func(cfg *BootScope) string {
			if v, ok := cfg.Extra["EtcdAdvertiseUrls"]; ok {
				return v.(string)
			}
			return etcd.GetClientURLByIP(cfg.HostIP)
		},
		"extraArgs": func(cfg *BootScope) []string {
			if cfg.CurrentNode.Etcd != nil && cfg.CurrentNode.Etcd.ExtraArgs != nil {
				return getExtraArgs(cfg.CurrentNode.Etcd.ExtraArgs)
			}
			return getExtraArgs(cfg.BkeConfig.Cluster.Etcd.ExtraArgs)
		},
	}

}

func controllerFuncMap() *template.FuncMap {
	return &template.FuncMap{
		"imageInfo": func(cfg *BootScope) string {
			k8sVersion := strings.TrimPrefix(cfg.BkeConfig.Cluster.KubernetesVersion, "v") // 去掉前缀字符 v
			return fmt.Sprintf("%s:%s", bkeinit.DefaultControllerManagerImageName, k8sVersion)
		},
		"extraArgs": func(cfg *BootScope) []string {
			if cfg.CurrentNode.ControllerManager != nil && cfg.CurrentNode.ControllerManager.ExtraArgs != nil {
				return getExtraArgs(cfg.CurrentNode.ControllerManager.ExtraArgs)
			}
			return getExtraArgs(cfg.BkeConfig.Cluster.ControllerManager.ExtraArgs)
		},
		"getSubnetMask": func(cidr string) string {
			res := strings.Split(cidr, "/")
			if len(res) != cidrPartsCount {
				return ""
			}
			mask, err := strconv.Atoi(res[cidrMaskIndex])
			if err != nil {
				return ""
			}
			if mask > subnetMaskThreshold {
				return res[cidrMaskIndex]
			}
			return strconv.Itoa(minSubnetMaskBits)
		},
	}
}

func schedulerFuncMap() *template.FuncMap {
	return &template.FuncMap{
		"imageInfo": func(cfg *BootScope) string {
			k8sVersion := strings.TrimPrefix(cfg.BkeConfig.Cluster.KubernetesVersion, "v") // 去掉前缀字符 v
			return fmt.Sprintf("%s:%s", bkeinit.DefaultSchedulerImageName, k8sVersion)
		},
		"extraArgs": func(cfg *BootScope) []string {
			if cfg.CurrentNode.Scheduler != nil && cfg.CurrentNode.Scheduler.ExtraArgs != nil {
				return getExtraArgs(cfg.CurrentNode.Scheduler.ExtraArgs)
			}
			return getExtraArgs(cfg.BkeConfig.Cluster.Scheduler.ExtraArgs)
		},
	}
}

// mergeFuncMap merge two funcMap
func mergeFuncMap(f1, f2 *template.FuncMap) *template.FuncMap {
	f := *f1
	for k, v := range *f2 {
		f[k] = v
	}
	return &f
}

func getExtraArgs(argsMap map[string]string) []string {
	var args []string
	for k, v := range argsMap {
		args = append(args, fmt.Sprintf("%s=%s", k, v))
	}
	return args
}
