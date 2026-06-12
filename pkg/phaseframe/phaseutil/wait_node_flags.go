/******************************************************************
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

package phaseutil

import (
	"context"
	"sync"
	"time"

	"github.com/pkg/errors"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
)

// NodeStateFlagReader reads node state flags for a cluster.
//
// It is intentionally defined as a small interface so callers can pass
// the existing NodeFetcher without importing its concrete type.
type NodeStateFlagReader interface {
	GetNodeStateFlagForCluster(ctx context.Context, bkeCluster *confv1beta1.BKECluster, ip string, flag int) (bool, error)
}

type WaitNodesStateFlagVisibleOptions struct {
	Timeout  time.Duration
	Interval time.Duration
}

func (o WaitNodesStateFlagVisibleOptions) withDefaults() WaitNodesStateFlagVisibleOptions {
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	if o.Interval <= 0 {
		o.Interval = 1 * time.Second
	}
	return o
}

// WaitNodesStateFlagVisible concurrently waits for `flag` to become visible for every node.
//
// Behavior:
// - For each node, poll `GetNodeStateFlagForCluster` every `Interval` until `Timeout`.
// - Any single-node timeout cancels the whole wait and returns an error.
// - Read errors are treated as transient and will be retried until timeout/cancel.
func WaitNodesStateFlagVisible(
	ctx context.Context,
	nf NodeStateFlagReader,
	bkeCluster *confv1beta1.BKECluster,
	nodes node.Nodes,
	flag int,
	opts WaitNodesStateFlagVisibleOptions,
) error {
	ctxWait, cancel := context.WithCancel(ctx)
	defer cancel()

	opts, err := validateAndNormalizeWaitNodesStateFlagVisibleArgs(nf, bkeCluster, nodes, opts)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(nodes))

	for _, n := range nodes {
		ip := n.IP
		wg.Add(1)
		go func() {
			defer wg.Done()
			waitSingleNodeStateFlagVisible(ctxWait, cancel, nf, bkeCluster, ip, flag, opts, errCh)
		}()
	}

	wg.Wait()
	close(errCh)

	for e := range errCh {
		if e != nil {
			return e
		}
	}
	return nil
}

func validateAndNormalizeWaitNodesStateFlagVisibleArgs(
	nf NodeStateFlagReader,
	bkeCluster *confv1beta1.BKECluster,
	nodes node.Nodes,
	opts WaitNodesStateFlagVisibleOptions,
) (WaitNodesStateFlagVisibleOptions, error) {
	if nf == nil {
		return WaitNodesStateFlagVisibleOptions{}, errors.New("node flag reader is nil")
	}
	if bkeCluster == nil {
		return WaitNodesStateFlagVisibleOptions{}, errors.New("bkeCluster is nil")
	}
	if len(nodes) == 0 {
		return WaitNodesStateFlagVisibleOptions{}, nil
	}
	return opts.withDefaults(), nil
}

func waitSingleNodeStateFlagVisible(
	ctxWait context.Context,
	cancelAll func(),
	nf NodeStateFlagReader,
	bkeCluster *confv1beta1.BKECluster,
	ip string,
	flag int,
	opts WaitNodesStateFlagVisibleOptions,
	errCh chan<- error,
) {
	ctxNode, cancelNode := context.WithTimeout(ctxWait, opts.Timeout)
	defer cancelNode()

	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctxNode.Done():
			if errors.Is(ctxNode.Err(), context.DeadlineExceeded) {
				errCh <- errors.Errorf("timed out waiting for flag %d on %s", flag, ip)
				cancelAll()
				return
			}
			errCh <- errors.Errorf("cancelled waiting for flag %d on %s", flag, ip)
			return
		case <-ticker.C:
			has, err := nf.GetNodeStateFlagForCluster(ctxWait, bkeCluster, ip, flag)
			if err != nil {
				continue
			}
			if has {
				return
			}
		}
	}
}
