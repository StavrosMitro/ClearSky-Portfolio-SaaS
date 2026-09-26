package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	RabbitMQ struct {
		URL string `yaml:"url"`
	} `yaml:"rabbitmq"`
	Exchange struct {
		Name string `yaml:"name"`
		Type string `yaml:"type"`
	} `yaml:"exchange"`
	Queue struct {
		Name string `yaml:"name"`
		DLX  string `yaml:"dlx"`
	} `yaml:"queue"`
	Bindings []string `yaml:"bindings"`
}

var Cfg Config

func LoadConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %q: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse config %q: %w", path, err)
	}
	if cfg.RabbitMQ.URL == "" || cfg.Exchange.Name == "" || cfg.Exchange.Type == "" || cfg.Queue.Name == "" {
		return fmt.Errorf("config %q is missing required RabbitMQ, exchange, or queue values", path)
	}
	Cfg = cfg
	return nil
}

func LoadFromEnvironment() error {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "configs/config.dev.yaml"
	}
	if err := LoadConfig(cfgPath); err != nil {
		return err
	}
	if url := os.Getenv("AMQP_URL"); url != "" {
		Cfg.RabbitMQ.URL = url
	}
	return nil
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
