// orchestrator/internal/routes/routes.go
package routes

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"orchestrator/internal/api"
	"orchestrator/internal/handlers"
	mw "orchestrator/internal/middleware"
	"orchestrator/internal/ratelimit"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

// Options holds the router settings that are not part of JWT or CORS.
type Options struct {
	// AuthLimiter limits authentication endpoints per client; nil disables it.
	AuthLimiter *ratelimit.Limiter
	// ClientIP identifies the client (trusted proxies); nil uses the TCP peer.
	ClientIP func(*http.Request) string
}

const (
	roleStudent        = "student"
	roleInstructor     = "instructor"
	roleRepresentative = "institution_representative"
)

// SetupRouter configures all HTTP endpoints and returns the Gin engine.
func SetupRouter(m handlers.Messenger, allowedOrigins []string, jwtKey []byte, jwtIssuer, jwtAudience string, opts Options) *gin.Engine {
	r := gin.New()
	// Gin never trusts forwarding headers; ClientIP below decides.
	_ = r.SetTrustedProxies(nil)
	clientIP := opts.ClientIP
	if clientIP == nil {
		clientIP = func(req *http.Request) string {
			host, _, _ := splitHostPort(req.RemoteAddr)
			return host
		}
	}
	r.Use(gin.Recovery())
	r.Use(otelgin.Middleware("gateway"))
	r.Use(api.RequestIDMiddleware())
	r.Use(requestLog(clientIP))
	r.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "Idempotency-Key"},
		ExposeHeaders:    []string{"Content-Length", "X-Request-ID", "Retry-After"},
		AllowCredentials: true,
	}))
	r.Use(requestBodyLimit(20 << 20))
	r.Use(securityHeaders())
	r.MaxMultipartMemory = 16 << 20

	r.GET("/health/live", func(c *gin.Context) { api.Success(c, http.StatusOK, gin.H{"status": "live"}) })
	r.GET("/health/ready", func(c *gin.Context) {
		if m == nil || !m.Ready() {
			api.Failure(c, http.StatusServiceUnavailable, api.CodeServiceUnavailable, "RabbitMQ is not ready", nil)
			return
		}
		api.Success(c, http.StatusOK, gin.H{"status": "ready"})
	})

	// ── Public endpoints (no JWT) ───────────────────────────────────────────
	authLimit := func(c *gin.Context) { c.Next() }
	if opts.AuthLimiter != nil {
		authLimit = ratelimit.Middleware(opts.AuthLimiter, "auth", clientIP)
	}
	with := func(h func(*gin.Context, handlers.Messenger)) gin.HandlerFunc {
		return func(c *gin.Context) { h(c, m) }
	}
	r.POST("/user/register", authLimit, with(handlers.HandleStudentRegistration))
	r.POST("/user/activate", authLimit, with(handlers.HandleAccountActivation))
	r.POST("/user/google-signup", authLimit, with(handlers.HandleGoogleSignup))
	r.POST("/user/login", authLimit, with(handlers.HandleUserLogin))
	r.POST("/user/google-login", authLimit, with(handlers.HandleUserGoogleLogin))
	r.POST("/user/forgot-password", authLimit, with(handlers.HandleForgotPassword))
	r.POST("/user/logout", handlers.HandleUserLogout)
	r.GET("/institutions", with(handlers.HandleListInstitutions))

	authenticated := mw.JWTAuthMiddleware(jwtKey, jwtIssuer, jwtAudience)
	only := func(roles ...string) gin.HandlerFunc {
		return func(c *gin.Context) {
			for _, role := range roles {
				if c.GetString("role") == role {
					c.Next()
					return
				}
			}
			api.Abort(c, http.StatusForbidden, api.CodeForbidden, "Your role cannot use this endpoint")
		}
	}

	// ── Every signed-in user ────────────────────────────────────────────────
	account := r.Group("/user", authenticated)
	account.PATCH("/change-password", with(handlers.HandleUserChangePassword))
	account.GET("/me", func(c *gin.Context) {
		api.Success(c, http.StatusOK, gin.H{"user_id": mw.GetUserID(c), "institution_id": mw.GetInstitutionID(c),
			"username": mw.GetUsername(c), "role": mw.GetRole(c), "student_id": mw.GetStudentID(c)})
	})
	stats := r.Group("/stats", authenticated)
	stats.GET("/available", with(handlers.HandleVisibleGradings))
	stats.GET("/gradings/:id/distributions", with(handlers.HandleDistributions))

	// ── Institution representatives (secretariat) ───────────────────────────
	secretariat := r.Group("/", authenticated, only(roleRepresentative))
	secretariat.POST("/registration", with(handlers.HandleRegisterInstitution))
	secretariat.GET("/institution", with(handlers.HandleMyInstitution))
	secretariat.GET("/mycredits", with(handlers.HandleMyInstitution))
	secretariat.PATCH("/purchase", with(handlers.HandlePurchaseCredits))
	secretariat.GET("/institution/credit-history", with(handlers.HandleCreditHistory))
	secretariat.POST("/institution/instructors", with(handlers.HandleCreateInstructor))
	secretariat.POST("/institution/student-roster", with(handlers.HandleStudentRosterUpload))

	// ── Students ────────────────────────────────────────────────────────────
	students := r.Group("/", authenticated, only(roleStudent), mw.RequireStudentID())
	students.GET("/personal/grades", with(handlers.HandlePersonalGrades))
	students.POST("/reviews", with(handlers.HandleCreateReview))
	students.GET("/reviews/mine", with(handlers.HandleMyReviews))

	// ── Instructors ─────────────────────────────────────────────────────────
	instructors := r.Group("/", authenticated, only(roleInstructor))
	instructors.POST("/grades/uploads", with(handlers.HandleGradesUpload))
	instructors.POST("/grades/uploads/:id/confirm", with(handlers.HandleGradesConfirm))
	instructors.POST("/grades/uploads/:id/cancel", with(handlers.HandleGradesCancel))
	instructors.GET("/reviews/inbox", with(handlers.HandleReviewInbox))
	instructors.GET("/reviews/:id", with(handlers.HandleReviewGet))
	instructors.POST("/reviews/:id/reply", with(handlers.HandleReviewReply))

	return r
}

// requestLog writes one JSON line per request (trace IDs are added by the
// slog handler from the request context).
func requestLog(clientIP func(*http.Request) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if strings.HasPrefix(route, "/health/") {
			return // polled every few seconds by the container runtime
		}
		if route == "" {
			route = "unmatched"
		}
		slog.InfoContext(c.Request.Context(), "http request",
			"method", c.Request.Method, "route", route, "status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(), "request_id", api.RequestID(c), "client_ip", clientIP(c.Request))
	}
}

func requestBodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Next()
	}
}
