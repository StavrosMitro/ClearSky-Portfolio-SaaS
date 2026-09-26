package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"google_auth_service/policy"

	amqp "github.com/rabbitmq/amqp091-go"
)

type GoogleAuthRequest struct {
	Token string `json:"token"`
}

type verifiedIdentity struct {
	Email string `json:"email"`
}

type rpcError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type rpcEnvelope struct {
	Version int       `json:"version"`
	Data    any       `json:"data,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

func StartGoogleAuthConsumer() {
	brokerURL := os.Getenv("RABBITMQ_URL")
	if brokerURL == "" {
		brokerURL = "amqp://guest:guest@rabbitmq:5672/"
	}
	conn, err := amqp.Dial(brokerURL)
	if err != nil {
		log.Fatal("RabbitMQ connection failed:", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		log.Fatal("RabbitMQ channel failed:", err)
	}

	if err = ch.ExchangeDeclare("clearsky.commands.v1", "direct", true, false, false, false, nil); err != nil {
		log.Fatal("Exchange declare failed:", err)
	}
	if err = ch.ExchangeDeclare("clearsky.dlx.v1", "direct", true, false, false, false, nil); err != nil {
		log.Fatal("DLX declare failed:", err)
	}
	queue := "clearsky.google-auth.commands.v1"
	args := amqp.Table{"x-dead-letter-exchange": "clearsky.dlx.v1", "x-dead-letter-routing-key": queue + ".dead"}
	if _, err = ch.QueueDeclare(queue, true, false, false, false, args); err != nil {
		log.Fatal("Queue declare failed:", err)
	}
	if _, err = ch.QueueDeclare(queue+".dlq", true, false, false, false, nil); err != nil {
		log.Fatal("DLQ declare failed:", err)
	}
	if err = ch.QueueBind(queue+".dlq", queue+".dead", "clearsky.dlx.v1", false, nil); err != nil {
		log.Fatal("DLQ bind failed:", err)
	}
	if err = ch.QueueBind(queue, "auth.login.google", "clearsky.commands.v1", false, nil); err != nil {
		log.Fatal("Queue bind failed:", err)
	}
	msgs, err := ch.Consume(queue, "", false, false, false, false, nil)
	if err != nil {
		log.Fatal("Consume failed:", err)
	}

	go func() {
		for d := range msgs {
			response := rpcEnvelope{}
			var req GoogleAuthRequest
			if err := json.Unmarshal(d.Body, &req); err != nil || req.Token == "" {
				response = rpcEnvelope{Version: 1, Error: &rpcError{Code: "INVALID_REQUEST", Message: "A Google ID token is required"}}
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				email, hostedDomain, err := verifyGoogleToken(ctx, req.Token)
				cancel()
				accessPolicy, policyErr := policy.FromEnvironment()
				switch {
				case err != nil:
					response = rpcEnvelope{Version: 1, Error: &rpcError{Code: "UNAUTHENTICATED", Message: "Invalid Google token"}}
				case policyErr != nil:
					log.Printf("Google access policy: %v", policyErr)
					response = rpcEnvelope{Version: 1, Error: &rpcError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Google login is unavailable", Retryable: true}}
				case !accessPolicy.Allows(email, hostedDomain):
					response = rpcEnvelope{Version: 1, Error: &rpcError{Code: "FORBIDDEN", Message: "Google account is not authorized"}}
				default:
					response = rpcEnvelope{Version: 1, Data: verifiedIdentity{Email: email}}
				}
			}

			body, err := json.Marshal(response)
			if err != nil || d.ReplyTo == "" {
				_ = d.Nack(false, false)
				continue
			}
			if err := ch.Publish("", d.ReplyTo, false, false, amqp.Publishing{ContentType: "application/json", CorrelationId: d.CorrelationId, Body: body}); err != nil {
				log.Printf("publish Google auth reply: %v", err)
				_ = d.Nack(false, true)
				continue
			}
			_ = d.Ack(false)
		}
	}()
}

func verifyGoogleToken(ctx context.Context, idToken string) (string, string, error) {
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	if clientID == "" {
		return "", "", errors.New("GOOGLE_CLIENT_ID is not configured")
	}
	endpoint := "https://oauth2.googleapis.com/tokeninfo?id_token=" + url.QueryEscape(idToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("tokeninfo status %d", resp.StatusCode)
	}
	var info struct {
		Audience      string `json:"aud"`
		Issuer        string `json:"iss"`
		Email         string `json:"email"`
		EmailVerified string `json:"email_verified"`
		HostedDomain  string `json:"hd"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", "", err
	}
	validIssuer := info.Issuer == "accounts.google.com" || info.Issuer == "https://accounts.google.com"
	if info.Audience != clientID || !validIssuer || info.Email == "" || info.EmailVerified != "true" {
		return "", "", errors.New("Google token claims are invalid")
	}
	return info.Email, info.HostedDomain, nil
}
