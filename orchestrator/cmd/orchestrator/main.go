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

	"orchestrator/internal/config"
	"orchestrator/internal/rabbitmq"
	"orchestrator/internal/routes"
)

func main() {
	log.Println("Starting Orchestrator...")
	if err := config.LoadFromEnvironment(); err != nil {
		log.Fatalf("Load configuration: %v", err)
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
	client, err := rabbitmq.New(config.Cfg.RabbitMQ.URL, maxInFlight, requestTimeout)
	if err != nil {
		log.Fatalf("RabbitMQ connection failed: %v", err)
	}
	defer client.Close()

	if err := rabbitmq.SetupMessaging(client.TopologyChannel()); err != nil {
		log.Printf("SetupMessaging failed: %v", err)
		return
	}
	if err := rabbitmq.StartOrchestratorConsumer(client.EventChannel()); err != nil {
		log.Printf("Consumer failed: %v", err)
		return
	}

	log.Printf("Orchestrator listening on exchange '%s', queue '%s'...", config.Cfg.Exchange.Name, config.Cfg.Queue.Name)

	router := routes.SetupRouter(client, allowedOrigins, jwtKey, jwtIssuer, jwtAudience)

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
