package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// Email sends plain-text mail over SMTP with STARTTLS. Without a host it is
// not configured and every notification skips the channel.
type Email struct {
	Host string
	Port string
	User string
	Pass string
	From string
	// Dial overrides the connection for tests.
	Dial func(ctx context.Context, addr string) (net.Conn, error)
}

// NewEmail builds the sender from SMTP_* settings.
func NewEmail(host, port, user, pass, from string) *Email {
	if port == "" {
		port = "587"
	}
	if from == "" && user != "" {
		from = user
	}
	return &Email{Host: host, Port: port, User: user, Pass: pass, From: from}
}

func (e *Email) Name() string     { return "email" }
func (e *Email) Configured() bool { return e != nil && e.Host != "" && e.From != "" }

// Send delivers one message. The recipient must parse as an address.
func (e *Email) Send(ctx context.Context, to, subject, text string) error {
	if !e.Configured() {
		return ErrNotConfigured
	}
	if _, err := mail.ParseAddress(to); err != nil {
		return fmt.Errorf("email: bad recipient")
	}
	addr := net.JoinHostPort(e.Host, e.Port)
	dial := e.Dial
	if dial == nil {
		dial = func(ctx context.Context, addr string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", addr)
		}
	}
	conn, err := dial(ctx, addr)
	if err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	c, err := smtp.NewClient(conn, e.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: e.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	} else if e.Port != "25" {
		return errors.New("email: server does not offer STARTTLS")
	}
	if e.User != "" {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(smtp.PlainAuth("", e.User, e.Pass, e.Host)); err != nil {
				return err
			}
		}
	}
	if err := c.Mail(e.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := strings.Join([]string{
		"From: " + e.From,
		"To: " + to,
		"Subject: " + sanitizeHeader(subject),
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		text,
		"",
	}, "\r\n")
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func sanitizeHeader(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, s)
}
