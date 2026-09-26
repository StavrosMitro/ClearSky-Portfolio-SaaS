package mail

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"
)

// fakeSMTPServer accepts one message without TLS or authentication.
func fakeSMTPServer(t *testing.T) (string, int, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	received := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		fmt.Fprint(conn, "220 fake ESMTP\r\n")
		var data strings.Builder
		inData := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					received <- data.String()
					fmt.Fprint(conn, "250 queued\r\n")
					continue
				}
				data.WriteString(line)
				continue
			}
			command := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(command, "EHLO"):
				fmt.Fprint(conn, "250-fake\r\n250 8BITMIME\r\n")
			case command == "DATA":
				inData = true
				fmt.Fprint(conn, "354 go ahead\r\n")
			case command == "QUIT":
				fmt.Fprint(conn, "221 bye\r\n")
				return
			default:
				fmt.Fprint(conn, "250 ok\r\n")
			}
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	return address.IP.String(), address.Port, received
}

func TestSMTPSenderDeliversEncodedMessage(t *testing.T) {
	host, port, received := fakeSMTPServer(t)
	sender := &SMTPSender{Host: host, Port: port, From: &mail.Address{Name: "ClearSky", Address: "no-reply@uni.example"}, Timeout: 5 * time.Second}

	err := sender.Send(context.Background(), Message{To: "alice@uni.example", Subject: "Επιβεβαίωση λογαριασμού", Body: "Γεια σας,\nopen https://clearsky.example/activate#token=abc\n"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var raw string
	select {
	case raw = <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("the server received no message")
	}
	message, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse delivered message: %v", err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
	if err != nil || subject != "Επιβεβαίωση λογαριασμού" {
		t.Fatalf("subject = %q, %v", subject, err)
	}
	if message.Header.Get("To") != "alice@uni.example" || !strings.Contains(message.Header.Get("From"), "no-reply@uni.example") {
		t.Fatalf("unexpected headers: %v", message.Header)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(message.Body))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Γεια σας") || !strings.Contains(string(body), "#token=abc") {
		t.Fatalf("body = %q", body)
	}
}

func TestSendRejectsHeaderInjection(t *testing.T) {
	sender := &SMTPSender{Host: "127.0.0.1", Port: 1, From: &mail.Address{Address: "no-reply@uni.example"}, Timeout: time.Second}
	for name, msg := range map[string]Message{
		"recipient line break": {To: "alice@uni.example\r\nBcc: mallory@evil.example", Subject: "x", Body: "x"},
		"display name":         {To: "Mallory <mallory@evil.example>", Subject: "x", Body: "x"},
		"subject line break":   {To: "alice@uni.example", Subject: "hi\r\nBcc: mallory@evil.example", Body: "x"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := sender.Send(context.Background(), msg); err == nil || strings.Contains(err.Error(), "connect") {
				t.Fatalf("expected validation to fail before connecting, got %v", err)
			}
		})
	}
}

func TestSMTPFromEnvironment(t *testing.T) {
	t.Setenv("SMTP_HOST", "")
	if sender, err := SMTPFromEnvironment(); sender != nil || err != nil {
		t.Fatalf("unset host must disable email: %v, %v", sender, err)
	}
	t.Setenv("SMTP_HOST", "smtp.uni.example")
	t.Setenv("SMTP_FROM", "ClearSky <no-reply@uni.example>")
	t.Setenv("SMTP_PORT", "")
	t.Setenv("SMTP_USERNAME", "clearsky")
	t.Setenv("SMTP_PASSWORD", "")
	if _, err := SMTPFromEnvironment(); err == nil {
		t.Fatal("a username without a password must be rejected")
	}
	t.Setenv("SMTP_PASSWORD", "secret")
	sender, err := SMTPFromEnvironment()
	if err != nil || sender.Port != 587 || sender.From.Address != "no-reply@uni.example" {
		t.Fatalf("sender = %+v, %v", sender, err)
	}
	t.Setenv("SMTP_FROM", "")
	if _, err := SMTPFromEnvironment(); err == nil {
		t.Fatal("a missing sender address must be rejected")
	}
}
