// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package dag

import (
	"fmt"
	"slices"
)

// Node is an identifier with dependencies on other identifiers.
type Node struct {
	ID        string
	DependsOn []string
}

// UnknownDependencyError is returned when a dependency names an ID not present in nodes.
type UnknownDependencyError struct {
	From, To string
}

func (e *UnknownDependencyError) Error() string {
	return fmt.Sprintf("unknown dependency: %s depends on %s, but %s is not defined", e.From, e.To, e.To)
}

// DuplicateIDError is returned when a node ID appears more than once.
type DuplicateIDError struct {
	ID string
}

func (e *DuplicateIDError) Error() string {
	return fmt.Sprintf("duplicate node ID: %s", e.ID)
}

// CycleError is returned when there is a circular dependency.
// Cycle lists the IDs on the cycle in cycle order, starting from the lexically smallest member.
type CycleError struct {
	Cycle []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("circular dependency detected: %v", e.Cycle)
}

// Sort returns the IDs in dependency order (dependencies before dependents).
// The order is deterministic: among nodes whose dependencies are equally
// satisfied, IDs are emitted in lexical order. Two calls with the same input
// yield the identical slice.
//
// Errors (both are returned as *CycleError / *UnknownDependencyError /
// *DuplicateIDError so the caller can map them to its own error codes):
//   - a dependency naming an ID not present in nodes
//   - a dependency cycle; the error lists the IDs on the cycle, in cycle order,
//     starting from the lexically smallest member
//   - a duplicate node ID in the input
func Sort(nodes []Node) ([]string, error) {
	if len(nodes) == 0 {
		return []string{}, nil
	}

	// Build graph and validate
	idSet := make(map[string]bool)
	inDegree := make(map[string]int)
	graph := make(map[string][]string) // id -> dependents
	allIDs := make(map[string]bool)

	// First pass: collect all IDs and check for duplicates
	for _, node := range nodes {
		if idSet[node.ID] {
			return nil, &DuplicateIDError{ID: node.ID}
		}
		idSet[node.ID] = true
		allIDs[node.ID] = true
		inDegree[node.ID] = 0
	}

	// Second pass: build graph and check for unknown dependencies
	for _, node := range nodes {
		for _, dep := range node.DependsOn {
			if !idSet[dep] {
				return nil, &UnknownDependencyError{From: node.ID, To: dep}
			}
			// dep -> node (node depends on dep, so dep must come before node)
			graph[dep] = append(graph[dep], node.ID)
			inDegree[node.ID]++
		}
	}

	// Kahn's algorithm with sorted ready-set for determinism
	var result []string
	ready := make([]string, 0, len(nodes))

	// Find all nodes with in-degree 0
	for id := range idSet {
		if inDegree[id] == 0 {
			ready = append(ready, id)
		}
	}
	slices.Sort(ready)

	// Process nodes in lexical order to ensure determinism
	for len(ready) > 0 {
		// Take the lexically first node
		current := ready[0]
		ready = ready[1:]
		result = append(result, current)

		// Process all dependents of current
		var newReady []string
		for _, dependent := range graph[current] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				newReady = append(newReady, dependent)
			}
		}

		// Add new ready nodes and re-sort to maintain lexical order
		ready = append(ready, newReady...)
		slices.Sort(ready)
	}

	// Processing fewer nodes than registered indicates a cycle
	if len(result) != len(nodes) {
		// Find the cycle
		cycle, err := findCycle(nodes, idSet)
		if err != nil {
			return nil, err
		}
		return nil, &CycleError{Cycle: cycle}
	}

	return result, nil
}

// findCycle finds a concrete cycle in the remaining nodes.
// It returns the cycle as a slice starting from the lexically smallest member,
// in the order they appear in the cycle.
func findCycle(nodes []Node, allIDs map[string]bool) ([]string, error) {
	// Build reverse adjacency graph (what nodes depend on me)
	nodeMap := make(map[string]Node)
	dependents := make(map[string][]string) // id -> [nodes that depend on id]

	for _, node := range nodes {
		if allIDs[node.ID] {
			nodeMap[node.ID] = node
		}
	}

	for _, node := range nodes {
		if allIDs[node.ID] {
			for _, dep := range node.DependsOn {
				if allIDs[dep] {
					// dep must come before node, so node is a dependent of dep
					dependents[dep] = append(dependents[dep], node.ID)
				}
			}
		}
	}

	// Find any cycle using DFS, following the "must come after" order
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	var path []string

	var dfs func(string) ([]string, bool)
	dfs = func(id string) ([]string, bool) {
		visited[id] = true
		recStack[id] = true
		path = append(path, id)

		if _, ok := nodeMap[id]; !ok {
			return nil, false
		}

		for _, nextID := range dependents[id] {
			if !visited[nextID] {
				if cycle, found := dfs(nextID); found {
					return cycle, true
				}
			} else if recStack[nextID] {
				// Found a cycle: extract it from path
				cycleStart := -1
				for i, p := range path {
					if p == nextID {
						cycleStart = i
						break
					}
				}
				if cycleStart >= 0 {
					cycle := path[cycleStart:]
					// Rotate so it starts at lexically smallest
					minIdx := 0
					for i := 1; i < len(cycle); i++ {
						if cycle[i] < cycle[minIdx] {
							minIdx = i
						}
					}
					rotated := make([]string, len(cycle))
					for i := 0; i < len(cycle); i++ {
						rotated[i] = cycle[(minIdx+i)%len(cycle)]
					}
					return rotated, true
				}
			}
		}

		path = path[:len(path)-1]
		recStack[id] = false
		return nil, false
	}

	// Try to find a cycle starting from each node
	for id := range nodeMap {
		if !visited[id] {
			path = []string{}
			if cycle, found := dfs(id); found {
				return cycle, nil
			}
		}
	}

	// Fallback when DFS finds no cycle even though Kahn's algorithm
	// reported one: return a minimal self-referential path so the error
	// still identifies an offending node.
	for id := range nodeMap {
		return []string{id}, nil
	}

	return nil, fmt.Errorf("cycle detected but not found")
}
