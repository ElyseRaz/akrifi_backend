package handlers

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"akrifi/api/internal/mail"
	"akrifi/api/internal/middleware"
)

var emailRe = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

type AuthHandler struct {
	pool *pgxpool.Pool
}

func NewAuthHandler(pool *pgxpool.Pool) *AuthHandler {
	return &AuthHandler{pool: pool}
}

// POST /api/auth/register
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Nom           string   `json:"nom"`
		Prenom        string   `json:"prenom"`
		Email         string   `json:"email"`
		Password      string   `json:"password"`
		DateNaissance string   `json:"date_naissance"`
		VaomieraIDs   []string `json:"vaomiera_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		JSONError(w, 400, "Corps de requête invalide")
		return
	}

	input.Nom    = strings.TrimSpace(input.Nom)
	input.Prenom = strings.TrimSpace(input.Prenom)
	input.Email  = strings.ToLower(strings.TrimSpace(input.Email))

	switch {
	case input.Nom == "":
		JSONError(w, 400, "Le nom est requis")
		return
	case len(input.Nom) > 100:
		JSONError(w, 400, "Le nom ne doit pas dépasser 100 caractères")
		return
	case input.Prenom == "":
		JSONError(w, 400, "Le prénom est requis")
		return
	case len(input.Prenom) > 100:
		JSONError(w, 400, "Le prénom ne doit pas dépasser 100 caractères")
		return
	case !emailRe.MatchString(input.Email):
		JSONError(w, 400, "Adresse email invalide")
		return
	case len(input.Password) < 8:
		JSONError(w, 400, "Le mot de passe doit contenir au moins 8 caractères")
		return
	case len(input.Password) > 128:
		JSONError(w, 400, "Le mot de passe est trop long")
		return
	}

	var exists bool
	h.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)`, input.Email).Scan(&exists)
	if exists {
		JSONError(w, 409, "Email déjà utilisé")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}

	var user struct {
		ID     string `json:"id"`
		Nom    string `json:"nom"`
		Prenom string `json:"prenom"`
		Email  string `json:"email"`
		Role   string `json:"role"`
	}
	err = h.pool.QueryRow(r.Context(),
		`INSERT INTO users (nom, prenom, email, password_hash, date_naissance)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id, nom, prenom, email, role`,
		input.Nom, input.Prenom, input.Email, string(hash), ParseDate(input.DateNaissance),
	).Scan(&user.ID, &user.Nom, &user.Prenom, &user.Email, &user.Role)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}

	for _, vID := range input.VaomieraIDs {
		h.pool.Exec(r.Context(),
			`INSERT INTO user_vaomiera (user_id, vaomiera_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			user.ID, vID,
		)
	}

	token, err := middleware.SignToken(user.ID)
	if err != nil {
		JSONError(w, 500, "Erreur génération token")
		return
	}

	JSON(w, 201, map[string]any{"token": token, "user": user})
}

// POST /api/auth/login
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		JSONError(w, 400, "Corps de requête invalide")
		return
	}

	var user struct {
		ID           string          `json:"id"`
		Nom          string          `json:"nom"`
		Prenom       string          `json:"prenom"`
		Email        string          `json:"email"`
		PasswordHash string          `json:"-"`
		Role         string          `json:"role"`
		IsActive     bool            `json:"-"`
		Vaomiera     json.RawMessage `json:"vaomiera"`
	}
	err := h.pool.QueryRow(r.Context(), `
		SELECT u.id, u.nom, u.prenom, u.email, u.password_hash, u.role, u.is_active,
		       COALESCE(json_agg(uv.vaomiera_id) FILTER (WHERE uv.vaomiera_id IS NOT NULL), '[]') AS vaomiera
		FROM users u
		LEFT JOIN user_vaomiera uv ON uv.user_id = u.id
		WHERE u.email = $1 GROUP BY u.id
	`, input.Email).Scan(
		&user.ID, &user.Nom, &user.Prenom, &user.Email,
		&user.PasswordHash, &user.Role, &user.IsActive, &user.Vaomiera,
	)
	if err != nil {
		JSONError(w, 401, "Email ou mot de passe incorrect")
		return
	}
	if !user.IsActive {
		JSONError(w, 403, "Compte désactivé")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)); err != nil {
		JSONError(w, 401, "Email ou mot de passe incorrect")
		return
	}

	token, err := middleware.SignToken(user.ID)
	if err != nil {
		JSONError(w, 500, "Erreur génération token")
		return
	}

	type loginUser struct {
		ID       string          `json:"id"`
		Nom      string          `json:"nom"`
		Prenom   string          `json:"prenom"`
		Email    string          `json:"email"`
		Role     string          `json:"role"`
		Vaomiera json.RawMessage `json:"vaomiera"`
	}
	JSON(w, 200, map[string]any{
		"token": token,
		"user":  loginUser{user.ID, user.Nom, user.Prenom, user.Email, user.Role, user.Vaomiera},
	})
}

// GET /api/auth/me
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFromCtx(r.Context())

	var user struct {
		ID            string          `json:"id"`
		Nom           string          `json:"nom"`
		Prenom        string          `json:"prenom"`
		Email         string          `json:"email"`
		Role          string          `json:"role"`
		DateNaissance *string         `json:"date_naissance"`
		AvatarURL     *string         `json:"avatar_url"`
		Vaomiera      json.RawMessage `json:"vaomiera"`
	}
	err := h.pool.QueryRow(r.Context(), `
		SELECT u.id, u.nom, u.prenom, u.email, u.role,
		       u.date_naissance::text AS date_naissance, u.avatar_url,
		       COALESCE(json_agg(json_build_object(
		           'id',v.id,'name',v.name,'short',v.short,'color',v.color,'icon',v.icon
		       )) FILTER (WHERE v.id IS NOT NULL), '[]') AS vaomiera
		FROM users u
		LEFT JOIN user_vaomiera uv ON uv.user_id = u.id
		LEFT JOIN vaomiera v ON v.id = uv.vaomiera_id
		WHERE u.id = $1 GROUP BY u.id
	`, u.ID).Scan(
		&user.ID, &user.Nom, &user.Prenom, &user.Email, &user.Role,
		&user.DateNaissance, &user.AvatarURL, &user.Vaomiera,
	)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}

	JSON(w, 200, user)
}

// POST /api/auth/forgot-password
// Génère un code à 6 chiffres (15 min), stocké hashé — ne touche PAS au mot de passe.
func (h *AuthHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		JSONError(w, 400, "Corps de requête invalide")
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))

	// Réponse identique quelle que soit l'existence de l'email — anti-énumération
	neutral := map[string]string{"message": "Si cet email existe, un code a été envoyé."}

	if !emailRe.MatchString(input.Email) {
		JSON(w, 200, neutral)
		return
	}

	var userID string
	err := h.pool.QueryRow(r.Context(),
		`SELECT id FROM users WHERE email=$1 AND is_active=true`, input.Email,
	).Scan(&userID)
	if err != nil {
		JSON(w, 200, neutral)
		return
	}

	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}
	codeStr := fmt.Sprintf("%06d", n.Int64()+100000)

	hash, err := bcrypt.GenerateFromPassword([]byte(codeStr), 10)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}

	expiresAt := time.Now().UTC().Add(15 * time.Minute)
	_, err = h.pool.Exec(r.Context(),
		`UPDATE users SET reset_code_hash=$1, reset_code_expires_at=$2 WHERE id=$3`,
		string(hash), expiresAt, userID,
	)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}

	// Envoi en arrière-plan — la réponse HTTP est renvoyée immédiatement,
	// sans attendre le serveur SMTP (évite le timeout 499 côté client).
	if mail.IsConfigured() {
		dest, code := input.Email, codeStr
		go func() {
			if emailErr := mail.SendResetCode(dest, code); emailErr != nil {
				log.Printf("[MAIL] ERREUR envoi à %s : %v", dest, emailErr)
			}
		}()
	} else {
		log.Printf("[MAIL] SMTP non configuré — code non envoyé par email à %s", input.Email)
	}

	resp := map[string]any{"message": "Si cet email existe, un code a été envoyé."}
	// debug_code uniquement en développement — jamais en production
	if os.Getenv("APP_ENV") == "development" {
		resp["debug_code"] = codeStr
	}
	JSON(w, 200, resp)
}

// POST /api/auth/reset-password
// Vérifie le code et remplace le mot de passe — efface ensuite le code.
func (h *AuthHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email       string `json:"email"`
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		JSONError(w, 400, "Corps de requête invalide")
		return
	}

	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Code  = strings.TrimSpace(input.Code)

	switch {
	case len(input.Code) != 6:
		JSONError(w, 400, "Le code doit contenir 6 chiffres")
		return
	case len(input.NewPassword) < 8:
		JSONError(w, 400, "Le mot de passe doit contenir au moins 8 caractères")
		return
	case len(input.NewPassword) > 128:
		JSONError(w, 400, "Le mot de passe est trop long")
		return
	}

	var codeHash string
	err := h.pool.QueryRow(r.Context(), `
		SELECT reset_code_hash FROM users
		WHERE email=$1 AND is_active=true
		  AND reset_code_hash IS NOT NULL
		  AND reset_code_expires_at > NOW()
	`, input.Email).Scan(&codeHash)
	if err != nil {
		// Réponse volontairement vague pour ne pas indiquer si l'email existe
		JSONError(w, 400, "Code invalide ou expiré")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(codeHash), []byte(input.Code)); err != nil {
		JSONError(w, 400, "Code invalide ou expiré")
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), 12)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}

	_, err = h.pool.Exec(r.Context(), `
		UPDATE users
		SET password_hash=$1, reset_code_hash=NULL, reset_code_expires_at=NULL
		WHERE email=$2
	`, string(newHash), input.Email)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}

	JSON(w, 200, map[string]string{"message": "Mot de passe réinitialisé avec succès"})
}
