package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"user_management_service/internal/accounts"
	"user_management_service/internal/model"
	"user_management_service/pkg/jwt"

	amqp "github.com/rabbitmq/amqp091-go"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const rpcVersion = 1

type AuthRequest struct {
	Type         string `json:"type"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	StudentID    string `json:"student_id,omitempty"`
	Email        string `json:"email,omitempty"`
	Token        string `json:"token,omitempty"`
	SignupTicket string `json:"signup_ticket,omitempty"`
	CSV          string `json:"csv,omitempty"`
	ActorToken   string `json:"actor_token,omitempty"`
	OldPassword  string `json:"old_password,omitempty"`
	NewPassword  string `json:"new_password,omitempty"`
}

// emailTimeout stays below the orchestrator's default 10s RPC deadline.
const emailTimeout = 8 * time.Second

type AuthResult struct {
	Token  string `json:"token,omitempty"`
	Role   string `json:"role,omitempty"`
	UserID string `json:"user_id,omitempty"`
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

func rpcSuccess(data any) rpcEnvelope {
	return rpcEnvelope{Version: rpcVersion, Data: data}
}

func rpcFailure(code, message string, retryable bool) rpcEnvelope {
	return rpcEnvelope{Version: rpcVersion, Error: &rpcError{Code: code, Message: message, Retryable: retryable}}
}

func handleAuthRequest(svc *accounts.Service, req AuthRequest) rpcEnvelope {
	db := svc.DB
	switch req.Type {
	case "request_student_activation":
		ctx, cancel := context.WithTimeout(context.Background(), emailTimeout)
		defer cancel()
		return fromResult(svc.RequestStudentActivation(ctx, req.StudentID, req.Email))

	case "complete_activation":
		return fromResult(svc.CompleteActivation(req.Token, req.Password))

	case "complete_google_signup":
		user, err := svc.CompleteGoogleSignup(req.SignupTicket, req.StudentID)
		if err != nil {
			return fromError(err)
		}
		return issueToken(user)

	case "create_instructor":
		if _, err := svc.AuthorizeRepresentative(req.ActorToken); err != nil {
			return fromError(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), emailTimeout)
		defer cancel()
		return fromResult(svc.CreateInstructor(ctx, req.Email))

	case "import_student_roster":
		actorID, err := svc.AuthorizeRepresentative(req.ActorToken)
		if err != nil {
			return fromError(err)
		}
		return fromResult(svc.ImportRoster(req.CSV, actorID))

	case "login":
		if req.Username == "" || req.Password == "" {
			return rpcFailure("INVALID_REQUEST", "Username and password are required", false)
		}
		user, found := findLoginUser(db, req.Username)
		// Accounts awaiting their emailed password link have no password yet.
		if !found || user.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
			return rpcFailure("INVALID_CREDENTIALS", "Invalid credentials", false)
		}
		return issueToken(user)

	case "google_login":
		if req.Username == "" {
			return rpcFailure("INVALID_REQUEST", "Verified Google email is required", false)
		}
		var user model.User
		if err := db.Where("username = ?", req.Username).First(&user).Error; err != nil {
			return rpcFailure("FORBIDDEN", "This Google account has not been provisioned", false)
		}
		return issueToken(user)

	case "change_password":
		if req.Username == "" || req.OldPassword == "" || len(req.NewPassword) < 8 {
			return rpcFailure("INVALID_REQUEST", "Old password and a new password of at least 8 characters are required", false)
		}
		var user model.User
		if err := db.Where("username = ?", req.Username).First(&user).Error; err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.OldPassword)) != nil {
			return rpcFailure("INVALID_CREDENTIALS", "Invalid credentials", false)
		}
		newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			return rpcFailure("INTERNAL_ERROR", "Could not secure the password", false)
		}
		if err := db.Model(&user).Update("password_hash", string(newHash)).Error; err != nil {
			return rpcFailure("DEPENDENCY_UNAVAILABLE", "User store is unavailable", true)
		}
		return rpcSuccess(map[string]string{"message": "Password changed"})

	default:
		return rpcFailure("INVALID_REQUEST", "Unknown authentication request type", false)
	}
}

// findLoginUser matches the username exactly, then as a lower-case email,
// because new accounts store university emails in lower case.
func findLoginUser(db *gorm.DB, username string) (model.User, bool) {
	username = strings.TrimSpace(username)
	candidates := []string{username}
	if lower := strings.ToLower(username); lower != username && strings.Contains(username, "@") {
		candidates = append(candidates, lower)
	}
	for _, candidate := range candidates {
		var users []model.User
		if err := db.Where("username = ?", candidate).Limit(1).Find(&users).Error; err == nil && len(users) > 0 {
			return users[0], true
		}
	}
	return model.User{}, false
}

func fromError(err error) rpcEnvelope {
	var accountErr *accounts.Error
	if errors.As(err, &accountErr) {
		return rpcFailure(accountErr.Code, accountErr.Message, accountErr.Retryable)
	}
	log.Printf("account operation failed: %v", err)
	return rpcFailure("INTERNAL_ERROR", "The operation failed", false)
}

func fromResult[T any](data T, err error) rpcEnvelope {
	if err != nil {
		return fromError(err)
	}
	return rpcSuccess(data)
}

func issueToken(user model.User) rpcEnvelope {
	token, err := jwt.GenerateToken(user.ID, user.Username, user.Role, user.StudentID)
	if err != nil {
		log.Printf("generate application token: %v", err)
		return rpcFailure("INTERNAL_ERROR", "Could not create a session", false)
	}
	return rpcSuccess(AuthResult{Token: token, Role: user.Role, UserID: user.ID})
}

func reply(d amqp.Delivery, response rpcEnvelope) error {
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if d.ReplyTo == "" {
		return errors.New("RPC request has no reply queue")
	}
	return Channel.Publish("", d.ReplyTo, false, false, amqp.Publishing{
		ContentType:   "application/json",
		CorrelationId: d.CorrelationId,
		Body:          body,
	})
}

func ConsumeAuthQueue(svc *accounts.Service) {
	msgs, err := Channel.Consume("clearsky.auth.commands.v1", "", false, false, false, false, nil)
	if err != nil {
		log.Fatalf("Consume auth command queue: %v", err)
	}

	go func() {
		for d := range msgs {
			var req AuthRequest
			response := rpcEnvelope{}
			if err := json.Unmarshal(d.Body, &req); err != nil {
				response = rpcFailure("INVALID_REQUEST", "Invalid authentication request", false)
			} else {
				response = handleAuthRequest(svc, req)
			}
			if err := reply(d, response); err != nil {
				log.Printf("publish auth RPC reply: %v", err)
				_ = d.Nack(false, true)
				continue
			}
			if err := d.Ack(false); err != nil {
				log.Printf("ack auth command: %v", err)
			}
		}
	}()
}
