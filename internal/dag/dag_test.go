// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package dag

import (
	"errors"
	"slices"
	"testing"
)

func TestSort(t *testing.T) {
	tests := []struct {
		name      string
		nodes     []Node
		want      []string
		wantError func(error) bool
	}{
		{
			name:      "empty input",
			nodes:     []Node{},
			want:      []string{},
			wantError: func(err error) bool { return err == nil },
		},
		{
			name: "single node no deps",
			nodes: []Node{
				{ID: "a", DependsOn: []string{}},
			},
			want:      []string{"a"},
			wantError: func(err error) bool { return err == nil },
		},
		{
			name: "linear chain",
			nodes: []Node{
				{ID: "a", DependsOn: []string{}},
				{ID: "b", DependsOn: []string{"a"}},
				{ID: "c", DependsOn: []string{"b"}},
			},
			want:      []string{"a", "b", "c"},
			wantError: func(err error) bool { return err == nil },
		},
		{
			name: "diamond: a→b, a→c, b→d, c→d",
			nodes: []Node{
				{ID: "a", DependsOn: []string{}},
				{ID: "b", DependsOn: []string{"a"}},
				{ID: "c", DependsOn: []string{"a"}},
				{ID: "d", DependsOn: []string{"b", "c"}},
			},
			want:      []string{"a", "b", "c", "d"},
			wantError: func(err error) bool { return err == nil },
		},
		{
			name: "determinism: reversed input same output",
			nodes: []Node{
				{ID: "c", DependsOn: []string{"b"}},
				{ID: "b", DependsOn: []string{"a"}},
				{ID: "a", DependsOn: []string{}},
			},
			want:      []string{"a", "b", "c"},
			wantError: func(err error) bool { return err == nil },
		},
		{
			name: "unknown dependency",
			nodes: []Node{
				{ID: "a", DependsOn: []string{"x"}},
			},
			want: nil,
			wantError: func(err error) bool {
				var ude *UnknownDependencyError
				if !errors.As(err, &ude) {
					return false
				}
				return ude.From == "a" && ude.To == "x"
			},
		},
		{
			name: "duplicate ID",
			nodes: []Node{
				{ID: "a", DependsOn: []string{}},
				{ID: "a", DependsOn: []string{}},
			},
			want: nil,
			wantError: func(err error) bool {
				var dup *DuplicateIDError
				if !errors.As(err, &dup) {
					return false
				}
				return dup.ID == "a"
			},
		},
		{
			name: "self-dependency",
			nodes: []Node{
				{ID: "a", DependsOn: []string{"a"}},
			},
			want: nil,
			wantError: func(err error) bool {
				var ce *CycleError
				if !errors.As(err, &ce) {
					return false
				}
				return len(ce.Cycle) == 1 && ce.Cycle[0] == "a"
			},
		},
		{
			name: "2-cycle: a→b, b→a",
			nodes: []Node{
				{ID: "a", DependsOn: []string{"b"}},
				{ID: "b", DependsOn: []string{"a"}},
			},
			want: nil,
			wantError: func(err error) bool {
				var ce *CycleError
				if !errors.As(err, &ce) {
					return false
				}
				// Cycle should start at lexically smallest and be in cycle order
				if len(ce.Cycle) != 2 {
					return false
				}
				return ce.Cycle[0] == "a" && ce.Cycle[1] == "b"
			},
		},
		{
			name: "3-cycle: a→b→c→a",
			nodes: []Node{
				{ID: "a", DependsOn: []string{"c"}},
				{ID: "b", DependsOn: []string{"a"}},
				{ID: "c", DependsOn: []string{"b"}},
			},
			want: nil,
			wantError: func(err error) bool {
				var ce *CycleError
				if !errors.As(err, &ce) {
					return false
				}
				// Cycle should start at lexically smallest and be in cycle order
				if len(ce.Cycle) != 3 {
					return false
				}
				return ce.Cycle[0] == "a" && ce.Cycle[1] == "b" && ce.Cycle[2] == "c"
			},
		},
		{
			name: "determinism: same input twice produces identical slices",
			nodes: []Node{
				{ID: "a", DependsOn: []string{}},
				{ID: "b", DependsOn: []string{"a"}},
				{ID: "c", DependsOn: []string{"a"}},
			},
			want:      []string{"a", "b", "c"},
			wantError: func(err error) bool { return err == nil },
		},
		{
			name: "multiple independent nodes lexical order",
			nodes: []Node{
				{ID: "c", DependsOn: []string{}},
				{ID: "a", DependsOn: []string{}},
				{ID: "b", DependsOn: []string{}},
			},
			want:      []string{"a", "b", "c"},
			wantError: func(err error) bool { return err == nil },
		},
		{
			name: "complex DAG with multiple levels",
			nodes: []Node{
				{ID: "a", DependsOn: []string{}},
				{ID: "b", DependsOn: []string{"a"}},
				{ID: "c", DependsOn: []string{"a"}},
				{ID: "d", DependsOn: []string{"b", "c"}},
				{ID: "e", DependsOn: []string{"d"}},
			},
			want:      []string{"a", "b", "c", "d", "e"},
			wantError: func(err error) bool { return err == nil },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Sort(tt.nodes)
			if !tt.wantError(err) {
				t.Errorf("Sort() error = %v", err)
				return
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Sort() got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeterminism(t *testing.T) {
	nodes := []Node{
		{ID: "c", DependsOn: []string{}},
		{ID: "b", DependsOn: []string{"a"}},
		{ID: "a", DependsOn: []string{"c"}},
	}

	result1, err1 := Sort(nodes)
	if err1 != nil {
		t.Fatalf("first call failed: %v", err1)
	}

	result2, err2 := Sort(nodes)
	if err2 != nil {
		t.Fatalf("second call failed: %v", err2)
	}

	if !slices.Equal(result1, result2) {
		t.Errorf("determinism failed: first call %v, second call %v", result1, result2)
	}
}
