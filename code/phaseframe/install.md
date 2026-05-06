# EnsureBKEAgent
# EnsureNodesEnv
- buildEnvCommand
```go
commandSpec.Commands = []agentv1beta1.ExecCommand{
		// Check whether the host hardware resources are sufficient to run k8s
		{
			ID: "node hardware resources check",
			Command: []string{
				"K8sEnvInit",
				"init=true",
				"check=true",
				"scope=node",
				bkeConfigStr,
			},
			Type:          agentv1beta1.CommandBuiltIn,
			BackoffIgnore: false,
		},
		// reset node
		{
			ID: "reset",
			Command: []string{
				"Reset",
				bkeConfigStr,
				scope,
				extra,
			},
			Type:          agentv1beta1.CommandBuiltIn,
			BackoffIgnore: true,
		},
		// init node env to run k8s
		{
			ID: "init and check node env",
			Command: []string{
				"K8sEnvInit",
				"init=true",
				"check=true",
				"scope=time,hosts,dns,kernel,firewall,selinux,swap,httpRepo,runtime,iptables,registry,extra",
				bkeConfigStr,
				extraHosts,
			},
			Type:          agentv1beta1.CommandBuiltIn,
			BackoffDelay:  5,
			BackoffIgnore: false,
		},
	}
```
```go
	defaultEnvExtraExecScripts = []string{
		"install-lxcfs.sh",
		"install-nfsutils.sh",
		"install-etcdctl.sh",
		"install-helm.sh",
		"install-calicoctl.sh",
		"update-runc.sh",
		"clean-docker-images.py",
	}

	commonEnvExtraExecScripts = []string{
		"file-downloader.sh",
		"package-downloader.sh",
	}
```
```go
update-runc.sh"
```
- finalDecisionAndCleanup
  - initClusterExtra
    - 从configMap中查找带有bke.bocloud.com/scripts标签的脚本
  - executeNodePreprocessScripts
  - 前置处理
    ```go
    // 创建执行前置处理脚本的命令
	execCommands := []agentv1beta1.ExecCommand{
		{
			ID: "execute-preprocess-scripts",
			Command: []string{
				"Preprocess", // 内置执行器名称，不传递nodeIP参数，PreprocessPlugin会自动获取当前节点IP
			},
			Type:          agentv1beta1.CommandBuiltIn,
			BackoffIgnore: false,
		},
	}
```
# EnsureClusterAPIObj

# EnsureCerts
- Secret:kube-system/global-ca
- ConfigMap:kube-system/cluster-cert-config

# EnsureLoadBalance
