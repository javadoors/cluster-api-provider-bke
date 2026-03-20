/******************************************************************
 * Copyright (c) 2024 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

package certs

import (
	"context"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/pkiutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/log"
)

type BKEKubernetesCertGetter struct {
	certNamespace   string
	certClusterName string

	bkeCluster *bkev1beta1.BKECluster
	client     client.Client
	ctx        context.Context

	log *zap.SugaredLogger
}

type CertContent struct {
	Key  string
	Cert string
}

func NewBKEKubernetesCertGetter(ctx context.Context, client client.Client, bkeCluster *bkev1beta1.BKECluster) *BKEKubernetesCertGetter {
	return &BKEKubernetesCertGetter{
		certNamespace:   bkeCluster.Namespace,
		certClusterName: bkeCluster.Name,
		bkeCluster:      bkeCluster,
		client:          client,
		ctx:             ctx,
		log:             log.Named("certsGetter").Named(utils.ClientObjNS(bkeCluster)),
	}
}

func (g *BKEKubernetesCertGetter) GetCertContent(cert *pkiutil.BKECert) (*CertContent, error) {
	secretName := NewCertSecretName(g.certClusterName, cert.Name)
	secret := &corev1.Secret{}
	err := g.client.Get(g.ctx, types.NamespacedName{Namespace: g.certNamespace, Name: secretName}, secret)
	if err != nil {
		return nil, err
	}
	return &CertContent{
		Key:  string(secret.Data[TLSKeyDataName]),
		Cert: string(secret.Data[TLSCrtDataName]),
	}, nil
}

func (g *BKEKubernetesCertGetter) GetTargetClusterKubeconfig() (string, error) {
	key := client.ObjectKey{
		Namespace: g.certNamespace,
		Name:      NewCertSecretName(g.certClusterName, "kubeconfig"),
	}
	secret := &corev1.Secret{}
	err := g.client.Get(g.ctx, key, secret)
	if err != nil {
		return "", err
	}
	return string(secret.Data["value"]), nil
}
