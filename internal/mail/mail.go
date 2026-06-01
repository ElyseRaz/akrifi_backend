package mail

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// SendResetCode envoie le code de réinitialisation à l'adresse indiquée.
func SendResetCode(toEmail, code string) error {
	subject := "A.KRI.FI — Code de réinitialisation (15 min)"
	body := resetCodeHTML(code)
	if err := send(toEmail, subject, body); err != nil {
		return err
	}
	log.Printf("[MAIL] Email reset envoyé avec succès à %s", toEmail)
	return nil
}

// IsConfigured indique si les variables SMTP minimales sont définies.
func IsConfigured() bool {
	return os.Getenv("SMTP_HOST") != "" &&
		os.Getenv("SMTP_USER") != "" &&
		os.Getenv("SMTP_PASS") != ""
}

// ─── Envoi ───────────────────────────────────────────────────────────────────

func send(to, subject, htmlBody string) error {
	host := os.Getenv("SMTP_HOST") // smtp.gmail.com
	port := os.Getenv("SMTP_PORT") // 587 ou 465
	user := os.Getenv("SMTP_USER") // email expéditeur brut
	pass := os.Getenv("SMTP_PASS") // mot de passe / app-password
	fromDisplay := os.Getenv("SMTP_FROM") // optionnel : "A.KRI.FI <email>"

	if host == "" || user == "" || pass == "" {
		return fmt.Errorf("SMTP non configuré (SMTP_HOST / SMTP_USER / SMTP_PASS)")
	}
	if port == "" {
		port = "587"
	}

	// mailFrom = adresse brute pour la commande SMTP "MAIL FROM" — PAS de "Nom <addr>"
	mailFrom := user

	// displayFrom = affiché dans l'en-tête "From:" du message
	displayFrom := fromDisplay
	if displayFrom == "" {
		displayFrom = "A.KRI.FI <" + user + ">"
	}

	raw := buildRaw(displayFrom, to, subject, htmlBody)
	addr := net.JoinHostPort(host, port)

	log.Printf("[MAIL] Tentative envoi à %s via %s:%s", to, host, port)

	if port == "465" {
		return sendDirectTLS(addr, host, user, pass, mailFrom, to, raw)
	}
	// Port 587 : connexion plain + STARTTLS
	auth := smtp.PlainAuth("", user, pass, host)
	return smtp.SendMail(addr, auth, mailFrom, []string{to}, raw)
}

// sendDirectTLS pour le port 465 (TLS immédiat, pas de STARTTLS).
func sendDirectTLS(addr, host, user, pass, from, to string, msg []byte) error {
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	rawConn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("dial tcp: %w", err)
	}

	tlsConn := tls.Client(rawConn, &tls.Config{ServerName: host})
	if err := tlsConn.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		return err
	}
	if err := tlsConn.Handshake(); err != nil {
		return fmt.Errorf("tls handshake: %w", err)
	}

	c, err := smtp.NewClient(tlsConn, host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer c.Quit() //nolint:errcheck

	if err := c.Auth(smtp.PlainAuth("", user, pass, host)); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err = w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}

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
  <table width="100%" cellpadding="0" cellspacing="0" style="min-height:100vh;">
    <tr><td align="center" style="padding:40px 16px;">
      <table width="100%" style="max-width:480px;background:#ffffff;border-radius:20px;overflow:hidden;box-shadow:0 8px 40px rgba(38,27,190,0.10);">

        <!-- En-tête -->
        <tr>
          <td style="background:#261BBE;padding:36px 40px 28px;text-align:center;">
            <div style="font-size:30px;font-weight:800;color:#ffffff;letter-spacing:-1px;line-height:1;">A.KRI.FI</div>
            <div style="font-size:12px;color:rgba(255,255,255,0.65);margin-top:6px;">Antoko mpihira Kristy Fitiavana</div>
          </td>
        </tr>

        <!-- Corps -->
        <tr>
          <td style="padding:36px 40px 28px;">
            <h1 style="margin:0 0 8px;font-size:22px;font-weight:800;color:#0E0B2E;letter-spacing:-0.4px;">Fanovana teny miafina</h1>
            <p style="margin:0 0 28px;font-size:14px;color:#7A77A1;line-height:1.6;">
              Nahangataka ny fanovana ny teny miafina-nao tao amin'ny A.KRI.FI.
              Ampiasao ity code ity mba hamerenana azy.
              <strong>Manan-kery 15 minitra</strong> ity.
            </p>

            <!-- Code -->
            <div style="background:#F6F4FB;border-radius:14px;padding:28px 20px;text-align:center;border:1.5px solid #E6E4F0;margin-bottom:28px;">
              <div style="font-size:11px;font-weight:700;color:#7A77A1;letter-spacing:0.1em;margin-bottom:14px;text-transform:uppercase;">Ny code-nao</div>
              <div style="font-size:40px;font-weight:800;color:#261BBE;font-family:'Courier New',Courier,monospace;line-height:1;letter-spacing:4px;">` + digits + `</div>
              <div style="font-size:12px;color:#A8A6CC;margin-top:12px;">Indray mandeha ihany &nbsp;·&nbsp; 15 minitra</div>
            </div>

            <!-- Avertissement -->
            <div style="background:#FFF3F5;border-left:3px solid #E63E5C;border-radius:0 8px 8px 0;padding:12px 16px;">
              <p style="margin:0;font-size:12.5px;color:#E63E5C;line-height:1.5;">
                <strong>Raha tsy ianao no nangataka ity?</strong><br>
                Afoy ity hafatra ity — tsy hisy fiovana amin'ny kaontinao.
              </p>
            </div>
          </td>
        </tr>

        <!-- Pied de page -->
        <tr>
          <td style="background:#F6F4FB;padding:18px 40px;text-align:center;border-top:1px solid #E6E4F0;">
            <p style="margin:0;font-size:12px;color:#A8A6CC;">A.KRI.FI &nbsp;·&nbsp; Ny hirany, eo am-pelatananao</p>
          </td>
        </tr>

      </table>
    </td></tr>
  </table>
</body>
</html>`
}
