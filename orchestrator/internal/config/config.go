package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// AMQPURL is the broker address (required).
func AMQPURL() (string, error) {
	url := strings.TrimSpace(os.Getenv("AMQP_URL"))
	if url == "" {
		return "", fmt.Errorf("AMQP_URL must be set")
	}
	return url, nil
}

func JWTSecret() ([]byte, error) {
	secret := os.Getenv("JWT_SECRET")
	if len(secret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must contain at least 32 bytes")
	}
	return []byte(secret), nil
}

func JWTIssuerAudience() (string, string, error) {
	issuer := strings.TrimSpace(os.Getenv("JWT_ISSUER"))
	audience := strings.TrimSpace(os.Getenv("JWT_AUDIENCE"))
	if issuer == "" {
		issuer = "clearsky-identity"
	}
	if audience == "" {
		audience = "clearsky-api"
	}
	return issuer, audience, nil
}

func CORSAllowedOrigins() ([]string, error) {
	raw := os.Getenv("CORS_ALLOWED_ORIGINS")
	if raw == "" {
		raw = "http://localhost:3000"
	}

	origins := make([]string, 0)
	for _, origin := range strings.Split(raw, ",") {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		if origin == "*" {
			return nil, fmt.Errorf("CORS_ALLOWED_ORIGINS cannot contain '*' when credentials are enabled")
		}
		origins = append(origins, origin)
	}
	if len(origins) == 0 {
		return nil, fmt.Errorf("CORS_ALLOWED_ORIGINS must contain at least one origin")
	}
	return origins, nil
}

func MaxInFlightRPC() (int, error) {
	raw := os.Getenv("RABBITMQ_MAX_IN_FLIGHT")
	if raw == "" {
		return 64, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 4096 {
		return 0, fmt.Errorf("RABBITMQ_MAX_IN_FLIGHT must be an integer from 1 to 4096")
	}
	return value, nil
}

func RabbitMQRequestTimeout() (time.Duration, error) {
	raw := os.Getenv("RABBITMQ_REQUEST_TIMEOUT")
	if raw == "" {
		return 10 * time.Second, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < 100*time.Millisecond || value > 2*time.Minute {
		return 0, fmt.Errorf("RABBITMQ_REQUEST_TIMEOUT must be a duration from 100ms to 2m")
	}
	return value, nil
}

// AuthRateLimit returns the per-client limit for authentication endpoints.
// Defaults are generous because a campus network puts many students behind
// few public addresses; per-account backoff in user management is the main
// protection against password guessing.
func AuthRateLimit() (perMinute, burst int, err error) {
	perMinute, err = positiveInt("AUTH_RATE_LIMIT_PER_MINUTE", 60)
	if err != nil {
		return 0, 0, err
	}
	burst, err = positiveInt("AUTH_RATE_LIMIT_BURST", 20)
	return perMinute, burst, err
}

// TrustedProxies lists the proxies whose X-Forwarded-For is believed: IPs,
// CIDRs or hostnames (resolved periodically). Empty means none: the client
// address is the TCP peer.
func TrustedProxies() []string {
	var proxies []string
	for _, value := range strings.Split(os.Getenv("TRUSTED_PROXIES"), ",") {
		if value = strings.TrimSpace(value); value != "" {
			proxies = append(proxies, value)
		}
	}
	return proxies
}

func positiveInt(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 100000 {
		return 0, fmt.Errorf("%s must be an integer from 1 to 100000", name)
	}
	return value, nil
}
