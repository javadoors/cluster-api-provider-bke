/*
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 */

package command

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkeagent/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
)

const (
	numC0       = 0
	numC1       = 1
	numC2       = 2
	numC3       = 3
	testNS      = "test-ns"
	testClName  = "test-cluster"
	testCmdName = "test-command"
	testNodeIP  = "192.168.1.1"
	testNodeIP2 = "192.168.1.2"
	testHost    = "test-node"
	cmdTimeout  = 5 * time.Minute
	cmdInterval = 2 * time.Second
)

func TestGenerateBkeConfigStr(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		config    string
		expected  string
	}{
		{
			name:      "Normal case",
			namespace: testNS,
			config:    testClName,
			expected:  "bkeConfig=test-ns:test-cluster",
		},
		{
			name:      "Empty namespace",
			namespace: "",
			config:    testClName,
			expected:  "bkeConfig=:test-cluster",
		},
		{
			name:      "Empty config",
			namespace: testNS,
			config:    "",
			expected:  "bkeConfig=test-ns:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateBkeConfigStr(tt.namespace, tt.config)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateDefaultCommandSpec(t *testing.T) {
	spec := GenerateDefaultCommandSpec()

	assert.NotNil(t, spec)
	assert.Equal(t, DefaultBackoffLimit, spec.BackoffLimit)
	assert.Equal(t, DefaultActiveDeadlineSecond, spec.ActiveDeadlineSecond)
	assert.Equal(t, DefaultTTLSecondsAfterFinished, spec.TTLSecondsAfterFinished)
	assert.NotNil(t, spec.NodeSelector)
	assert.False(t, spec.Suspend)
}

func TestValidateCommand(t *testing.T) {
	tests := []struct {
		name    string
		cmd     *agentv1beta1.Command
		wantErr bool
	}{
		{
			name: "Valid with node name",
			cmd: &agentv1beta1.Command{
				Spec: agentv1beta1.CommandSpec{
					NodeName: testNodeIP,
				},
			},
			wantErr: false,
		},
		{
			name: "Valid with node selector",
			cmd: &agentv1beta1.Command{
				Spec: agentv1beta1.CommandSpec{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test": "value",
						},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCommand(tt.cmd)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCheckCommandStatus(t *testing.T) {
	tests := []struct {
		name           string
		cmd            *agentv1beta1.Command
		expectComplete bool
	}{
		{
			name: "Nil status",
			cmd: &agentv1beta1.Command{
				Status: nil,
			},
			expectComplete: false,
		},
		{
			name: "Suspended command",
			cmd: &agentv1beta1.Command{
				Spec: agentv1beta1.CommandSpec{
					Suspend: true,
				},
				Status: map[string]*agentv1beta1.CommandStatus{
					testNodeIP: {
						Phase: agentv1beta1.CommandRunning,
					},
				},
			},
			expectComplete: false,
		},
		{
			name: "Complete - all success",
			cmd: &agentv1beta1.Command{
				Spec: agentv1beta1.CommandSpec{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							testNodeIP: testNodeIP,
						},
					},
				},
				Status: map[string]*agentv1beta1.CommandStatus{
					testNodeIP: {
						Phase:  agentv1beta1.CommandComplete,
						Status: metav1.ConditionTrue,
					},
				},
			},
			expectComplete: true,
		},
		{
			name: "Complete - with failure",
			cmd: &agentv1beta1.Command{
				Spec: agentv1beta1.CommandSpec{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							testNodeIP:  testNodeIP,
							testNodeIP2: testNodeIP2,
						},
					},
				},
				Status: map[string]*agentv1beta1.CommandStatus{
					testNodeIP: {
						Phase:  agentv1beta1.CommandComplete,
						Status: metav1.ConditionTrue,
					},
					testNodeIP2: {
						Phase:  agentv1beta1.CommandFailed,
						Status: metav1.ConditionFalse,
					},
				},
			},
			expectComplete: true,
		},
		{
			name: "Running command",
			cmd: &agentv1beta1.Command{
				Status: map[string]*agentv1beta1.CommandStatus{
					testNodeIP: {
						Phase: agentv1beta1.CommandRunning,
					},
				},
			},
			expectComplete: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			complete, _, _ := CheckCommandStatus(tt.cmd)
			assert.Equal(t, tt.expectComplete, complete)
		})
	}
}

