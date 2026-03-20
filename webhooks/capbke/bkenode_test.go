package capbke

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/nodeutil"
)

func TestBKENode_Default(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	webhook := &BKENode{Client: client}

	t.Run("set default port", func(t *testing.T) {
		node := &confv1beta1.BKENode{
			ObjectMeta: metav1.ObjectMeta{Name: "test-node", Namespace: "default"},
			Spec:       confv1beta1.BKENodeSpec{IP: "192.168.1.1"},
		}
		err := webhook.Default(context.Background(), node)
		assert.NoError(t, err)
		assert.Equal(t, "22", node.Spec.Port)
	})

	t.Run("keep existing port", func(t *testing.T) {
		node := &confv1beta1.BKENode{
			ObjectMeta: metav1.ObjectMeta{Name: "test-node", Namespace: "default"},
			Spec:       confv1beta1.BKENodeSpec{IP: "192.168.1.1", Port: "2222"},
		}
		err := webhook.Default(context.Background(), node)
		assert.NoError(t, err)
		assert.Equal(t, "2222", node.Spec.Port)
	})
}

func TestBKENode_ValidateCreate(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	t.Run("valid node", func(t *testing.T) {
		node := &confv1beta1.BKENode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-node",
				Namespace: "default",
				Labels:    map[string]string{nodeutil.ClusterNameLabel: "test-cluster"},
			},
			Spec: confv1beta1.BKENodeSpec{
				IP:       "192.168.1.1",
				Port:     "22",
				Username: "root",
				Password: "test",
				Role:     []string{"master"},
			},
		}
		client := fake.NewClientBuilder().WithScheme(scheme).Build()
		webhook := &BKENode{Client: client}
		_, err := webhook.ValidateCreate(context.Background(), node)
		assert.NoError(t, err)
	})

	t.Run("missing cluster label", func(t *testing.T) {
		node := &confv1beta1.BKENode{
			ObjectMeta: metav1.ObjectMeta{Name: "test-node", Namespace: "default"},
			Spec:       confv1beta1.BKENodeSpec{IP: "192.168.1.1"},
		}
		client := fake.NewClientBuilder().WithScheme(scheme).Build()
		webhook := &BKENode{Client: client}
		_, err := webhook.ValidateCreate(context.Background(), node)
		assert.Error(t, err)
	})
}

func TestBKENode_ValidateUpdate(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	t.Run("IP change not allowed", func(t *testing.T) {
		oldNode := &confv1beta1.BKENode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-node",
				Namespace: "default",
				Labels:    map[string]string{nodeutil.ClusterNameLabel: "test-cluster"},
			},
			Spec: confv1beta1.BKENodeSpec{IP: "192.168.1.1", Port: "22", Username: "root", Password: "test", Role: []string{"master"}},
		}
		newNode := oldNode.DeepCopy()
		newNode.Spec.IP = "192.168.1.2"

		client := fake.NewClientBuilder().WithScheme(scheme).Build()
		webhook := &BKENode{Client: client}
		_, err := webhook.ValidateUpdate(context.Background(), oldNode, newNode)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "IP cannot be changed")
	})

	t.Run("role change in pending state not allowed", func(t *testing.T) {
		oldNode := &confv1beta1.BKENode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-node",
				Namespace: "default",
				Labels:    map[string]string{nodeutil.ClusterNameLabel: "test-cluster"},
			},
			Spec:   confv1beta1.BKENodeSpec{IP: "192.168.1.1", Port: "22", Username: "root", Password: "test", Role: []string{"master"}},
			Status: confv1beta1.BKENodeStatus{State: confv1beta1.NodePending},
		}
		newNode := oldNode.DeepCopy()
		newNode.Spec.Role = []string{"worker"}

		client := fake.NewClientBuilder().WithScheme(scheme).Build()
		webhook := &BKENode{Client: client}
		_, err := webhook.ValidateUpdate(context.Background(), oldNode, newNode)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot change node role")
	})
}

func TestBKENode_ValidateDelete(t *testing.T) {
	t.Run("delete pending node not allowed", func(t *testing.T) {
		node := &confv1beta1.BKENode{
			ObjectMeta: metav1.ObjectMeta{Name: "test-node", Namespace: "default"},
			Status:     confv1beta1.BKENodeStatus{State: confv1beta1.NodePending},
		}
		webhook := &BKENode{}
		_, err := webhook.ValidateDelete(context.Background(), node)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot delete node while in pending state")
	})

	t.Run("delete upgrading node not allowed", func(t *testing.T) {
		node := &confv1beta1.BKENode{
			ObjectMeta: metav1.ObjectMeta{Name: "test-node", Namespace: "default"},
			Status:     confv1beta1.BKENodeStatus{State: confv1beta1.NodeUpgrading},
		}
		webhook := &BKENode{}
		_, err := webhook.ValidateDelete(context.Background(), node)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot delete node while upgrading")
	})

	t.Run("delete ready node allowed", func(t *testing.T) {
		node := &confv1beta1.BKENode{
			ObjectMeta: metav1.ObjectMeta{Name: "test-node", Namespace: "default"},
			Status:     confv1beta1.BKENodeStatus{State: confv1beta1.NodeReady},
		}
		webhook := &BKENode{}
		_, err := webhook.ValidateDelete(context.Background(), node)
		assert.NoError(t, err)
	})
}

func TestEncryptPasswordIfNeeded(t *testing.T) {
	t.Run("plain password", func(t *testing.T) {
		result, err := encryptPasswordIfNeeded("plaintext")
		assert.NoError(t, err)
		assert.NotEqual(t, "plaintext", result)
	})

	t.Run("empty password", func(t *testing.T) {
		result, err := encryptPasswordIfNeeded("")
		assert.NoError(t, err)
		assert.NotEmpty(t, result)
	})
}

func TestBKENode_ValidateCreateInvalidType(t *testing.T) {
	webhook := &BKENode{}
	_, err := webhook.ValidateCreate(context.Background(), &bkev1beta1.BKECluster{})
	assert.Error(t, err)
}

func TestBKENode_ValidateUpdateInvalidType(t *testing.T) {
	webhook := &BKENode{}
	_, err := webhook.ValidateUpdate(context.Background(), &bkev1beta1.BKECluster{}, &bkev1beta1.BKECluster{})
	assert.Error(t, err)
}

func TestBKENode_ValidateDeleteInvalidType(t *testing.T) {
	webhook := &BKENode{}
	_, err := webhook.ValidateDelete(context.Background(), &bkev1beta1.BKECluster{})
	assert.Error(t, err)
}

func TestBKENode_DefaultInvalidType(t *testing.T) {
	webhook := &BKENode{}
	err := webhook.Default(context.Background(), &bkev1beta1.BKECluster{})
	assert.Error(t, err)
}

