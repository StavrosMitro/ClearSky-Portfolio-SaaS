package handlers

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

func asStudent(c *gin.Context) {
	c.Set("institution_id", testInstitution)
	c.Set("user_id", "22222222-2222-2222-2222-222222222222")
	c.Set("role", "student")
	c.Set("student_id", "031001")
	c.Set("username", "alice@uni.example")
}

func TestCreateReviewRequiresAGradeInTheCourse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{replies: [][]byte{[]byte(`{"version":1,"data":{"has_grade":false}}`)}}
	c, w := newJSONContext(http.MethodPost, `{"grading_id":"`+testGrading+`","message":"please"}`)
	asStudent(c)
	HandleCreateReview(c, m)
	if w.Code != http.StatusNotFound || len(m.calls) != 1 {
		t.Fatalf("status %d, calls %d", w.Code, len(m.calls))
	}
}

func TestCreateReviewUsesTheVerifiedStudent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{replies: [][]byte{
		[]byte(`{"version":1,"data":{"has_grade":true,"state":"open"}}`),
		[]byte(`{"version":1,"data":{"id":"r1","status":"pending"}}`),
	}}
	c, w := newJSONContext(http.MethodPost, `{"grading_id":"`+testGrading+`","message":"please","student_id":"attacker"}`)
	asStudent(c)
	HandleCreateReview(c, m)
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	create := body(t, m.calls[1].body)
	if m.calls[1].key != "reviews.request" || create["student_id"] != "031001" || create["student_display"] != "alice@uni.example" || create["institution_id"] != testInstitution {
		t.Fatalf("create = %v", create)
	}
}

func TestPurchaseUsesTheCallersInstitution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{replies: [][]byte{[]byte(`{"version":1,"data":{"credits":10,"applied":true}}`)}}
	c, w := newJSONContext(http.MethodPatch, `{"amount":10,"name":"Someone Else","institution_id":"attacker"}`)
	c.Request.Header.Set("Idempotency-Key", "req-42")
	c.Set("institution_id", testInstitution)
	c.Set("user_id", "33333333-3333-3333-3333-333333333333")
	HandlePurchaseCredits(c, m)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	req := body(t, m.calls[0].body)
	if req["institution_id"] != testInstitution || req["idempotency_key"] != "req-42" || req["amount"] != float64(10) {
		t.Fatalf("purchase = %v", req)
	}
}
