package handlers

import (
	"bufio"
	"encoding/json"
	"log"
	"net/http"
	"orchestrator/internal/api"
	"os"
	"sync"

	"github.com/gin-gonic/gin"
)

type UserRequest struct {
	Name     string `json:"name" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Director string `json:"director" binding:"required"`
}
type Response struct {
	Message string `json:"message,omitempty"`
}

var (
	institutionStoragePath = "requests.json"
	institutionStorageMu   sync.Mutex
)

func appendInstitution(req UserRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	institutionStorageMu.Lock()
	defer institutionStorageMu.Unlock()
	f, err := os.OpenFile(institutionStoragePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func HandleInstitutionRegistered(c *gin.Context, m Messenger) {
	var req UserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Failure(c, http.StatusBadRequest, api.CodeInvalidRequest, "Name, director and a valid email are required", nil)
		return
	}
	var response Response
	if err := callJSON(c.Request.Context(), m, "institution.registered", req, &response); err != nil {
		messagingError(c, err)
		return
	}
	if err := appendInstitution(req); err != nil {
		// Registration is already committed in the owning service. Do not tell
		// clients to retry and create a duplicate; report the projection failure.
		log.Printf("institution list projection write failed: %v", err)
	}
	api.Success(c, http.StatusOK, response)
}

func GetInstitutions(c *gin.Context) {
	institutionStorageMu.Lock()
	defer institutionStorageMu.Unlock()
	f, err := os.Open(institutionStoragePath)
	if os.IsNotExist(err) {
		api.Success(c, http.StatusOK, []UserRequest{})
		return
	}
	if err != nil {
		log.Printf("institution list projection open failed: %v", err)
		api.Internal(c)
		return
	}
	defer f.Close()

	list := make([]UserRequest, 0)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var req UserRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			log.Printf("institution list projection contains malformed data: %v", err)
			api.Internal(c)
			return
		}
		list = append(list, req)
	}
	if err := scanner.Err(); err != nil {
		log.Printf("institution list projection read failed: %v", err)
		api.Internal(c)
		return
	}
	api.Success(c, http.StatusOK, list)
}
