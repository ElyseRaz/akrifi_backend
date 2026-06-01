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

type EventsHandler struct {
	pool *pgxpool.Pool
}

func NewEventsHandler(pool *pgxpool.Pool) *EventsHandler {
	return &EventsHandler{pool: pool}
}

type event struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Location    *string   `json:"location"`
	EventDate   string    `json:"event_date"`
	EventTime   *string   `json:"event_time"`
	Tag         *string   `json:"tag"`
	Color       *string   `json:"color"`
	Description *string   `json:"description"`
	CreatedBy   *string   `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Nom         *string   `json:"nom"`
	Prenom      *string   `json:"prenom"`
}

// GET /api/events
func (h *EventsHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	year := q.Get("year")
	month := q.Get("month")
	since := q.Get("since")

	where := []string{"1=1"}
	args := []any{}

	if year != "" && month != "" {
		args = append(args, year, month)
		where = append(where,
			fmt.Sprintf("EXTRACT(YEAR FROM e.event_date) = $%d", len(args)-1),
			fmt.Sprintf("EXTRACT(MONTH FROM e.event_date) = $%d", len(args)),
		)
	}
	if since != "" {
		args = append(args, since)
		where = append(where, fmt.Sprintf("e.updated_at > $%d", len(args)))
	}

	sql := fmt.Sprintf(`
		SELECT e.id, e.title, e.location, e.event_date::text, e.event_time::text,
		       e.tag, e.color, e.description, e.created_by, e.created_at, e.updated_at,
		       u.nom, u.prenom
		FROM events e
		LEFT JOIN users u ON u.id = e.created_by
		WHERE %s
		ORDER BY e.event_date, e.event_time
	`, strings.Join(where, " AND "))

	rows, err := h.pool.Query(r.Context(), sql, args...)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}
	defer rows.Close()

	events := []event{}
	for rows.Next() {
		var ev event
		if err := rows.Scan(
			&ev.ID, &ev.Title, &ev.Location, &ev.EventDate, &ev.EventTime,
			&ev.Tag, &ev.Color, &ev.Description, &ev.CreatedBy, &ev.CreatedAt, &ev.UpdatedAt,
			&ev.Nom, &ev.Prenom,
		); err != nil {
			JSONError(w, 500, "Erreur serveur")
			return
		}
		events = append(events, ev)
	}
	JSON(w, 200, events)
}

// GET /api/events/:id
func (h *EventsHandler) GetOne(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var ev event
	err := h.pool.QueryRow(r.Context(), `
		SELECT e.id, e.title, e.location, e.event_date::text, e.event_time::text,
		       e.tag, e.color, e.description, e.created_by, e.created_at, e.updated_at,
		       u.nom, u.prenom
		FROM events e
		LEFT JOIN users u ON u.id = e.created_by
		WHERE e.id = $1
	`, id).Scan(
		&ev.ID, &ev.Title, &ev.Location, &ev.EventDate, &ev.EventTime,
		&ev.Tag, &ev.Color, &ev.Description, &ev.CreatedBy, &ev.CreatedAt, &ev.UpdatedAt,
		&ev.Nom, &ev.Prenom,
	)
	if err != nil {
		JSONError(w, 404, "Événement introuvable")
		return
	}
	JSON(w, 200, ev)
}

// POST /api/events
func (h *EventsHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromCtx(r.Context())
	var input struct {
		Title       string `json:"title"`
		Location    string `json:"location"`
		EventDate   string `json:"event_date"`
		EventTime   string `json:"event_time"`
		Tag         string `json:"tag"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		JSONError(w, 400, "Corps de requête invalide")
		return
	}

	var ev event
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO events (title, location, event_date, event_time, tag, color, description, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, title, location, event_date::text, event_time::text, tag, color, description, created_by, created_at, updated_at
	`, input.Title, NullStr(input.Location), input.EventDate, NullStr(input.EventTime),
		NullStr(input.Tag), NullStr(input.Color), NullStr(input.Description), user.ID,
	).Scan(
		&ev.ID, &ev.Title, &ev.Location, &ev.EventDate, &ev.EventTime,
		&ev.Tag, &ev.Color, &ev.Description, &ev.CreatedBy, &ev.CreatedAt, &ev.UpdatedAt,
	)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}

	payload, _ := json.Marshal(ev)
	h.pool.Exec(r.Context(),
		`INSERT INTO sync_log (table_name, record_id, action, payload) VALUES ('events',$1,'INSERT',$2)`,
		ev.ID, payload,
	)

	JSON(w, 201, ev)
}

// PUT /api/events/:id
func (h *EventsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var input struct {
		Title       *string `json:"title"`
		Location    *string `json:"location"`
		EventDate   *string `json:"event_date"`
		EventTime   *string `json:"event_time"`
		Tag         *string `json:"tag"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		JSONError(w, 400, "Corps de requête invalide")
		return
	}

	var ev event
	err := h.pool.QueryRow(r.Context(), `
		UPDATE events
		SET title=COALESCE($1,title), location=COALESCE($2,location),
		    event_date=COALESCE($3,event_date), event_time=COALESCE($4,event_time),
		    tag=COALESCE($5,tag), description=COALESCE($6,description)
		WHERE id=$7
		RETURNING id, title, location, event_date::text, event_time::text, tag, color, description, created_by, created_at, updated_at
	`, input.Title, input.Location, input.EventDate, input.EventTime,
		input.Tag, input.Description, id,
	).Scan(
		&ev.ID, &ev.Title, &ev.Location, &ev.EventDate, &ev.EventTime,
		&ev.Tag, &ev.Color, &ev.Description, &ev.CreatedBy, &ev.CreatedAt, &ev.UpdatedAt,
	)
	if err != nil {
		JSONError(w, 404, "Événement introuvable")
		return
	}

	payload, _ := json.Marshal(ev)
	h.pool.Exec(r.Context(),
		`INSERT INTO sync_log (table_name, record_id, action, payload) VALUES ('events',$1,'UPDATE',$2)`,
		ev.ID, payload,
	)

	JSON(w, 200, ev)
}

// DELETE /api/events/:id
func (h *EventsHandler) Remove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var deletedID string
	err := h.pool.QueryRow(r.Context(),
		`DELETE FROM events WHERE id=$1 RETURNING id`, id,
	).Scan(&deletedID)
	if err != nil {
		JSONError(w, 404, "Événement introuvable")
		return
	}

	h.pool.Exec(r.Context(),
		`INSERT INTO sync_log (table_name, record_id, action) VALUES ('events',$1,'DELETE')`, id)

	JSON(w, 200, map[string]string{"message": "Événement supprimé"})
}
