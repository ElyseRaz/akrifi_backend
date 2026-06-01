// Package mail gère l'envoi d'emails pour A.KRI.FI.
//
// Priorité des providers (premier configuré utilisé) :
//  1. Resend   — RESEND_API_KEY              (HTTP/443, recommandé sur Railway)
//  2. SMTP     — SMTP_HOST + SMTP_USER + SMTP_PASS  (587/465)
package mail

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// SendResetCode envoie le code de réinitialisation.
func SendResetCode(toEmail, code string) error {
	subject := "A.KRI.FI — Code de réinitialisation (15 min)"
	body := resetCodeHTML(code)

	var err error
	switch {
	case os.Getenv("RESEND_API_KEY") != "":
		log.Printf("[MAIL] Provider : Resend API")
		err = sendResend(toEmail, subject, body)
	case smtpConfigured():
		log.Printf("[MAIL] Provider : SMTP (%s:%s)", os.Getenv("SMTP_HOST"), smtpPort())
		err = sendSMTP(toEmail, subject, body)
	default:
		return fmt.Errorf("aucun provider email configuré (RESEND_API_KEY ou SMTP_HOST/USER/PASS)")
	}

	if err != nil {
		return err
	}
	log.Printf("[MAIL] ✓ Email envoyé à %s", toEmail)
	return nil
}

// IsConfigured retourne vrai si au moins un provider est configuré.
func IsConfigured() bool {
	return os.Getenv("RESEND_API_KEY") != "" || smtpConfigured()
}

func smtpConfigured() bool {
	return os.Getenv("SMTP_HOST") != "" &&
		os.Getenv("SMTP_USER") != "" &&
		os.Getenv("SMTP_PASS") != ""
}

func smtpPort() string {
	if p := os.Getenv("SMTP_PORT"); p != "" {
		return p
	}
	return "587"
}

// ─── Provider 1 : Resend (HTTP/443) ─────────────────────────────────────────

