// Package channelbinding composes channel-to-deployment binding capabilities.
package channelbinding

// Dependencies lists process-owned dependencies required by the channel binding module.
type Dependencies struct{}

// Module is the assembled channel binding module.
type Module struct{}

// NewModule assembles the channel binding module without starting process resources.
func NewModule(Dependencies) *Module {
	return &Module{}
}
