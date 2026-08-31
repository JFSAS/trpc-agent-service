// Package deployment composes deployment revision and runtime publication capabilities.
package deployment

// Dependencies lists process-owned dependencies required by the deployment module.
type Dependencies struct{}

// Module is the assembled deployment module.
type Module struct{}

// NewModule assembles the deployment module without starting process resources.
func NewModule(Dependencies) *Module {
	return &Module{}
}