func sendResend(to, subject, htmlBody string) error {
	apiKey := os.Getenv("RESEND_API_KEY")
	fromAddr := os.Getenv("RESEND_FROM")
	if fromAddr == "" {
		// Resend fournit ce domaine par défaut sur le plan gratuit
		fromAddr = "A.KRI.FI <onboarding@resend.dev>"
	}

	payload, err := json.Marshal(map[string]any{
		"from":    fromAddr,
		"to":      []string{to},
		"subject": subject,
		"html":    htmlBody,
	})
	if err != nil {
		return fmt.Errorf("json: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("resend http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("resend API %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// ─── Provider 2 : SMTP (587 STARTTLS / 465 TLS direct) ──────────────────────

func sendSMTP(to, subject, htmlBody string) error {
	host := os.Getenv("SMTP_HOST")
	port := smtpPort()
	user := os.Getenv("SMTP_USER")
	pass := os.Getenv("SMTP_PASS")
	fromDisplay := os.Getenv("SMTP_FROM")

	// mailFrom = adresse brute pour la commande SMTP "MAIL FROM"
	mailFrom := user
	if fromDisplay == "" {
		fromDisplay = "A.KRI.FI <" + user + ">"
	}

	raw := buildRaw(fromDisplay, to, subject, htmlBody)
	addr := net.JoinHostPort(host, port)

	if port == "465" {
		return smtpDirectTLS(addr, host, user, pass, mailFrom, to, raw)
	}
	return smtpStartTLS(addr, host, user, pass, mailFrom, to, raw)
}

// smtpStartTLS — port 587 avec timeout explicite.
func smtpStartTLS(addr, host, user, pass, from, to string, msg []byte) error {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connexion SMTP: %w", err)
	}
	conn.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("client SMTP: %w", err)
	}
	defer c.Quit() //nolint:errcheck

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if err := c.Auth(smtp.PlainAuth("", user, pass, host)); err != nil {
		return fmt.Errorf("auth SMTP: %w", err)
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}

// smtpDirectTLS — port 465 avec TLS immédiat.
func smtpDirectTLS(addr, host, user, pass, from, to string, msg []byte) error {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	rawConn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("dial TLS: %w", err)
	}
	tlsConn := tls.Client(rawConn, &tls.Config{ServerName: host})
	tlsConn.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck
	if err := tlsConn.Handshake(); err != nil {
		return fmt.Errorf("handshake TLS: %w", err)
	}
	c, err := smtp.NewClient(tlsConn, host)
	if err != nil {
		return fmt.Errorf("client SMTP: %w", err)
	}
	defer c.Quit() //nolint:errcheck

	if err := c.Auth(smtp.PlainAuth("", user, pass, host)); err != nil {
		return fmt.Errorf("auth SMTP: %w", err)
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}

// ─── Construction du message ─────────────────────────────────────────────────

func buildRaw(from, to, subject, htmlBody string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(htmlBody)
	return []byte(b.String())
}

// ─── Template HTML ────────────────────────────────────────────────────────────

func resetCodeHTML(code string) string {
	digits := strings.Join(strings.Split(code, ""), "&nbsp;&nbsp;")
	return `<!DOCTYPE html>
<html lang="fr">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#F1EEF8;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;">
  <table width="100%" cellpadding="0" cellspacing="0">
    <tr><td align="center" style="padding:40px 16px;">
      <table width="100%" style="max-width:480px;background:#fff;border-radius:20px;overflow:hidden;box-shadow:0 8px 40px rgba(38,27,190,.10);">

        <tr><td style="background:#261BBE;padding:36px 40px 28px;text-align:center;">
          <div style="font-size:30px;font-weight:800;color:#fff;letter-spacing:-1px;">A.KRI.FI</div>
          <div style="font-size:12px;color:rgba(255,255,255,.65);margin-top:6px;">Antoko mpihira Kristy Fitiavana</div>
        </td></tr>

        <tr><td style="padding:36px 40px 28px;">
          <h1 style="margin:0 0 8px;font-size:22px;font-weight:800;color:#0E0B2E;">Fanovana teny miafina</h1>
          <p style="margin:0 0 28px;font-size:14px;color:#7A77A1;line-height:1.6;">
            Nahangataka ny fanovana ny teny miafina-nao tao amin'ny A.KRI.FI.
            Ampiasao ity code ity. <strong>Manan-kery 15 minitra.</strong>
          </p>
          <div style="background:#F6F4FB;border-radius:14px;padding:28px 20px;text-align:center;border:1.5px solid #E6E4F0;margin-bottom:28px;">
            <div style="font-size:11px;font-weight:700;color:#7A77A1;letter-spacing:.1em;margin-bottom:14px;text-transform:uppercase;">Code-nao</div>
            <div style="font-size:40px;font-weight:800;color:#261BBE;font-family:'Courier New',monospace;letter-spacing:4px;">` + digits + `</div>
            <div style="font-size:12px;color:#A8A6CC;margin-top:12px;">Indray mandeha · 15 minitra</div>
          </div>
          <div style="background:#FFF3F5;border-left:3px solid #E63E5C;border-radius:0 8px 8px 0;padding:12px 16px;">
            <p style="margin:0;font-size:12.5px;color:#E63E5C;line-height:1.5;">
              <strong>Raha tsy ianao no nangataka ity?</strong><br>
              Afoy ity hafatra ity — tsy hisy fiovana amin'ny kaontinao.
            </p>
          </div>
        </td></tr>

        <tr><td style="background:#F6F4FB;padding:18px 40px;text-align:center;border-top:1px solid #E6E4F0;">
          <p style="margin:0;font-size:12px;color:#A8A6CC;">A.KRI.FI · Ny hirany, eo am-pelatananao</p>
        </td></tr>

      </table>
    </td></tr>
  </table>
</body>
</html>`
}
