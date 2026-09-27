// Package mail sends plain-text transactional email over SMTP.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	To      string
	Subject string
	Body    string
}

type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// SMTPSender delivers mail through one SMTP relay. Port 465 uses implicit TLS;
// other ports upgrade with STARTTLS when the server offers it. Credentials are
// only ever sent over TLS (net/smtp refuses otherwise).
type SMTPSender struct {
	Host     string
	Port     int
	Username string
	Password string
	From     *mail.Address
	Timeout  time.Duration
}

// SMTPFromEnvironment returns nil when SMTP_HOST is unset, which disables
// every feature that needs email.
func SMTPFromEnvironment() (*SMTPSender, error) {
	host := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	if host == "" {
		return nil, nil
	}
	port := 587
	if raw := strings.TrimSpace(os.Getenv("SMTP_PORT")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 65535 {
			return nil, errors.New("SMTP_PORT must be a TCP port number")
		}
		port = value
	}
	from, err := mail.ParseAddress(strings.TrimSpace(os.Getenv("SMTP_FROM")))
	if err != nil {
		return nil, errors.New("SMTP_FROM must be a valid address, e.g. ClearSky <no-reply@example.edu>")
	}
	username, password := os.Getenv("SMTP_USERNAME"), os.Getenv("SMTP_PASSWORD")
	if (username == "") != (password == "") {
		return nil, errors.New("SMTP_USERNAME and SMTP_PASSWORD must be set together")
	}
	return &SMTPSender{Host: host, Port: port, Username: username, Password: password, From: from, Timeout: 8 * time.Second}, nil
}

func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	to, err := bareAddress(msg.To)
	if err != nil {
		return err
	}
	body, err := buildMessage(s.From, to, msg.Subject, msg.Body, time.Now())
	if err != nil {
		return err
	}

	deadline := time.Now().Add(s.Timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	address := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	tlsConfig := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Deadline: deadline}
	var conn net.Conn
	if s.Port == 465 {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("connect to SMTP server: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}

	client, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return fmt.Errorf("SMTP greeting: %w", err)
	}
	defer client.Close()
	if s.Port != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("SMTP STARTTLS: %w", err)
			}
		}
	}
	if s.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("SMTP authentication: %w", err)
		}
	}
	if err := client.Mail(s.From.Address); err != nil {
		return fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("SMTP RCPT TO: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := writer.Write(body); err != nil {
		return fmt.Errorf("SMTP message body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("SMTP message rejected: %w", err)
	}
	return client.Quit()
}

// bareAddress accepts only a plain address, which also rules out header
// injection through line breaks or display names.
func bareAddress(raw string) (*mail.Address, error) {
	address, err := mail.ParseAddress(raw)
	if err != nil || address.Name != "" || address.Address != raw {
		return nil, errors.New("recipient must be a plain email address")
	}
	return address, nil
}

func buildMessage(from, to *mail.Address, subject, body string, now time.Time) ([]byte, error) {
	if strings.ContainsAny(subject, "\r\n") {
		return nil, errors.New("subject must be a single line")
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	domain := from.Address[strings.LastIndex(from.Address, "@")+1:]

	var b bytes.Buffer
	for _, header := range [][2]string{
		{"From", from.String()},
		{"To", to.Address},
		{"Subject", mime.QEncoding.Encode("utf-8", subject)},
		{"Date", now.Format(time.RFC1123Z)},
		{"Message-ID", "<" + hex.EncodeToString(id) + "@" + domain + ">"},
		{"MIME-Version", "1.0"},
		{"Content-Type", "text/plain; charset=UTF-8"},
		{"Content-Transfer-Encoding", "quoted-printable"},
	} {
		fmt.Fprintf(&b, "%s: %s\r\n", header[0], header[1])
	}
	b.WriteString("\r\n")
	encoder := quotedprintable.NewWriter(&b)
	if _, err := encoder.Write([]byte(body)); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
