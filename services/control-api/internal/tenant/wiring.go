// Package tenant composes tenant membership and authorization capabilities.
package tenant

// Dependencies lists process-owned dependencies required by the tenant module.
type Dependencies struct{}

// Module is the assembled tenant module.
type Module struct{}

// NewModule assembles the tenant module without starting process resources.
func NewModule(Dependencies) *Module {
	return &Module{}
}
