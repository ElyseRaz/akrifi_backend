package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"akrifi/api/internal/middleware"
)

type NotificationsHandler struct {
	pool *pgxpool.Pool
}

func NewNotificationsHandler(pool *pgxpool.Pool) *NotificationsHandler {
	return &NotificationsHandler{pool: pool}
}

type notification struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Body          string    `json:"body"`
	FromUser      *string   `json:"from_user"`
	VaomieraID    *string   `json:"vaomiera_id"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Nom           *string   `json:"nom"`
	Prenom        *string   `json:"prenom"`
	SenderRole    *string   `json:"sender_role"`
	VaomieraShort *string   `json:"vaomiera_short"`
	VaomieraColor *string   `json:"vaomiera_color"`
	VaomieraIcon  *string   `json:"vaomiera_icon"`
	IsRead        bool      `json:"is_read"`
}

// GET /api/notifications
func (h *NotificationsHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromCtx(r.Context())
	since := r.URL.Query().Get("since")

	where := []string{"n.status = $1"}
	args := []any{"published"}

	if since != "" {
		args = append(args, since)
		where = append(where, fmt.Sprintf("n.updated_at > $%d", len(args)))
	}

	// Le user.ID est paramétrisé une seule fois et réutilisé dans les deux sous-requêtes
	args = append(args, user.ID)
	userN := len(args)

	if user.Role == "user" {
		where = append(where, fmt.Sprintf(
			"(n.vaomiera_id IS NULL OR EXISTS(SELECT 1 FROM user_vaomiera uv WHERE uv.user_id = $%d AND uv.vaomiera_id = n.vaomiera_id))",
			userN,
		))
	}

	sql := fmt.Sprintf(`
		SELECT n.id, n.title, n.body, n.from_user, n.vaomiera_id, n.status, n.created_at, n.updated_at,
		       u.nom, u.prenom, u.role AS sender_role,
		       v.short AS vaomiera_short, v.color AS vaomiera_color, v.icon AS vaomiera_icon,
		       EXISTS(SELECT 1 FROM notification_reads nr WHERE nr.notification_id = n.id AND nr.user_id = $%d) AS is_read
		FROM notifications n
		LEFT JOIN users u ON u.id = n.from_user
		LEFT JOIN vaomiera v ON v.id = n.vaomiera_id
		WHERE %s
		ORDER BY n.created_at DESC
	`, userN, strings.Join(where, " AND "))

	rows, err := h.pool.Query(r.Context(), sql, args...)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}
	defer rows.Close()

	notifs := []notification{}
	for rows.Next() {
		var n notification
		if err := rows.Scan(
			&n.ID, &n.Title, &n.Body, &n.FromUser, &n.VaomieraID, &n.Status, &n.CreatedAt, &n.UpdatedAt,
			&n.Nom, &n.Prenom, &n.SenderRole,
			&n.VaomieraShort, &n.VaomieraColor, &n.VaomieraIcon,
			&n.IsRead,
		); err != nil {
			JSONError(w, 500, "Erreur serveur")
			return
		}
		notifs = append(notifs, n)
	}
	JSON(w, 200, notifs)
}

// GET /api/notifications/:id
func (h *NotificationsHandler) GetOne(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	user := middleware.UserFromCtx(r.Context())

	var n struct {
		ID            string    `json:"id"`
		Title         string    `json:"title"`
		Body          string    `json:"body"`
		FromUser      *string   `json:"from_user"`
		VaomieraID    *string   `json:"vaomiera_id"`
		Status        string    `json:"status"`
		CreatedAt     time.Time `json:"created_at"`
		UpdatedAt     time.Time `json:"updated_at"`
		Nom           *string   `json:"nom"`
		Prenom        *string   `json:"prenom"`
		SenderRole    *string   `json:"sender_role"`
		VaomieraName  *string   `json:"vaomiera_name"`
		VaomieraShort *string   `json:"vaomiera_short"`
		VaomieraColor *string   `json:"vaomiera_color"`
	}
	err := h.pool.QueryRow(r.Context(), `
		SELECT n.id, n.title, n.body, n.from_user, n.vaomiera_id, n.status, n.created_at, n.updated_at,
		       u.nom, u.prenom, u.role AS sender_role,
		       v.name AS vaomiera_name, v.short AS vaomiera_short, v.color AS vaomiera_color
		FROM notifications n
		LEFT JOIN users u ON u.id = n.from_user
		LEFT JOIN vaomiera v ON v.id = n.vaomiera_id
		WHERE n.id = $1 AND n.status = 'published'
	`, id).Scan(
		&n.ID, &n.Title, &n.Body, &n.FromUser, &n.VaomieraID, &n.Status, &n.CreatedAt, &n.UpdatedAt,
		&n.Nom, &n.Prenom, &n.SenderRole,
		&n.VaomieraName, &n.VaomieraShort, &n.VaomieraColor,
	)
	if err != nil {
		JSONError(w, 404, "Notification introuvable")
		return
	}

	// Marquer comme lu
	h.pool.Exec(r.Context(),
		`INSERT INTO notification_reads (user_id, notification_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		user.ID, id,
	)

	JSON(w, 200, n)
}

