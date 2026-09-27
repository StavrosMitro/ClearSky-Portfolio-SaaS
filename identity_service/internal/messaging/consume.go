// Package messaging serves identity's RabbitMQ RPC operations (auth.request).
package messaging

import (
	"context"
	"encoding/json"
	"log/slog"

	"identity_service/internal/accounts"
	"identity_service/internal/google"
	"identity_service/internal/model"
	"identity_service/pkg/jwt"

	"clearsky/contracts/amqpx"
	"clearsky/contracts/rpc"
)

type AuthRequest struct {
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	StudentID    string `json:"student_id,omitempty"`
	Email        string `json:"email,omitempty"`
	Token        string `json:"token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	SignupTicket string `json:"signup_ticket,omitempty"`
	CSV          string `json:"csv,omitempty"`
	ActorToken   string `json:"actor_token,omitempty"`
	OldPassword  string `json:"old_password,omitempty"`
	NewPassword  string `json:"new_password,omitempty"`
}

// AuthResult is a signed-in session (the gateway turns Token into a cookie).
type AuthResult struct {
	Token         string `json:"token,omitempty"`
	Role          string `json:"role,omitempty"`
	UserID        string `json:"user_id,omitempty"`
	InstitutionID string `json:"institution_id,omitempty"`
	SignupTicket  string `json:"signup_ticket,omitempty"`
}

func issueToken(user model.User) (AuthResult, error) {
	token, err := jwt.GenerateToken(user.ID, user.InstitutionID, user.Username, user.Role, user.StudentID)
	if err != nil {
		slog.Error("generate application token", "error", err)
		return AuthResult{}, rpc.Fail(rpc.CodeInternal, "Could not create a session")
	}
	return AuthResult{Token: token, Role: user.Role, UserID: user.ID, InstitutionID: user.InstitutionID}, nil
}

// Handler returns the auth.request handler.
func Handler(svc *accounts.Service) amqpx.Handler {
	return func(ctx context.Context, msgType string, body json.RawMessage) (any, error) {
		var req AuthRequest
		if err := rpc.Bind(body, &req); err != nil {
			return nil, err
		}
		switch msgType {
		case "login":
			user, err := svc.Authenticate(req.Username, req.Password)
			if err != nil {
				return nil, err
			}
			return issueToken(user)

		case "change_password":
			if err := svc.ChangePassword(req.Username, req.OldPassword, req.NewPassword); err != nil {
				return nil, err
			}
			return map[string]string{"message": "Password changed"}, nil

		case "request_password_reset":
			return svc.RequestPasswordReset(ctx, req.Email)

		case "google_token_login":
			// The ID token is verified here; nothing on the broker is trusted.
			identity, err := google.VerifyIDToken(ctx, req.IDToken)
			if err != nil {
				return nil, rpc.Fail(rpc.CodeUnauthenticated, "Invalid Google token")
			}
			policy, err := google.FromEnvironment()
			if err != nil {
				return nil, rpc.Unavailable("Google login is unavailable")
			}
			if !policy.Allows(identity.Email, identity.HostedDomain) {
				return nil, rpc.Fail(rpc.CodeForbidden, "Sign in with your university Google account")
			}
			result, err := svc.GoogleSignIn(identity.Email, identity.Subject)
			if err != nil {
				return nil, err
			}
			if result.SignupTicket != "" {
				return AuthResult{SignupTicket: result.SignupTicket}, nil
			}
			return issueToken(*result.User)

		case "request_student_activation":
			return svc.RequestStudentActivation(ctx, req.StudentID, req.Email)

		case "complete_activation":
			return svc.CompleteActivation(req.Token, req.Password)

		case "complete_google_signup":
			user, err := svc.CompleteGoogleSignup(req.SignupTicket, req.StudentID)
			if err != nil {
				return nil, err
			}
			return issueToken(user)

		case "create_instructor":
			_, institutionID, err := svc.AuthorizeRepresentative(req.ActorToken)
			if err != nil {
				return nil, err
			}
			return svc.CreateInstructor(ctx, institutionID, req.Email)

		case "import_student_roster":
			actorID, institutionID, err := svc.AuthorizeRepresentative(req.ActorToken)
			if err != nil {
				return nil, err
			}
			return svc.ImportRoster(req.CSV, actorID, institutionID)

		default:
			return nil, rpc.Fail(rpc.CodeInvalidRequest, "Unknown authentication request type")
		}
	}
}
