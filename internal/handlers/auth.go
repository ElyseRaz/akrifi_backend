package handlers

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"akrifi/api/internal/middleware"
)

type AuthHandler struct {
	pool *pgxpool.Pool
}

func NewAuthHandler(pool *pgxpool.Pool) *AuthHandler {
	return &AuthHandler{pool: pool}
}

// POST /api/auth/register
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Nom          string   `json:"nom"`
		Prenom       string   `json:"prenom"`
		Email        string   `json:"email"`
		Password     string   `json:"password"`
		DateNaissance string  `json:"date_naissance"`
		VaomieraIDs  []string `json:"vaomiera_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		JSONError(w, 400, "Corps de requête invalide")
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
func (h *AuthHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		JSONError(w, 400, "Corps de requête invalide")
		return
	}

	var userID string
	err := h.pool.QueryRow(r.Context(), `SELECT id FROM users WHERE email=$1`, input.Email).Scan(&userID)
	if err != nil {
		JSON(w, 200, map[string]string{"message": "Si cet email existe, un lien a été envoyé."})
		return
	}

	code := 100000 + rand.Intn(900000)
	codeStr := strconv.Itoa(code)
	hash, _ := bcrypt.GenerateFromPassword([]byte(codeStr), 10)
	h.pool.Exec(r.Context(), `UPDATE users SET password_hash=$1 WHERE email=$2`, string(hash), input.Email)

	resp := map[string]any{"message": "Si cet email existe, un lien a été envoyé."}
	if os.Getenv("NODE_ENV") == "development" {
		resp["debug_code"] = codeStr
	}
	JSON(w, 200, resp)
}
