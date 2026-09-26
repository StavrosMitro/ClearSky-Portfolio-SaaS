package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestJWTSecretRejectsMissingOrShortValues(t *testing.T) {
	for _, secret := range []string{"", "too-short"} {
		t.Run(secret, func(t *testing.T) {
			t.Setenv("JWT_SECRET", secret)
			if _, err := JWTSecret(); err == nil {
				t.Fatal("JWTSecret() accepted an unsafe secret")
			}
		})
	}
}

func TestRabbitMQRequestTimeoutDefaultAndOverride(t *testing.T) {
	t.Setenv("RABBITMQ_REQUEST_TIMEOUT", "")
	if got, err := RabbitMQRequestTimeout(); err != nil || got != 10*time.Second {
		t.Fatalf("default timeout = %v, err=%v", got, err)
	}
	t.Setenv("RABBITMQ_REQUEST_TIMEOUT", "750ms")
	if got, err := RabbitMQRequestTimeout(); err != nil || got != 750*time.Millisecond {
		t.Fatalf("configured timeout = %v, err=%v", got, err)
	}
}

func TestRabbitMQRequestTimeoutRejectsUnsafeValues(t *testing.T) {
	for _, value := range []string{"not-a-duration", "50ms", "3m"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("RABBITMQ_REQUEST_TIMEOUT", value)
			if _, err := RabbitMQRequestTimeout(); err == nil {
				t.Fatalf("accepted timeout %q", value)
			}
		})
	}
}

func TestLoadFromEnvironmentOverridesYAMLRabbitMQURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	config := []byte("rabbitmq:\n  url: amqp://from-file\nexchange:\n  name: events\n  type: topic\nqueue:\n  name: orchestrator\n")
	if err := os.WriteFile(path, config, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_PATH", path)
	t.Setenv("AMQP_URL", "amqp://from-environment")
	if err := LoadFromEnvironment(); err != nil {
		t.Fatal(err)
	}
	if Cfg.RabbitMQ.URL != "amqp://from-environment" {
		t.Fatalf("RabbitMQ URL=%q", Cfg.RabbitMQ.URL)
	}
}

func TestJWTSecretAcceptsAtLeast32Bytes(t *testing.T) {
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	if _, err := JWTSecret(); err != nil {
		t.Fatalf("JWTSecret() returned an error: %v", err)
	}
}

func TestJWTIssuerAudienceDefaultsAndOverrides(t *testing.T) {
	t.Setenv("JWT_ISSUER", "")
	t.Setenv("JWT_AUDIENCE", "")
	issuer, audience, err := JWTIssuerAudience()
	if err != nil || issuer != "clearsky-identity" || audience != "clearsky-api" {
		t.Fatalf("defaults issuer=%q audience=%q err=%v", issuer, audience, err)
	}
	t.Setenv("JWT_ISSUER", "custom-issuer")
	t.Setenv("JWT_AUDIENCE", "custom-audience")
	issuer, audience, err = JWTIssuerAudience()
	if err != nil || issuer != "custom-issuer" || audience != "custom-audience" {
		t.Fatalf("overrides issuer=%q audience=%q err=%v", issuer, audience, err)
	}
}

func TestCORSAllowedOrigins(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.example.com, https://admin.example.com")

	got, err := CORSAllowedOrigins()
	if err != nil {
		t.Fatalf("CORSAllowedOrigins() returned an error: %v", err)
	}
	want := []string{"https://app.example.com", "https://admin.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CORSAllowedOrigins() = %v, want %v", got, want)
	}
}

func TestCORSAllowedOriginsRejectsWildcard(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "*")
	if _, err := CORSAllowedOrigins(); err == nil {
		t.Fatal("CORSAllowedOrigins() accepted wildcard credentials origin")
	}
}