func TestIsOwnerRefCommand(t *testing.T) {
	tests := []struct {
		name   string
		object *metav1.ObjectMeta
		cmd    *agentv1beta1.Command
		expect bool
	}{
		{
			name: "Is owner reference",
			object: &metav1.ObjectMeta{
				Name: "owner",
				UID:  "test-uid",
			},
			cmd: &agentv1beta1.Command{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							Name: "owner",
							UID:  "test-uid",
						},
					},
				},
			},
			expect: true,
		},
		{
			name: "Not owner reference - different UID",
			object: &metav1.ObjectMeta{
				Name: "owner",
				UID:  "test-uid",
			},
			cmd: &agentv1beta1.Command{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							Name: "owner",
							UID:  "different-uid",
						},
					},
				},
			},
			expect: false,
		},
		{
			name: "No owner references",
			object: &metav1.ObjectMeta{
				Name: "owner",
				UID:  "test-uid",
			},
			cmd: &agentv1beta1.Command{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{},
				},
			},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsOwnerRefCommand(tt.object, *tt.cmd)
			assert.Equal(t, tt.expect, result)
		})
	}
}

func TestGetNodeSelector(t *testing.T) {
	nodes := bkenode.Nodes{
		{IP: testNodeIP, Hostname: testHost},
		{IP: testNodeIP2, Hostname: "node2"},
	}

	selector := getNodeSelector(nodes)

	assert.NotNil(t, selector)
	assert.NotNil(t, selector.MatchLabels)
	assert.Equal(t, testNodeIP, selector.MatchLabels[testNodeIP])
	assert.Equal(t, testNodeIP2, selector.MatchLabels[testNodeIP2])
}

func TestConstants(t *testing.T) {
	assert.Equal(t, "bootstrap-", BootstrapCommandNamePrefix)
	assert.Equal(t, "k8s-ha-deploy", HACommandName)
	assert.Equal(t, "k8s-env-init", K8sEnvCommandName)
	assert.Equal(t, "switch-cluster-", SwitchClusterCommandNamePrefix)
	assert.Equal(t, "reset-node-", ResetNodeCommandNamePrefix)
	assert.Equal(t, "upgrade-node-", UpgradeNodeCommandNamePrefix)
	assert.Equal(t, "ping-", PingCommandNamePrefix)
	assert.Equal(t, "collect-", CollectCertCommandNamePrefix)
	assert.Equal(t, "bke.bocloud.com/cluster-command", BKEClusterLabel)
	assert.Equal(t, "bke.bocloud.com/machine-command", BKEMachineLabel)
	assert.Equal(t, 3, DefaultBackoffLimit)
	assert.Equal(t, 1000, DefaultActiveDeadlineSecond)
	assert.Equal(t, 600, DefaultTTLSecondsAfterFinished)
	assert.Equal(t, cmdTimeout, DefaultWaitTimeout)
	assert.Equal(t, cmdInterval, DefaultWaitInterval)
}

func TestBootstrapConstants(t *testing.T) {
	assert.Equal(t, "bke.bocloud.com/master-init-command", MasterInitCommandLabel)
	assert.Equal(t, "bke.bocloud.com/master-join-command", MasterJoinCommandLabel)
	assert.Equal(t, "bke.bocloud.com/worker-join-command", WorkerJoinCommandLabel)
}

func TestTimeoutCaseResult(t *testing.T) {
	result := TimeoutCaseResult{
		Err:          nil,
		Complete:     true,
		SuccessNodes: []string{testNodeIP},
		FailedNodes:  []string{},
	}

	assert.True(t, result.Complete)
	assert.Nil(t, result.Err)
	assert.Equal(t, []string{testNodeIP}, result.SuccessNodes)
}

func TestCommandNodes(t *testing.T) {
	nodes := CommandNodes{
		SuccessNodes: []string{testNodeIP},
		FailedNodes:  []string{testNodeIP2},
	}

	assert.Equal(t, numC1, len(nodes.SuccessNodes))
	assert.Equal(t, numC1, len(nodes.FailedNodes))
}

func TestWaitCommandResult(t *testing.T) {
	result := WaitCommandResult{
		Err:          nil,
		Complete:     true,
		SuccessNodes: []string{testNodeIP},
		FailedNodes:  []string{},
	}

	assert.True(t, result.Complete)
	assert.Nil(t, result.Err)
}

