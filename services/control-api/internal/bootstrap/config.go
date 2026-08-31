// Package bootstrap is the only process-level composition root for Control API.
package bootstrap

// Config will hold process configuration after the first vertical slice defines
// concrete HTTP, PostgreSQL, NATS, and telemetry requirements.
type Config struct{}

// LoadConfig loads process configuration. The scaffold has no configurable
// infrastructure yet, so it returns an empty configuration.
func LoadConfig() (Config, error) {
	return Config{}, nil
}
