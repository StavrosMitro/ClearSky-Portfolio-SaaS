package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const (
	testInstitution = "6f0b1a3e-0000-5000-8000-000000000001"
	testInstructor  = "11111111-2222-3333-4444-555555555555"
	testGrading     = "99999999-8888-7777-6666-555555555555"
)

func asInstructor(c *gin.Context) {
	c.Set("institution_id", testInstitution)
	c.Set("user_id", testInstructor)
	c.Set("role", "instructor")
}

func confirmContext() (*gin.Context, *httptest.ResponseRecorder) {
	c, w := newJSONContext(http.MethodPost, `{}`)
	c.Params = gin.Params{{Key: "id", Value: "upload-1"}}
	asInstructor(c)
	return c, w
}

func body(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

const snapshotReply = `{"version":1,"data":{"type":"grading.snapshot","grading_id":"` + testGrading + `","institution_id":"` + testInstitution + `",
	"course_code":"3101","course_title":"ΦΥΣΙΚΗ","period":"2025","state":"open","version":1,"instructor_id":"` + testInstructor + `",
	"question_weights":[1],"student_count":1,"opened_at":"2026-02-20T10:00:00Z",
	"grades":[{"student_id":"031001","student_name":"Alice Example","total":7.5,"question_scores":[7.5]}]}}`

func TestConfirmChargesThenCommitsThenSynchronises(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{replies: [][]byte{
		[]byte(`{"version":1,"data":{"grading_id":"` + testGrading + `","can_confirm":true,"requires_credit":true}}`),
		[]byte(`{"version":1,"data":{"credits":4,"applied":true}}`),
		[]byte(snapshotReply),
	}}
	c, w := confirmContext()

	HandleGradesConfirm(c, m)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var order []string
	for _, call := range m.calls {
		order = append(order, call.key+":"+body(t, call.body)["type"].(string))
	}
	if strings.Join(order, ",") != "grades.ingest.request:upload_status,institutions.request:charge,grades.ingest.request:confirm" {
		t.Fatalf("saga order = %v", order)
	}
	charge := body(t, m.calls[1].body)
	if charge["grading_id"] != testGrading || charge["institution_id"] != testInstitution {
		t.Fatalf("charge = %v", charge)
	}
	if len(m.sends) != 2 || m.sends[0].key != "grades.query.sync" || m.sends[1].key != "reviews.sync" {
		t.Fatalf("sync messages = %+v", m.sends)
	}
	if strings.Contains(string(m.sends[0].body), "Alice") {
		t.Fatal("student names must not leave grades-ingest")
	}
	if strings.Contains(string(m.sends[1].body), "grades") {
		t.Fatal("reviews receives only the grading header")
	}
	result := body(t, w.Body.Bytes())["data"].(map[string]any)
	if result["charged"] != true || result["synchronised"] != true || result["state"] != "open" {
		t.Fatalf("result = %v", result)
	}
}

func TestConfirmWithoutNewGradingDoesNotCharge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{replies: [][]byte{
		[]byte(`{"version":1,"data":{"grading_id":"` + testGrading + `","can_confirm":true,"requires_credit":false}}`),
		[]byte(snapshotReply),
	}}
	c, w := confirmContext()
	HandleGradesConfirm(c, m)
	if w.Code != http.StatusOK || len(m.calls) != 2 || m.calls[1].key != "grades.ingest.request" {
		t.Fatalf("status %d, calls %+v", w.Code, m.calls)
	}
}

func TestConfirmStopsWhenCreditsRunOut(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{replies: [][]byte{
		[]byte(`{"version":1,"data":{"grading_id":"` + testGrading + `","can_confirm":true,"requires_credit":true}}`),
		[]byte(`{"version":1,"error":{"code":"INSUFFICIENT_CREDITS","message":"Not enough credits","retryable":false}}`),
	}}
	c, w := confirmContext()
	HandleGradesConfirm(c, m)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"INSUFFICIENT_CREDITS"`) {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
	if len(m.calls) != 2 || len(m.sends) != 0 {
		t.Fatalf("nothing may be published without a credit: calls %d, sends %d", len(m.calls), len(m.sends))
	}
}

func TestConfirmSucceedsWhenSynchronisationFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{sendError: errors.New("broker hiccup"), replies: [][]byte{
		[]byte(`{"version":1,"data":{"grading_id":"` + testGrading + `","can_confirm":true,"requires_credit":false}}`),
		[]byte(snapshotReply),
	}}
	c, w := confirmContext()
	HandleGradesConfirm(c, m)
	if w.Code != http.StatusOK || body(t, w.Body.Bytes())["data"].(map[string]any)["synchronised"] != false {
		t.Fatalf("grades are committed; reconcile repairs the sync: %d %s", w.Code, w.Body.String())
	}
}

func upload(t *testing.T, kind, filename string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if kind != "" {
		_ = writer.WriteField("kind", kind)
	}
	part, _ := writer.CreateFormFile("file", filename)
	_, _ = part.Write([]byte("PK fake workbook"))
	_ = writer.Close()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/grades/uploads", &buf)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	asInstructor(c)
	return c, w
}

func TestUploadForwardsWorkbookWithCallerIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, args := range map[string][2]string{"no kind": {"", "grades.xlsx"}, "not xlsx": {"initial", "grades.csv"}} {
		m := &fakeMessenger{}
		c, w := upload(t, args[0], args[1])
		HandleGradesUpload(c, m)
		if w.Code != http.StatusBadRequest || len(m.calls) != 0 {
			t.Fatalf("%s: status %d, calls %d", name, w.Code, len(m.calls))
		}
	}
	m := &fakeMessenger{replies: [][]byte{[]byte(`{"version":1,"data":{"upload_id":"u1","can_confirm":true}}`)}}
	c, w := upload(t, "final", "Grades.XLSX")
	HandleGradesUpload(c, m)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	req := body(t, m.calls[0].body)
	if req["type"] != "preview" || req["kind"] != "final" || req["institution_id"] != testInstitution || req["uploader_id"] != testInstructor || req["file_base64"] == "" {
		t.Fatalf("preview request = %v", req)
	}
}
