package handlers

import (
	"context"
	"net/http/httptest"
	"strings"

	"github.com/gin-gonic/gin"
)

type recordedMessage struct {
	key  string
	body []byte
}

// fakeMessenger records calls and returns queued replies in order.
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
