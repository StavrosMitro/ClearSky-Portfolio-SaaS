package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

type recordedMessage struct {
	key  string
	body []byte
}

type fakeMessenger struct {
	calls     []recordedMessage
	sends     []recordedMessage
	replies   [][]byte
	callError error
	sendError error
}

func (f *fakeMessenger) Call(_ context.Context, key string, body []byte) ([]byte, error) {
	f.calls = append(f.calls, recordedMessage{key: key, body: append([]byte(nil), body...)})
	if f.callError != nil {
		return nil, f.callError
	}
	if len(f.replies) == 0 {
		return []byte(`{"version":1,"data":{}}`), nil
	}
	reply := f.replies[0]
	f.replies = f.replies[1:]
	return reply, nil
}

func (f *fakeMessenger) Send(_ context.Context, key string, body []byte) error {
	f.sends = append(f.sends, recordedMessage{key: key, body: append([]byte(nil), body...)})
	return f.sendError
}

func (f *fakeMessenger) Ready() bool { return true }

func newJSONContext(method, body string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, w
}

func decodeReviewBody(t *testing.T, raw []byte) map[string]interface{} {
	t.Helper()
	var envelope struct {
		Body map[string]interface{} `json:"body"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode review envelope: %v", err)
	}
	return envelope.Body
}

func TestHandlePostNewRequestRestoresBothProjectionsAndTrustedIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{replies: [][]byte{[]byte(`{"version":1,"data":{"created":true}}`), []byte(`{"version":1,"data":{"inserted":true}}`)}}
	c, w := newJSONContext(http.MethodPatch, `{"course_id":"CS101","exam_period":"spring","student_message":"please review","user_id":"attacker","student_id":"attacker"}`)
	c.Set("role", "student")
	c.Set("user_id", "user-7")
	c.Set("student_id", "0310001")

	HandlePostNewRequest(c, m)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	wantKeys := []string{"student.postNewRequest", "instructor.insertStudentRequest"}
	if len(m.calls) != len(wantKeys) {
		t.Fatalf("calls=%v", m.calls)
	}
	for i, key := range wantKeys {
		if m.calls[i].key != key {
			t.Fatalf("call %d key=%q want=%q", i, m.calls[i].key, key)
		}
		body := decodeReviewBody(t, m.calls[i].body)
		if body["user_id"] != "user-7" || body["student_id"] != "0310001" {
			t.Fatalf("untrusted identity reached message: %#v", body)
		}
	}
}

func TestHandlePostResponseUpdatesBothProjectionsAndInjectsInstructor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{}
	c, w := newJSONContext(http.MethodPatch, `{"user_id":"student-user","exam_period":"spring","instructor_reply_message":"accepted","instructor_action":"approve"}`)
	c.Set("role", "instructor")
	c.Set("username", "teacher@example.test")

	HandlePostResponse(c, m)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	wantKeys := []string{"student.updateInstructorResponse", "instructor.postResponse"}
	if len(m.calls) != len(wantKeys) {
		t.Fatalf("calls=%v", m.calls)
	}
	for i, key := range wantKeys {
		if m.calls[i].key != key {
			t.Fatalf("call %d key=%q want=%q", i, m.calls[i].key, key)
		}
		if got := decodeReviewBody(t, m.calls[i].body)["username"]; got != "teacher@example.test" {
			t.Fatalf("username=%v", got)
		}
	}
}

func TestHandleGetRequestListDoesNotRequireAnHTTPBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPatch, "/", nil)
	c.Set("username", "teacher@example.test")

	HandleGetRequestList(c, m)

	if w.Code != http.StatusOK || len(m.calls) != 1 {
		t.Fatalf("status=%d calls=%v body=%s", w.Code, m.calls, w.Body.String())
	}
	if got := decodeReviewBody(t, m.calls[0].body)["username"]; got != "teacher@example.test" {
		t.Fatalf("username=%v", got)
	}
}

func TestCreditsPurchaseSynchronizesFinalGradesOnlyAfterSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Run("success", func(t *testing.T) {
		m := &fakeMessenger{replies: [][]byte{[]byte(`{"version":1,"data":{"message":"purchased"}}`)}}
		c, w := newJSONContext(http.MethodPatch, `{"name":"NTUA","amount":4}`)
		HandleCreditsPurchased(c, m)
		if w.Code != http.StatusOK || len(m.sends) != 1 || m.sends[0].key != "incr.credits" {
			t.Fatalf("status=%d sends=%v body=%s", w.Code, m.sends, w.Body.String())
		}
		var request PurchaseRequest
		if err := json.Unmarshal(m.sends[0].body, &request); err != nil || request.Name != "NTUA" || request.Amount != 4 {
			t.Fatalf("sync request=%+v err=%v", request, err)
		}
	})
	t.Run("downstream rejection", func(t *testing.T) {
		m := &fakeMessenger{replies: [][]byte{[]byte(`{"version":1,"error":{"code":"INVALID_REQUEST","message":"rejected","retryable":false}}`)}}
		c, w := newJSONContext(http.MethodPatch, `{"name":"NTUA","amount":4}`)
		HandleCreditsPurchased(c, m)
		if w.Code != http.StatusBadRequest || len(m.sends) != 0 {
			t.Fatalf("status=%d sends=%v body=%s", w.Code, m.sends, w.Body.String())
		}
	})
}

func TestCreditsSpentContainsTheRequiredPayload(t *testing.T) {
	m := &fakeMessenger{}
	if err := HandleCreditsSpent(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if len(m.sends) != 1 || m.sends[0].key != "credits.spent" {
		t.Fatalf("sends=%v", m.sends)
	}
	var request SpendReq
	if err := json.Unmarshal(m.sends[0].body, &request); err != nil || request.Name == "" || request.Amount != 1 {
		t.Fatalf("spend request=%+v err=%v", request, err)
	}
}

func TestInstitutionRegistrationRestoresListProjection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldPath := institutionStoragePath
	institutionStoragePath = t.TempDir() + "/requests.json"
	t.Cleanup(func() { institutionStoragePath = oldPath })
	m := &fakeMessenger{replies: [][]byte{[]byte(`{"version":1,"data":{"message":"registered"}}`)}}
	c, w := newJSONContext(http.MethodPost, `{"name":"NTUA","email":"admin@example.test","director":"Director"}`)
	HandleInstitutionRegistered(c, m)
	if w.Code != http.StatusOK {
		t.Fatalf("registration status=%d body=%s", w.Code, w.Body.String())
	}

	listWriter := httptest.NewRecorder()
	listContext, _ := gin.CreateTestContext(listWriter)
	GetInstitutions(listContext)
	if listWriter.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listWriter.Code, listWriter.Body.String())
	}
	var envelope struct {
		Data []UserRequest `json:"data"`
	}
	if err := json.Unmarshal(listWriter.Body.Bytes(), &envelope); err != nil || len(envelope.Data) != 1 || envelope.Data[0].Name != "NTUA" {
		list := envelope.Data
		t.Fatalf("list=%+v err=%v", list, err)
	}
	info, err := os.Stat(institutionStoragePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("projection mode=%v err=%v", info.Mode().Perm(), err)
	}
}

func xlsxUploadRequest(t *testing.T) *http.Request {
	t.Helper()
	book := excelize.NewFile()
	if err := book.SetCellValue("Sheet1", "A1", "valid workbook"); err != nil {
		t.Fatal(err)
	}
	xlsx, err := book.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "grades.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(xlsx.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPatch, "/postFinalGrades", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func TestFinalGradeSideEffectsRunOnlyAfterSuccessfulWorkerReply(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Run("worker rejection", func(t *testing.T) {
		m := &fakeMessenger{replies: [][]byte{[]byte(`{"version":1,"error":{"code":"INVALID_REQUEST","message":"invalid template","retryable":false}}`)}}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = xlsxUploadRequest(t)
		UploadExcelFinal(c, m)
		if w.Code != http.StatusBadRequest || len(m.sends) != 0 {
			t.Fatalf("status=%d sends=%v body=%s", w.Code, m.sends, w.Body.String())
		}
	})
	t.Run("worker success", func(t *testing.T) {
		m := &fakeMessenger{replies: [][]byte{[]byte(`{"version":1,"data":{"message":"inserted"}}`)}}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = xlsxUploadRequest(t)
		UploadExcelFinal(c, m)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		wantKeys := []string{"credits.spent", "postgrades.statistics", "postgrades.view"}
		if len(m.sends) != len(wantKeys) {
			t.Fatalf("sends=%v", m.sends)
		}
		for i, key := range wantKeys {
			if m.sends[i].key != key {
				t.Fatalf("send %d key=%q want=%q", i, m.sends[i].key, key)
			}
		}
	})
}
