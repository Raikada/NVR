package notifications

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"html/template"
	"net/smtp"
	"strings"
)

// SMTPSettings is the rendered subset of system_settings the SMTP path
// needs. Loaded fresh per dispatch (cheap; cached if needed).
type SMTPSettings struct {
	Host        string
	Port        int
	Username    string
	Password    string // already decrypted by caller via Vault
	FromAddress string
	UseTLS      bool
}

const emailTemplate = `<!DOCTYPE html>
<html><body style="font-family: sans-serif">
<h2>{{.Site.Name}} — {{.Event.TypeDisplayName}}</h2>
<p><strong>Camera:</strong> {{.Event.Camera.DisplayName}}</p>
<p><strong>Source:</strong> {{.Event.Source}}</p>
<p><strong>When:</strong> {{.Event.OccurredAt}}</p>
{{if .ThumbnailURL}}<p><img src="{{.ThumbnailURL}}" alt="thumbnail" style="max-width:480px"/></p>{{end}}
<p><a href="{{.SnapshotURL}}">View full snapshot</a></p>
</body></html>`

// RenderEmail returns the subject + html body for the given payload.
func RenderEmail(p *Payload) (subject, htmlBody string, err error) {
	tmpl, err := template.New("email").Parse(emailTemplate)
	if err != nil {
		return "", "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return "", "", err
	}
	subject = fmt.Sprintf("[%s] %s — %s", p.Site.Name, p.Event.TypeDisplayName, p.Event.Camera.DisplayName)
	return subject, buf.String(), nil
}

// SendEmail delivers the message to recipient via the configured SMTP
// server. Honors UseTLS by dialing TLS first (rather than STARTTLS).
func SendEmail(ctx context.Context, s SMTPSettings, recipient string, subject, htmlBody string) error {
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	auth := smtp.PlainAuth("", s.Username, s.Password, s.Host)
	headers := []string{
		"From: " + s.FromAddress,
		"To: " + recipient,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
	}
	msg := []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + htmlBody)
	if s.UseTLS {
		return sendTLS(addr, auth, s.FromAddress, recipient, msg, s.Host)
	}
	return smtp.SendMail(addr, auth, s.FromAddress, []string{recipient}, msg)
}

func sendTLS(addr string, auth smtp.Auth, from, to string, msg []byte, host string) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if err := c.Auth(auth); err != nil {
		return err
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
