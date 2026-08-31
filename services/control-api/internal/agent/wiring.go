// Package agent composes Agent authoring, validation, and versioning capabilities.
package agent

// Dependencies lists process-owned dependencies required by the Agent module.
type Dependencies struct{}

// Module is the assembled Agent module.
type Module struct{}

// NewModule assembles the Agent module without starting process resources.
func NewModule(Dependencies) *Module {
	return &Module{}
}
