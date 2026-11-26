package editor

import (
	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/workflow"
)

// ConnectionTarget describes a valid downstream node and binding expression.
type ConnectionTarget struct {
	NodeID     string `json:"node_id"`
	PortName   string `json:"port_name"`
	Expression string `json:"expression"`
}

// CatalogOperations lists enabled catalog operations for the visual node picker.
func CatalogOperations(reg *catalog.Registry) []catalog.OperationDescriptor {
	if reg == nil {
		return nil
	}
	return reg.Search("", false).Operations
}

// AvailableBindings returns typed output expressions produced by upstream nodes
// that can be wired into consumerNodeID.
func AvailableBindings(cw *workflow.CompiledWorkflow, consumerNodeID string) []ConnectionTarget {
	if cw == nil || cw.Definition == nil {
		return nil
	}
	var out []ConnectionTarget
	for _, id := range cw.Definition.NodeOrder {
		if id == consumerNodeID {
			continue
		}
		port := cw.Ports[id]
		for _, pName := range resolvePortNames(port) {
			expr := "${{ nodes." + id + ".outputs }}"
			if pName != "completion" {
				expr = "${{ nodes." + id + ".outputs." + pName + " }}"
			}
			out = append(out, ConnectionTarget{
				NodeID:     id,
				PortName:   pName,
				Expression: expr,
			})
		}
	}
	return out
}
