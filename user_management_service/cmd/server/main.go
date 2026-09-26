package main

import (
	"log"
	"os"
	"strings"
	"user_management_service/internal/accounts"
	"user_management_service/internal/config"
	"user_management_service/internal/handler"
	"user_management_service/internal/mail"
	"user_management_service/internal/messaging"
	"user_management_service/internal/middleware"
	jwtutil "user_management_service/pkg/jwt"

	"github.com/gin-gonic/gin"
)

func main() {
	if err := jwtutil.ValidateConfiguration(); err != nil {
		log.Fatalf("JWT configuration: %v", err)
	}
	if len(os.Getenv("INTERNAL_AUTH_TOKEN")) < 32 {
		log.Fatal("INTERNAL_AUTH_TOKEN must contain at least 32 bytes")
	}

	// 1) Database setup
	db, err := config.SetupDatabase()
	if err != nil {
		log.Fatalf("Database setup: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("Database handle: %v", err)
	}
	defer sqlDB.Close()

	// Email is needed for student confirmation and instructor invitations.
	mailer, err := mail.SMTPFromEnvironment()
	if err != nil {
		log.Fatalf("SMTP configuration: %v", err)
	}
	appURL := strings.TrimSpace(os.Getenv("PUBLIC_APP_URL"))
	if appURL == "" {
		appURL = "http://localhost:3000"
	}
	svc := &accounts.Service{DB: db, AppURL: appURL}
	if mailer != nil {
		svc.Mailer = mailer
	} else {
		log.Println("SMTP_HOST is not set: student confirmation and instructor invitations are disabled")
	}

	// 2) RabbitMQ init (messaging.Init() δηλώνει exchanges & queues/bindings)
	messaging.Init()
	defer messaging.Conn.Close()
	defer messaging.Channel.Close()

	// 3) Start consumer για auth requests
	//    (δεν χρειάζεται να δίνουμε πια το Channel, το έχει ήδη global)
	messaging.ConsumeAuthQueue(svc)

	// 4) HTTP server (internal network only). The orchestrator is the public API:
	//    password login and registration arrive over RabbitMQ, never over HTTP.
	r := gin.Default()
	r.POST("/internal/google-login", handler.InternalGoogleLogin(svc))

	auth := r.Group("/auth")
	auth.Use(middleware.JWTAuthMiddleware()) // sets user_id, email, role in context
	auth.GET("/validate", handler.Validate())
	auth.GET("/profile", handler.Profile(db))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}

	log.Println("🟢 User-Management Service listening on port", port)
	r.Run(":" + port)
}