func TestGenerateDefaultCommandSpecFields(t *testing.T) {
	spec := GenerateDefaultCommandSpec()

	assert.NotNil(t, spec)
	assert.Empty(t, spec.NodeName)
	assert.False(t, spec.Suspend)
	assert.Empty(t, spec.Commands)
	assert.Equal(t, DefaultBackoffLimit, spec.BackoffLimit)
	assert.Equal(t, DefaultActiveDeadlineSecond, spec.ActiveDeadlineSecond)
	assert.Equal(t, DefaultTTLSecondsAfterFinished, spec.TTLSecondsAfterFinished)
	assert.NotNil(t, spec.NodeSelector)
}

func TestBaseCommandValidate(t *testing.T) {
	tests := []struct {
		name    string
		cmd     *BaseCommand
		wantErr bool
	}{
		{
			name: "Valid base command",
			cmd: &BaseCommand{
				Client:    fake.NewClientBuilder().Build(),
				NameSpace: testNS,
				Scheme:    runtime.NewScheme(),
			},
			wantErr: false,
		},
		{
			name: "Nil client",
			cmd: &BaseCommand{
				Client:    nil,
				NameSpace: testNS,
				Scheme:    runtime.NewScheme(),
			},
			wantErr: true,
		},
		{
			name: "Nil scheme",
			cmd: &BaseCommand{
				Client:    fake.NewClientBuilder().Build(),
				NameSpace: testNS,
				Scheme:    nil,
			},
			wantErr: true,
		},
		{
			name: "Empty namespace",
			cmd: &BaseCommand{
				Client:    fake.NewClientBuilder().Build(),
				NameSpace: "",
				Scheme:    runtime.NewScheme(),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cmd.validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateBkeCommand(t *testing.T) {
	tests := []struct {
		name    string
		nodes   bkenode.Nodes
		config  string
		cmd     *BaseCommand
		wantErr bool
	}{
		{
			name:   "Valid command",
			nodes:  bkenode.Nodes{{IP: testNodeIP, Hostname: testHost}},
			config: testBKEConfig,
			cmd: &BaseCommand{
				Client:    fake.NewClientBuilder().Build(),
				NameSpace: testNS,
				Scheme:    runtime.NewScheme(),
			},
			wantErr: false,
		},
		{
			name:   "Empty nodes",
			nodes:  bkenode.Nodes{},
			config: testBKEConfig,
			cmd: &BaseCommand{
				Client:    fake.NewClientBuilder().Build(),
				NameSpace: testNS,
				Scheme:    runtime.NewScheme(),
			},
			wantErr: true,
		},
		{
			name:   "Empty config",
			nodes:  bkenode.Nodes{{IP: testNodeIP, Hostname: testHost}},
			config: "",
			cmd: &BaseCommand{
				Client:    fake.NewClientBuilder().Build(),
				NameSpace: testNS,
				Scheme:    runtime.NewScheme(),
			},
			wantErr: true,
		},
		{
			name:   "Nil client",
			nodes:  bkenode.Nodes{{IP: testNodeIP, Hostname: testHost}},
			config: testBKEConfig,
			cmd: &BaseCommand{
				Client:    nil,
				NameSpace: testNS,
				Scheme:    runtime.NewScheme(),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBkeCommand(tt.nodes, tt.config, tt.cmd)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBaseCommandSetCommandName(t *testing.T) {
	cmd := &BaseCommand{}
	cmd.setCommandName(testCmdName)
	assert.Equal(t, testCmdName, cmd.commandName)
}

func TestBaseCommandGetCommand(t *testing.T) {
	tests := []struct {
		name    string
		cmd     *BaseCommand
		wantErr bool
	}{
		{
			name: "Empty command name",
			cmd: &BaseCommand{
				Ctx:         context.Background(),
				Client:      fake.NewClientBuilder().Build(),
				NameSpace:   testNS,
				Scheme:      runtime.NewScheme(),
				commandName: "",
			},
			wantErr: true,
		},
		{
			name: "Command not found",
			cmd: &BaseCommand{
				Ctx:         context.Background(),
				Client:      fake.NewClientBuilder().Build(),
				NameSpace:   testNS,
				Scheme:      runtime.NewScheme(),
				commandName: "non-existent-command",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, err := tt.cmd.GetCommand()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, command)
			}
		})
	}
}

func TestBaseCommandDeleteCommand(t *testing.T) {
	tests := []struct {
		name    string
		cmd     *BaseCommand
		wantErr bool
	}{
		{
			name: "Nil command",
			cmd: &BaseCommand{
				Ctx:       context.Background(),
				Client:    fake.NewClientBuilder().Build(),
				NameSpace: testNS,
				Scheme:    runtime.NewScheme(),
				Command:   nil,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cmd.deleteCommand()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBaseCommandWaitCommandComplete(t *testing.T) {
	cmd := &BaseCommand{
		Ctx:         context.Background(),
		Client:      fake.NewClientBuilder().Build(),
		NameSpace:   testNS,
		Scheme:      runtime.NewScheme(),
		commandName: "",
	}

	err, complete, nodes := cmd.waitCommandComplete()
	assert.Error(t, err)
	assert.False(t, complete)
	assert.Nil(t, nodes.SuccessNodes)
	assert.Nil(t, nodes.FailedNodes)
}

func TestBaseCommandWaitCommandCompleteWithStruct(t *testing.T) {
	cmd := &BaseCommand{
		Ctx:         context.Background(),
		Client:      fake.NewClientBuilder().Build(),
		NameSpace:   testNS,
		Scheme:      runtime.NewScheme(),
		commandName: "",
	}

	result := cmd.waitCommandCompleteWithStruct()
	assert.Error(t, result.Err)
	assert.False(t, result.Complete)
	assert.Nil(t, result.SuccessNodes)
	assert.Nil(t, result.FailedNodes)
}

func TestBaseCommandHandleTimeoutCase(t *testing.T) {
	cmd := &BaseCommand{
		Ctx:         context.Background(),
		Client:      fake.NewClientBuilder().Build(),
		NameSpace:   testNS,
		Scheme:      runtime.NewScheme(),
		commandName: "non-existent-command",
	}

	result := cmd.handleTimeoutCase(false, []string{}, []string{})
	assert.Error(t, result.Err)
	assert.False(t, result.Complete)
}

func TestClusterNameLabelSelectorRequirement(t *testing.T) {
	cmd := &BaseCommand{
		ClusterName: testClName,
	}

	requirement := cmd.ClusterNameLabelSelectorRequirement()
	assert.Equal(t, utils.ClusterNameLabelKey, requirement.Key)
	assert.Equal(t, metav1.LabelSelectorOpIn, requirement.Operator)
	assert.Contains(t, requirement.Values, testClName)
}

func TestBaseCommandBuildLabels(t *testing.T) {
	cmd := &BaseCommand{
		ClusterName: testClName,
	}

	labels := cmd.buildLabels("test-label", []string{"custom-label"})
	assert.NotNil(t, labels)
	assert.Contains(t, labels, "test-label")
	assert.Contains(t, labels, clusterv1.ClusterNameLabel)
	assert.Contains(t, labels, "custom-label")
}

func TestBaseCommandBuildLabelsWithoutClusterName(t *testing.T) {
	cmd := &BaseCommand{
		ClusterName: "",
	}

	labels := cmd.buildLabels("test-label", []string{})
	assert.NotNil(t, labels)
	assert.Contains(t, labels, "test-label")
	assert.NotContains(t, labels, clusterv1.ClusterNameLabel)
}

func TestBaseCommandSetOwnerReference(t *testing.T) {
	tests := []struct {
		name    string
		cmd     *BaseCommand
		wantErr bool
	}{
		{
			name: "Nil owner object",
			cmd: &BaseCommand{
				Scheme: runtime.NewScheme(),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := &agentv1beta1.Command{}
			err := tt.cmd.setOwnerReference(command)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBaseCommandHandleUniqueCommand(t *testing.T) {
	tests := []struct {
		name    string
		cmd     *BaseCommand
		wantErr bool
	}{
		{
			name: "Not unique command",
			cmd: &BaseCommand{
				Ctx:       context.Background(),
				Client:    fake.NewClientBuilder().Build(),
				NameSpace: testNS,
				Scheme:    runtime.NewScheme(),
				Unique:    false,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cmd.handleUniqueCommand("test-command")
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBaseCommandWaitCommandCompleteWithDefaults(t *testing.T) {
	cmd := &BaseCommand{
		Ctx:          context.Background(),
		Client:       fake.NewClientBuilder().Build(),
		NameSpace:    testNS,
		Scheme:       runtime.NewScheme(),
		commandName:  "",
		WaitTimeout:  0,
		WaitInterval: 0,
	}

	result := cmd.waitCommandCompleteWithStruct()
	assert.Error(t, result.Err)
	assert.Equal(t, time.Duration(0), cmd.WaitInterval)
	assert.Equal(t, time.Duration(0), cmd.WaitTimeout)
}
