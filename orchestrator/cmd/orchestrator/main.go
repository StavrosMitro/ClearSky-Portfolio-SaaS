package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"clearsky/contracts/obs"
	"clearsky/contracts/topology"
	"orchestrator/internal/clientip"
	"orchestrator/internal/config"
	"orchestrator/internal/rabbitmq"
	"orchestrator/internal/ratelimit"
	"orchestrator/internal/routes"
)

func main() {
	shutdownTracing, err := obs.Setup(context.Background(), "gateway")
	if err != nil {
		log.Fatalf("Observability: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(ctx)
	}()
	log.Println("Starting gateway (orchestrator)...")
	amqpURL, err := config.AMQPURL()
	if err != nil {
		log.Fatalf("RabbitMQ configuration: %v", err)
	}
	jwtKey, err := config.JWTSecret()
	if err != nil {
		log.Fatalf("JWT configuration: %v", err)
	}
	jwtIssuer, jwtAudience, err := config.JWTIssuerAudience()
	if err != nil {
		log.Fatalf("JWT configuration: %v", err)
	}
	allowedOrigins, err := config.CORSAllowedOrigins()
	if err != nil {
		log.Fatalf("CORS configuration: %v", err)
	}

	maxInFlight, err := config.MaxInFlightRPC()
	if err != nil {
		log.Fatalf("RabbitMQ configuration: %v", err)
	}
	requestTimeout, err := config.RabbitMQRequestTimeout()
	if err != nil {
		log.Fatalf("RabbitMQ configuration: %v", err)
	}
	client, err := rabbitmq.New(amqpURL, maxInFlight, requestTimeout)
	if err != nil {
		log.Fatalf("RabbitMQ connection failed: %v", err)
	}
	defer client.Close()

	// The services declare their own queues; the gateway only needs the
	// exchanges it publishes to.
	if err := topology.DeclareExchanges(client.TopologyChannel()); err != nil {
		log.Fatalf("Declare exchanges: %v", err)
	}

	authPerMinute, authBurst, err := config.AuthRateLimit()
	if err != nil {
		log.Fatalf("Rate limit configuration: %v", err)
	}
	proxies := clientip.New(config.TrustedProxies())
	refreshCtx, stopRefresh := context.WithCancel(context.Background())
	defer stopRefresh()
	go proxies.Run(refreshCtx, 30*time.Second)
	router := routes.SetupRouter(client, allowedOrigins, jwtKey, jwtIssuer, jwtAudience, routes.Options{
		AuthLimiter: ratelimit.New(authPerMinute, authBurst),
		ClientIP:    proxies.ClientIP,
	})

	server := &http.Server{
		Addr:              ":8080",
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Println("HTTP server running on :8080")
		serverErrors <- server.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case sig := <-signals:
		log.Printf("Received %s; shutting down", sig)
	case <-client.Unavailable():
		log.Printf("RabbitMQ client became unavailable; shutting down for supervisor restart")
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP server failed: %v", err)
		}
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP shutdown failed: %v", err)
	}
}
