// orchestrator/internal/routes/routes.go
package routes

import (
	"net/http"
	"orchestrator/internal/api"
	"orchestrator/internal/handlers"
	mw "orchestrator/internal/middleware"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// SetupRouter configures all HTTP endpoints and returns the Gin engine.
func SetupRouter(m handlers.Messenger, allowedOrigins []string, jwtKey []byte, jwtIssuer, jwtAudience string) *gin.Engine {
	r := gin.Default()
	r.Use(api.RequestIDMiddleware())

	r.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length", "X-Request-ID"},
		AllowCredentials: true,
	}))
	r.Use(requestBodyLimit(20 << 20))
	r.Use(securityHeaders())

	r.MaxMultipartMemory = 16 << 20 // 16 MiB
	r.GET("/health/live", func(c *gin.Context) { api.Success(c, http.StatusOK, gin.H{"status": "live"}) })
	r.GET("/health/ready", func(c *gin.Context) {
		if m == nil || !m.Ready() {
			api.Failure(c, http.StatusServiceUnavailable, api.CodeServiceUnavailable, "RabbitMQ is not ready", nil)
			return
		}
		api.Success(c, http.StatusOK, gin.H{"status": "ready"})
	})

	// ────────────────────────────────────────────────────────────────────────
	//  Public endpoints (no JWT)
	// ────────────────────────────────────────────────────────────────────────
	{
		r.POST("/user/register", func(c *gin.Context) { handlers.HandleStudentRegistration(c, m) })
		r.POST("/user/activate", func(c *gin.Context) { handlers.HandleAccountActivation(c, m) })
		r.POST("/user/google-signup", func(c *gin.Context) { handlers.HandleGoogleSignup(c, m) })
		r.POST("/user/login", func(c *gin.Context) { handlers.HandleUserLogin(c, m) })
		r.POST("/user/google-login", func(c *gin.Context) { handlers.HandleUserGoogleLogin(c, m) })
		r.POST("/user/logout", handlers.HandleUserLogout)
		r.GET("/institutions", func(c *gin.Context) {
			handlers.GetInstitutions(c)
		})
		// NEW: purchase credits endpoint
		// front-end does: PATCH /purchase { name, amount }

	}
	account := r.Group("/user")
	account.Use(mw.JWTAuthMiddleware(jwtKey, jwtIssuer, jwtAudience))
	account.PATCH("/change-password", func(c *gin.Context) { handlers.HandleUserChangePassword(c, m) })
	account.GET("/me", func(c *gin.Context) {
		api.Success(c, http.StatusOK, gin.H{"user_id": mw.GetUserID(c), "username": mw.GetUsername(c), "role": mw.GetRole(c), "student_id": mw.GetStudentID(c)})
	})

	repr := r.Group("/")
	repr.Use(mw.JWTAuthMiddleware(jwtKey, jwtIssuer, jwtAudience))
	repr.Use(func(c *gin.Context) {
		if c.GetString("role") != "institution_representative" {
			api.Abort(c, http.StatusForbidden, api.CodeForbidden, "Access restricted to institution representatives only")
			return
		}
		c.Next()
	})
	{
		repr.PATCH("/purchase", func(c *gin.Context) {
			handlers.HandleCreditsPurchased(c, m)
		})
		repr.GET("/mycredits", func(c *gin.Context) {
			handlers.HandleCreditsAvail(c, m)
		})
		repr.POST("/registration", func(c *gin.Context) {
			handlers.HandleInstitutionRegistered(c, m)
		})
		repr.POST("/institution/instructors", func(c *gin.Context) { handlers.HandleCreateInstructor(c, m) })
		repr.POST("/institution/student-roster", func(c *gin.Context) { handlers.HandleStudentRosterUpload(c, m) })
	}
	// ────────────────────────────────────────────────────────────────────────
	//  Student‐only endpoints
	// ────────────────────────────────────────────────────────────────────────
	std := r.Group("/")
	std.Use(mw.JWTAuthMiddleware(jwtKey, jwtIssuer, jwtAudience))
	std.Use(func(c *gin.Context) {
		if c.GetString("role") != "student" {
			api.Abort(c, http.StatusForbidden, api.CodeForbidden, "Access restricted to students only")
			return
		}
		c.Next()
	})
	{
		std.GET("/personal/grades", func(c *gin.Context) { handlers.HandleGetPersonalGrades(c, m) })
		std.PATCH("/student/reviewRequest", func(c *gin.Context) { handlers.HandlePostNewRequest(c, m) })
		std.PATCH("/student/status", func(c *gin.Context) { handlers.HandleGetRequestStatus(c, m) })
	}

	// ────────────────────────────────────────────────────────────────────────
	//  Instructor‐only endpoints
	// ────────────────────────────────────────────────────────────────────────
	instr := r.Group("/")
	instr.Use(mw.JWTAuthMiddleware(jwtKey, jwtIssuer, jwtAudience))
	instr.Use(func(c *gin.Context) {
		if c.GetString("role") != "instructor" {
			api.Abort(c, http.StatusForbidden, api.CodeForbidden, "Access restricted to instructors only")
			return
		}
		c.Next()
	})
	{
		instr.POST("/upload_init", func(c *gin.Context) { handlers.UploadExcelInit(c, m) })
		instr.PATCH("/postFinalGrades", func(c *gin.Context) { handlers.UploadExcelFinal(c, m) })
		instr.PATCH("/instructor/review-list", func(c *gin.Context) { handlers.HandleGetRequestList(c, m) })
		instr.PATCH("/instructor/reply", func(c *gin.Context) { handlers.HandlePostResponse(c, m) })
	}

	// ────────────────────────────────────────────────────────────────────────
	//  Shared stats endpoints (all roles)
	// ────────────────────────────────────────────────────────────────────────
	stats := r.Group("/stats")
	stats.Use(mw.JWTAuthMiddleware(jwtKey, jwtIssuer, jwtAudience))
	{
		stats.GET("/available", func(c *gin.Context) { handlers.HandleSubmissionLogs(c, m) })
		stats.GET("/courses", func(c *gin.Context) { handlers.HandleSubmissionLogs(c, m) })
		stats.POST("/distributions", handlers.HandleGetDistributions(m))
	}

	return r
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
