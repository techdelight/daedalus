// Copyright (C) 2026 Techdelight BV

package core

import "fmt"

// Programme represents a named collection of related projects with dependency relationships.
type Programme struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Projects    []string         `json:"projects"`
	Deps        []DependencyEdge `json:"deps,omitempty"`
}

// DependencyEdge declares that Downstream depends on Upstream.
type DependencyEdge struct {
	Upstream   string `json:"upstream"`
	Downstream string `json:"downstream"`
}

// DependencyGraph provides graph operations over a programme's dependency edges.
type DependencyGraph struct {
	projects []string
	edges    []DependencyEdge
}

// NewDependencyGraph creates a graph from a programme's projects and edges.
func NewDependencyGraph(projects []string, edges []DependencyEdge) *DependencyGraph {
	return &DependencyGraph{projects: projects, edges: edges}
}

// TopologicalSort returns projects in dependency order (upstream before downstream).
// Returns an error if the graph contains a cycle.
func (g *DependencyGraph) TopologicalSort() ([]string, error) {
	// Kahn's algorithm
	adj := make(map[string][]string)
	inDeg := make(map[string]int)
	for _, p := range g.projects {
		inDeg[p] = 0
	}
	for _, e := range g.edges {
		adj[e.Upstream] = append(adj[e.Upstream], e.Downstream)
		inDeg[e.Downstream]++
	}

	var queue []string
	for _, p := range g.projects {
		if inDeg[p] == 0 {
			queue = append(queue, p)
		}
	}

	var result []string
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		result = append(result, node)
		for _, next := range adj[node] {
			inDeg[next]--
			if inDeg[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if len(result) != len(g.projects) {
		return nil, fmt.Errorf("dependency cycle detected")
	}
	return result, nil
}

// DetectCycles returns true if the dependency graph contains a cycle.
func (g *DependencyGraph) DetectCycles() bool {
	_, err := g.TopologicalSort()
	return err != nil
}

// Downstreams returns all projects that directly depend on the named project.
func (g *DependencyGraph) Downstreams(project string) []string {
	var result []string
	for _, e := range g.edges {
		if e.Upstream == project {
			result = append(result, e.Downstream)
		}
	}
	return result
}

// Upstreams returns all projects that the named project directly depends on.
func (g *DependencyGraph) Upstreams(project string) []string {
	var result []string
	for _, e := range g.edges {
		if e.Downstream == project {
			result = append(result, e.Upstream)
		}
	}
	return result
}

// ValidateProgrammeName checks whether name is a valid programme name.
// Same rules as project names.
func ValidateProgrammeName(name string) error {
	if name == "" {
		return fmt.Errorf("programme name cannot be empty")
	}
	if !validProjectName.MatchString(name) {
		return fmt.Errorf("invalid programme name %q: must start with alphanumeric and contain only [a-zA-Z0-9._-]", name)
	}
	return nil
}