// POST /api/notifications
func (h *NotificationsHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromCtx(r.Context())
	var input struct {
		Title      string  `json:"title"`
		Body       string  `json:"body"`
		VaomieraID *string `json:"vaomiera_id"`
		Status     string  `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		JSONError(w, 400, "Corps de requête invalide")
		return
	}

	if input.Status == "" {
		input.Status = "published"
	}

	var n notification
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO notifications (title, body, from_user, vaomiera_id, status)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, title, body, from_user, vaomiera_id, status, created_at, updated_at
	`, input.Title, input.Body, user.ID, input.VaomieraID, input.Status,
	).Scan(
		&n.ID, &n.Title, &n.Body, &n.FromUser, &n.VaomieraID, &n.Status, &n.CreatedAt, &n.UpdatedAt,
	)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}

	payload, _ := json.Marshal(n)
	h.pool.Exec(r.Context(),
		`INSERT INTO sync_log (table_name, record_id, action, payload) VALUES ('notifications',$1,'INSERT',$2)`,
		n.ID, payload,
	)

	JSON(w, 201, n)
}

// PUT /api/notifications/:id
func (h *NotificationsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	user := middleware.UserFromCtx(r.Context())
	var input struct {
		Title  *string `json:"title"`
		Body   *string `json:"body"`
		Status *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		JSONError(w, 400, "Corps de requête invalide")
		return
	}

	var n notification
	err := h.pool.QueryRow(r.Context(), `
		UPDATE notifications
		SET title=COALESCE($1,title), body=COALESCE($2,body), status=COALESCE($3,status)
		WHERE id=$4 AND from_user=$5
		RETURNING id, title, body, from_user, vaomiera_id, status, created_at, updated_at
	`, input.Title, input.Body, input.Status, id, user.ID,
	).Scan(
		&n.ID, &n.Title, &n.Body, &n.FromUser, &n.VaomieraID, &n.Status, &n.CreatedAt, &n.UpdatedAt,
	)
	if err != nil {
		JSONError(w, 404, "Notification introuvable ou accès refusé")
		return
	}

	payload, _ := json.Marshal(n)
	h.pool.Exec(r.Context(),
		`INSERT INTO sync_log (table_name, record_id, action, payload) VALUES ('notifications',$1,'UPDATE',$2)`,
		n.ID, payload,
	)

	JSON(w, 200, n)
}

// POST /api/notifications/mark-all-read
func (h *NotificationsHandler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromCtx(r.Context())
	h.pool.Exec(r.Context(), `
		INSERT INTO notification_reads (user_id, notification_id)
		SELECT $1, id FROM notifications WHERE status='published'
		ON CONFLICT DO NOTHING
	`, user.ID)
	JSON(w, 200, map[string]string{"message": "Toutes les notifications marquées comme lues"})
}
