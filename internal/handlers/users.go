package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"akrifi/api/internal/middleware"
)

type UsersHandler struct {
	pool *pgxpool.Pool
}

func NewUsersHandler(pool *pgxpool.Pool) *UsersHandler {
	return &UsersHandler{pool: pool}
}

type userRow struct {
	ID            string          `json:"id"`
	Nom           string          `json:"nom"`
	Prenom        string          `json:"prenom"`
	Email         string          `json:"email"`
	Role          string          `json:"role"`
	DateNaissance *string         `json:"date_naissance"`
	AvatarURL     *string         `json:"avatar_url"`
	CreatedAt     time.Time       `json:"created_at"`
	Vaomiera      json.RawMessage `json:"vaomiera"`
}

// GET /api/users (super)
func (h *UsersHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	roleFilter := q.Get("role")
	search := q.Get("search")

	where := []string{"u.is_active = true"}
	args := []any{}

	if roleFilter != "" {
		args = append(args, roleFilter)
		where = append(where, fmt.Sprintf("u.role = $%d", len(args)))
	}
	if search != "" {
		args = append(args, "%"+search+"%")
		n := len(args)
		where = append(where, fmt.Sprintf(
			"(u.nom ILIKE $%d OR u.prenom ILIKE $%d OR u.email ILIKE $%d)", n, n, n))
	}

	sql := fmt.Sprintf(`
		SELECT u.id, u.nom, u.prenom, u.email, u.role,
		       u.date_naissance::text AS date_naissance, u.avatar_url, u.created_at,
		       COALESCE(json_agg(json_build_object(
		           'id',v.id,'name',v.name,'short',v.short,'color',v.color
		       )) FILTER (WHERE v.id IS NOT NULL), '[]') AS vaomiera
		FROM users u
		LEFT JOIN user_vaomiera uv ON uv.user_id = u.id
		LEFT JOIN vaomiera v ON v.id = uv.vaomiera_id
		WHERE %s
		GROUP BY u.id ORDER BY u.nom, u.prenom
	`, strings.Join(where, " AND "))

	rows, err := h.pool.Query(r.Context(), sql, args...)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}
	defer rows.Close()

	users := []userRow{}
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.ID, &u.Nom, &u.Prenom, &u.Email, &u.Role,
			&u.DateNaissance, &u.AvatarURL, &u.CreatedAt, &u.Vaomiera); err != nil {
			JSONError(w, 500, "Erreur serveur")
			return
		}
		users = append(users, u)
	}
	JSON(w, 200, users)
}

// GET /api/users/stats (admin/super)
func (h *UsersHandler) Stats(w http.ResponseWriter, r *http.Request) {
	var s struct {
		TotalMembers       int64 `json:"total_members"`
		TotalAdmins        int64 `json:"total_admins"`
		TotalSuper         int64 `json:"total_super"`
		TotalSongs         int64 `json:"total_songs"`
		TotalNotifications int64 `json:"total_notifications"`
		TotalEvents        int64 `json:"total_events"`
	}
	err := h.pool.QueryRow(r.Context(), `
		SELECT
			(SELECT COUNT(*) FROM users WHERE is_active=true),
			(SELECT COUNT(*) FROM users WHERE role='admin' AND is_active=true),
			(SELECT COUNT(*) FROM users WHERE role='super' AND is_active=true),
			(SELECT COUNT(*) FROM songs),
			(SELECT COUNT(*) FROM notifications WHERE status='published'),
			(SELECT COUNT(*) FROM events)
	`).Scan(
		&s.TotalMembers, &s.TotalAdmins, &s.TotalSuper,
		&s.TotalSongs, &s.TotalNotifications, &s.TotalEvents,
	)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}
	JSON(w, 200, s)
}

// GET /api/users/:id (admin/super)
func (h *UsersHandler) GetOne(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var u userRow
	err := h.pool.QueryRow(r.Context(), `
		SELECT u.id, u.nom, u.prenom, u.email, u.role,
		       u.date_naissance::text AS date_naissance, u.avatar_url, u.created_at,
		       COALESCE(json_agg(json_build_object(
		           'id',v.id,'name',v.name,'short',v.short,'color',v.color,'icon',v.icon
		       )) FILTER (WHERE v.id IS NOT NULL), '[]') AS vaomiera
		FROM users u
		LEFT JOIN user_vaomiera uv ON uv.user_id = u.id
		LEFT JOIN vaomiera v ON v.id = uv.vaomiera_id
		WHERE u.id = $1 GROUP BY u.id
	`, id).Scan(
		&u.ID, &u.Nom, &u.Prenom, &u.Email, &u.Role,
		&u.DateNaissance, &u.AvatarURL, &u.CreatedAt, &u.Vaomiera,
	)
	if err != nil {
		JSONError(w, 404, "Utilisateur introuvable")
		return
	}
	JSON(w, 200, u)
}

