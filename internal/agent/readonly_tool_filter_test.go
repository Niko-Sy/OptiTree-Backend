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

func TestFilterToolsForMode_DefaultExposesOnlyProductionHybrid(t *testing.T) {
	defs := graph_ops.FilterToolsForMode("faultTree", false, false)
	if len(defs) == 0 {
		t.Fatal("expected tool definitions")
	}

	hasProductionHybrid := false
	for _, def := range defs {
		if def.Tier != graph_ops.TierHybrid {
			continue
		}
		if !def.ProductionReady {
			t.Fatalf("default mode should not expose non-production hybrid tool: %s", def.Name)
		}
		if strings.EqualFold(def.Name, "suggest_batch_label_fix") {
			hasProductionHybrid = true
		}
	}

	if !hasProductionHybrid {
		t.Fatal("expected suggest_batch_label_fix to be exposed as production-ready hybrid tool")
	}
}
