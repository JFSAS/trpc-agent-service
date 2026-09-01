// Package bootstrap is the only process-level composition root for Control API.
package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains the process configuration required by Control API V1.
type Config struct {
	HTTPAddress          string
	DatabaseURL          string
	SessionLifetime      time.Duration
	SessionCookieName    string
	SessionCookieDomain  string
	SessionCookieSecure  bool
	BootstrapMode        string
	BootstrapUsername    string
	BootstrapDisplayName string
	BootstrapPassword    string
	ShutdownTimeout      time.Duration
}

// LoadConfig reads and validates Control API process configuration.
func LoadConfig() (Config, error) {
	databaseURL := strings.TrimSpace(os.Getenv("CONTROL_DATABASE_URL"))
	if databaseURL == "" {
		return Config{}, errors.New("CONTROL_DATABASE_URL is required")
	}

	sessionLifetime, err := durationEnvironment("CONTROL_SESSION_LIFETIME", 24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	secure, err := boolEnvironment("CONTROL_SESSION_COOKIE_SECURE", true)
	if err != nil {
		return Config{}, err
	}
	bootstrapMode := strings.ToLower(stringEnvironment("CONTROL_BOOTSTRAP_MODE", "disabled"))
	if bootstrapMode != "disabled" && bootstrapMode != "auto" {
		return Config{}, errors.New("CONTROL_BOOTSTRAP_MODE must be disabled or auto")
	}
	bootstrapUsername := strings.TrimSpace(os.Getenv("CONTROL_BOOTSTRAP_USERNAME"))
	bootstrapPassword := os.Getenv("CONTROL_BOOTSTRAP_PASSWORD")
	if bootstrapMode == "auto" && (bootstrapUsername == "" || bootstrapPassword == "") {
		return Config{}, errors.New("CONTROL_BOOTSTRAP_USERNAME and CONTROL_BOOTSTRAP_PASSWORD are required in auto mode")
	}

	return Config{
		HTTPAddress:          stringEnvironment("CONTROL_HTTP_ADDRESS", ":8080"),
		DatabaseURL:          databaseURL,
		SessionLifetime:      sessionLifetime,
		SessionCookieName:    stringEnvironment("CONTROL_SESSION_COOKIE_NAME", "control_session"),
		SessionCookieDomain:  strings.TrimSpace(os.Getenv("CONTROL_SESSION_COOKIE_DOMAIN")),
		SessionCookieSecure:  secure,
		BootstrapMode:        bootstrapMode,
		BootstrapUsername:    bootstrapUsername,
		BootstrapDisplayName: stringEnvironment("CONTROL_BOOTSTRAP_DISPLAY_NAME", "Platform Operator"),
		BootstrapPassword:    bootstrapPassword,
		ShutdownTimeout:      10 * time.Second,
	}, nil
}

func stringEnvironment(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func durationEnvironment(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return parsed, nil
}

func boolEnvironment(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return parsed, nil
}