// PUT /api/users/me/profile
func (h *UsersHandler) UpdateMyProfile(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFromCtx(r.Context())
	h.updateProfileFor(w, r, u.ID)
}

// PUT /api/users/:id/profile (super)
func (h *UsersHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	u := middleware.UserFromCtx(r.Context())
	if u.Role != "super" && u.ID != id {
		JSONError(w, 403, "Accès refusé")
		return
	}
	h.updateProfileFor(w, r, id)
}

func (h *UsersHandler) updateProfileFor(w http.ResponseWriter, r *http.Request, targetID string) {
	var input struct {
		Nom           string   `json:"nom"`
		Prenom        string   `json:"prenom"`
		DateNaissance string   `json:"date_naissance"`
		VaomieraIDs   []string `json:"vaomiera_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		JSONError(w, 400, "Corps de requête invalide")
		return
	}

	_, err := h.pool.Exec(r.Context(), `
		UPDATE users
		SET nom=COALESCE($1,nom), prenom=COALESCE($2,prenom), date_naissance=COALESCE($3,date_naissance)
		WHERE id=$4
	`, NullStr(input.Nom), NullStr(input.Prenom), ParseDate(input.DateNaissance), targetID)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}

	if input.VaomieraIDs != nil {
		h.pool.Exec(r.Context(), `DELETE FROM user_vaomiera WHERE user_id=$1`, targetID)
		for _, vID := range input.VaomieraIDs {
			h.pool.Exec(r.Context(),
				`INSERT INTO user_vaomiera (user_id, vaomiera_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
				targetID, vID,
			)
		}
	}

	JSON(w, 200, map[string]string{"message": "Profil mis à jour"})
}

// PUT /api/users/me/password
func (h *UsersHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFromCtx(r.Context())
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		JSONError(w, 400, "Corps de requête invalide")
		return
	}

	if len(input.NewPassword) < 8 {
		JSONError(w, 400, "Le nouveau mot de passe doit contenir au moins 8 caractères")
		return
	}
	if len(input.NewPassword) > 128 {
		JSONError(w, 400, "Le mot de passe est trop long")
		return
	}

	var hash string
	err := h.pool.QueryRow(r.Context(),
		`SELECT password_hash FROM users WHERE id=$1`, u.ID,
	).Scan(&hash)
	if err != nil {
		JSONError(w, 404, "Utilisateur introuvable")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.CurrentPassword)); err != nil {
		JSONError(w, 401, "Mot de passe actuel incorrect")
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), 12)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}

	h.pool.Exec(r.Context(), `UPDATE users SET password_hash=$1 WHERE id=$2`, string(newHash), u.ID)
	JSON(w, 200, map[string]string{"message": "Mot de passe modifié"})
}

// PUT /api/users/:id/role (super)
func (h *UsersHandler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var input struct {
		Role        string   `json:"role"`
		VaomieraIDs []string `json:"vaomiera_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		JSONError(w, 400, "Corps de requête invalide")
		return
	}

	validRoles := map[string]bool{"user": true, "admin": true, "super": true}
	if !validRoles[input.Role] {
		JSONError(w, 400, "Rôle invalide")
		return
	}

	h.pool.Exec(r.Context(), `UPDATE users SET role=$1 WHERE id=$2`, input.Role, id)

	if input.VaomieraIDs != nil {
		h.pool.Exec(r.Context(), `DELETE FROM user_vaomiera WHERE user_id=$1`, id)
		for _, vID := range input.VaomieraIDs {
			h.pool.Exec(r.Context(),
				`INSERT INTO user_vaomiera (user_id, vaomiera_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
				id, vID,
			)
		}
	}

	JSON(w, 200, map[string]string{"message": "Rôle mis à jour"})
}

// DELETE /api/users/:id (super)
func (h *UsersHandler) Deactivate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	caller := middleware.UserFromCtx(r.Context())
	if caller.ID == id {
		JSONError(w, 400, "Impossible de désactiver son propre compte")
		return
	}
	h.pool.Exec(r.Context(), `UPDATE users SET is_active=false WHERE id=$1`, id)
	JSON(w, 200, map[string]string{"message": "Utilisateur désactivé"})
}
