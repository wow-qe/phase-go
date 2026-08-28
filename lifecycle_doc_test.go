// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// The ARCHITECTURE tables render FROM the code's own transition and
// capability tables — a failing test cannot drift the way an un-run
// generator can (the TestSchemaSurfaceIsFullyTagged doctrine, generalized).

func renderCapabilityTable() string {
	order := []stageKind{stageExec, stageWhen, stageGroupSetup, stageGroupTeardown,
		stageFixtureSetup, stageFixtureTeardown, stageSession}
	var b strings.Builder
	b.WriteString("| Stage | Record | Observe | Put | Get | PriorEvidence |\n|---|---|---|---|---|---|\n")
	mark := func(c capability, has capability) string {
		if c&has != 0 {
			return "yes"
		}
		return "—"
	}
	for _, st := range order {
		c := stageCaps[st]
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", st,
			mark(c, capRecord), mark(c, capObserve), mark(c, capPut), mark(c, capGet), mark(c, capPriorEvidence))
	}
	return b.String()
}

func renderGroupLifecycle() string {
	states := []groupState{groupPending, groupSettingUp, groupActive, groupSetupFailed, groupTearingDown}
	var lines []string
	for _, from := range states {
		tos := append([]groupState(nil), groupTransitions[from]...)
		sort.Slice(tos, func(i, j int) bool { return tos[i] < tos[j] })
		var names []string
		for _, to := range tos {
			names = append(names, to.String())
		}
		lines = append(lines, fmt.Sprintf("- %s → %s", from, strings.Join(names, " | ")))
	}
	return strings.Join(lines, "\n") + "\n"
}

func renderEventCatalog() string {
	var names []string
	for k := SessionStarted; k < numEventKinds; k++ {
		names = append(names, k.String())
	}
	return strings.Join(names, ", ") + "\n"
}

func extractSection(t *testing.T, doc, name string) string {
	t.Helper()
	begin := "<!-- generated:" + name + " begin -->\n"
	end := "<!-- generated:" + name + " end -->"
	i := strings.Index(doc, begin)
	j := strings.Index(doc, end)
	if i < 0 || j < 0 || j < i {
		t.Fatalf("ARCHITECTURE.md is missing the generated %q section markers", name)
	}
	return doc[i+len(begin) : j]
}

func TestArchitectureTablesMatchTheCode(t *testing.T) {
	raw, err := os.ReadFile("docs/ARCHITECTURE.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for name, want := range map[string]string{
		"capabilities":    renderCapabilityTable(),
		"group-lifecycle": renderGroupLifecycle(),
		"events":          renderEventCatalog(),
	} {
		if got := extractSection(t, doc, name); got != want {
			t.Errorf("ARCHITECTURE.md %q section drifted from the code.\n--- doc ---\n%s\n--- code ---\n%s", name, got, want)
		}
	}
}
