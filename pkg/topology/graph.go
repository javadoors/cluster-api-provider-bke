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

package topology

import (
	"fmt"
	"sort"
)

// Graph is a directed acyclic graph used for upgrade scheduling.
// An edge From -> To means From must complete before To runs.
type Graph struct {
	nodes    map[string]struct{}
	outEdges map[string]map[string]struct{}
	inDegree map[string]int
}

// NewGraph creates an empty graph.
func NewGraph() *Graph {
	return &Graph{
		nodes:    make(map[string]struct{}),
		outEdges: make(map[string]map[string]struct{}),
		inDegree: make(map[string]int),
	}
}

// AddNode registers a node. Idempotent.
func (g *Graph) AddNode(name string) {
	if name == "" {
		return
	}
	if _, ok := g.nodes[name]; ok {
		return
	}
	g.nodes[name] = struct{}{}
	if _, ok := g.inDegree[name]; !ok {
		g.inDegree[name] = 0
	}
	if g.outEdges[name] == nil {
		g.outEdges[name] = make(map[string]struct{})
	}
}

// AddEdge adds a dependency edge: prerequisite must finish before dependent.
func (g *Graph) AddEdge(prerequisite, dependent string) error {
	if prerequisite == "" || dependent == "" {
		return fmt.Errorf("edge requires non-empty endpoints")
	}
	if prerequisite == dependent {
		return fmt.Errorf("self-edge on %q", prerequisite)
	}
	g.AddNode(prerequisite)
	g.AddNode(dependent)
	if g.hasPath(dependent, prerequisite) {
		return fmt.Errorf("cycle detected: %q -> %q", prerequisite, dependent)
	}
	if _, ok := g.outEdges[prerequisite][dependent]; ok {
		return nil
	}
	g.outEdges[prerequisite][dependent] = struct{}{}
	g.inDegree[dependent]++
	return nil
}

// Nodes returns sorted node names.
func (g *Graph) Nodes() []string {
	names := make([]string, 0, len(g.nodes))
	for name := range g.nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TopologicalBatches returns execution batches in dependency order.
// Nodes in the same batch have no dependency on each other and may run concurrently.
func (g *Graph) TopologicalBatches() ([][]string, error) {
	if len(g.nodes) == 0 {
		return nil, nil
	}
	inDegree := make(map[string]int, len(g.inDegree))
	for name, deg := range g.inDegree {
		inDegree[name] = deg
	}

	var batches [][]string
	remaining := len(g.nodes)
	processed := make(map[string]struct{}, len(g.nodes))

	for remaining > 0 {
		var batch []string
		for name := range g.nodes {
			if _, done := processed[name]; done {
				continue
			}
			if inDegree[name] == 0 {
				batch = append(batch, name)
			}
		}
		if len(batch) == 0 {
			return nil, fmt.Errorf("graph has cycles")
		}
		sort.Strings(batch)
		batches = append(batches, batch)

		for _, name := range batch {
			processed[name] = struct{}{}
			remaining--
			for succ := range g.outEdges[name] {
				inDegree[succ]--
			}
		}
	}
	return batches, nil
}

func (g *Graph) hasPath(from, to string) bool {
	if from == to {
		return true
	}
	visited := make(map[string]struct{}, 8)
	current := []string{from}
	visited[from] = struct{}{}

	// Two-queue BFS:
	// - iterate over `current` using range without mutating it
	// - collect successors into `next` only
	for len(current) > 0 {
		next := make([]string, 0, 8)
		for _, cur := range current {
			if cur == to {
				return true
			}
			for succ := range g.outEdges[cur] {
				if succ == to {
					return true
				}
				if _, seen := visited[succ]; seen {
					continue
				}
				visited[succ] = struct{}{}
				next = append(next, succ)
			}
		}
		current = next
	}
	return false
}
