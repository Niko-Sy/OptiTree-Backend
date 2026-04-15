package agent

import (
	"strings"
	"testing"

	"optitree-backend/internal/graph_ops"
)

func TestFilterToolsForMode_ReadOnlyExcludesMutationAndHybrid(t *testing.T) {
	defs := graph_ops.FilterToolsForMode("faultTree", true, false)
	if len(defs) == 0 {
		t.Fatal("expected read-only tool definitions")
	}

	for _, def := range defs {
		if def.MutatesGraph {
			t.Fatalf("read-only mode should not expose mutating tool: %s", def.Name)
		}
		if def.Tier == graph_ops.TierHybrid {
			t.Fatalf("read-only mode should not expose hybrid tool: %s", def.Name)
		}
		if strings.EqualFold(def.Name, "annotate_node") || strings.EqualFold(def.Name, "preview_layout") {
			t.Fatalf("read-only mode should not expose client-side side-effect tool: %s", def.Name)
		}
	}
}

func TestFilterToolsForMode_DefaultHidesHybridTools(t *testing.T) {
	defs := graph_ops.FilterToolsForMode("faultTree", false, false)
	if len(defs) == 0 {
		t.Fatal("expected tool definitions")
	}

	for _, def := range defs {
		if def.Tier == graph_ops.TierHybrid {
			t.Fatalf("default mode should not expose hybrid tool: %s", def.Name)
		}
	}
}

func TestFilterToolsForMode_CanIncludeHybridToolsExplicitly(t *testing.T) {
	defs := graph_ops.FilterToolsForMode("faultTree", false, true)
	hasHybrid := false
	for _, def := range defs {
		if def.Tier == graph_ops.TierHybrid {
			hasHybrid = true
			break
		}
	}
	if !hasHybrid {
		t.Fatal("expected hybrid tools when includeHybridTools=true")
	}
}
