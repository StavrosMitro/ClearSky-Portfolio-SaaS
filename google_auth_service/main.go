package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"google_auth_service/handlers"
	"google_auth_service/policy"
	"google_auth_service/rabbitmq"

	"github.com/joho/godotenv"
)

func main() {
	// Load .env file if it exists
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	// Validate required environment variables
	requiredEnvVars := []string{
		"GOOGLE_CLIENT_ID",
		"GOOGLE_CLIENT_SECRET",
		"GOOGLE_REDIRECT_URL",
		"INTERNAL_AUTH_TOKEN",
	}

	for _, envVar := range requiredEnvVars {
		if os.Getenv(envVar) == "" {
			log.Fatalf("Required environment variable %s is not set", envVar)
		}
	}
	if len(os.Getenv("INTERNAL_AUTH_TOKEN")) < 32 {
		log.Fatal("INTERNAL_AUTH_TOKEN must contain at least 32 bytes")
	}
	accessPolicy, err := policy.FromEnvironment()
	if err != nil {
		log.Fatalf("Google access policy: %v", err)
	}
	log.Printf("Google login restricted to %v (Workspace required: %v)", accessPolicy.Domains, accessPolicy.RequireWorkspace)

	// Log loaded configuration (without secrets)
	log.Printf("Google Client ID: %s", os.Getenv("GOOGLE_CLIENT_ID"))
	log.Printf("Redirect URL: %s", os.Getenv("GOOGLE_REDIRECT_URL"))

	rabbitmq.Connect()
	rabbitmq.StartGoogleAuthConsumer()

	port := "8086"
	if os.Getenv("PORT") != "" {
		port = os.Getenv("PORT")
	}

	http.HandleFunc("/auth/google/login", handlers.GoogleLoginHandler)
	http.HandleFunc("/auth/google/callback", handlers.GoogleCallbackHandler)
	http.HandleFunc("/auth/logout", handlers.LogoutHandler)
	fmt.Printf("Starting google_auth_service on port %s...\n", port)
	fmt.Printf("Google OAuth redirect URL: %s\n", os.Getenv("GOOGLE_REDIRECT_URL"))
	server := &http.Server{Addr: ":" + port, Handler: http.DefaultServeMux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
