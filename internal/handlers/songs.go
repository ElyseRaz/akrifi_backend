package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"akrifi/api/internal/config"
	"akrifi/api/internal/middleware"
)

type SongsHandler struct {
	pool    *pgxpool.Pool
	supabase *config.SupabaseClient
}

func NewSongsHandler(pool *pgxpool.Pool, supabase *config.SupabaseClient) *SongsHandler {
	return &SongsHandler{pool: pool, supabase: supabase}
}

type Song struct {
	ID         string    `json:"id"`
	Numero     *string   `json:"numero"`
	Title      string    `json:"title"`
	Composer   *string   `json:"composer"`
	Category   *string   `json:"category"`
	Tonalite   *string   `json:"tonalite"`
	Lang       *string   `json:"lang"`
	Paroles    *string   `json:"paroles"`
	FileURL    *string   `json:"file_url"`
	FileSize   *int32    `json:"file_size"`
	FileType   *string   `json:"file_type"`
	CreatedBy  *string   `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	IsFavorite bool      `json:"is_favorite"`
}

// GET /api/songs
func (h *SongsHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromCtx(r.Context())
	q := r.URL.Query()

	search := q.Get("search")
	category := q.Get("category")
	composer := q.Get("composer")
	since := q.Get("since")
	sortField := q.Get("sort")
	sortOrder := q.Get("order")
	limit := 50
	offset := 0
	if n, err := strconv.Atoi(q.Get("limit")); err == nil {
		limit = n
	}
	if n, err := strconv.Atoi(q.Get("offset")); err == nil {
		offset = n
	}

	validSorts := map[string]bool{"created_at": true, "title": true, "numero": true, "updated_at": true}
	if !validSorts[sortField] {
		sortField = "created_at"
	}
	if sortOrder != "ASC" {
		sortOrder = "DESC"
	}

	where := []string{"1=1"}
	args := []any{}

	if search != "" {
		args = append(args, "%"+search+"%")
		n := len(args)
		where = append(where, fmt.Sprintf("(s.title ILIKE $%d OR s.numero ILIKE $%d OR s.composer ILIKE $%d)", n, n, n))
	}
	if category != "" {
		args = append(args, category)
		where = append(where, fmt.Sprintf("s.category = $%d", len(args)))
	}
	if composer != "" {
		args = append(args, composer)
		where = append(where, fmt.Sprintf("s.composer = $%d", len(args)))
	}
	if since != "" {
		args = append(args, since)
		where = append(where, fmt.Sprintf("s.updated_at > $%d", len(args)))
	}

	args = append(args, user.ID)
	userN := len(args)
	args = append(args, limit, offset)
	limitN := len(args) - 1
	offsetN := len(args)

	sql := fmt.Sprintf(`
		SELECT s.id, s.numero, s.title, s.composer, s.category, s.tonalite, s.lang,
		       s.paroles, s.file_url, s.file_size, s.file_type, s.created_by,
		       s.created_at, s.updated_at,
		       EXISTS(SELECT 1 FROM song_favorites sf WHERE sf.song_id = s.id AND sf.user_id = $%d) AS is_favorite
		FROM songs s
		WHERE %s
		ORDER BY s.%s %s
		LIMIT $%d OFFSET $%d
	`, userN, strings.Join(where, " AND "), sortField, sortOrder, limitN, offsetN)

	rows, err := h.pool.Query(r.Context(), sql, args...)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}
	defer rows.Close()

	songs := []Song{}
	for rows.Next() {
		var s Song
		if err := rows.Scan(
			&s.ID, &s.Numero, &s.Title, &s.Composer, &s.Category, &s.Tonalite, &s.Lang,
			&s.Paroles, &s.FileURL, &s.FileSize, &s.FileType, &s.CreatedBy,
			&s.CreatedAt, &s.UpdatedAt, &s.IsFavorite,
		); err != nil {
			JSONError(w, 500, "Erreur serveur")
			return
		}
		songs = append(songs, s)
	}
	if err := rows.Err(); err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}

	JSON(w, 200, map[string]any{"data": songs, "total": len(songs)})
}

// GET /api/songs/categories
func (h *SongsHandler) Categories(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT DISTINCT category FROM songs WHERE category IS NOT NULL ORDER BY category`)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}
	defer rows.Close()

	cats := []string{}
	for rows.Next() {
		var c string
		rows.Scan(&c)
		cats = append(cats, c)
	}
	JSON(w, 200, cats)
}

// GET /api/songs/:id
func (h *SongsHandler) GetOne(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	user := middleware.UserFromCtx(r.Context())

	var s Song
	err := h.pool.QueryRow(r.Context(), `
		SELECT s.id, s.numero, s.title, s.composer, s.category, s.tonalite, s.lang,
		       s.paroles, s.file_url, s.file_size, s.file_type, s.created_by,
		       s.created_at, s.updated_at,
		       EXISTS(SELECT 1 FROM song_favorites sf WHERE sf.song_id = s.id AND sf.user_id = $2) AS is_favorite
		FROM songs s WHERE s.id = $1
	`, id, user.ID).Scan(
		&s.ID, &s.Numero, &s.Title, &s.Composer, &s.Category, &s.Tonalite, &s.Lang,
		&s.Paroles, &s.FileURL, &s.FileSize, &s.FileType, &s.CreatedBy,
		&s.CreatedAt, &s.UpdatedAt, &s.IsFavorite,
	)
	if err != nil {
		JSONError(w, 404, "Partition introuvable")
		return
	}
	JSON(w, 200, s)
}

// POST /api/songs
func (h *SongsHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromCtx(r.Context())
	maxSize := int64(GetEnvInt("MAX_FILE_SIZE", 1048576))

	var numero, title, composer, category, tonalite, lang, paroles string
	var fileURL *string
	var fileSize *int32
	var fileType *string

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxSize); err != nil {
			JSONError(w, 400, "Formulaire invalide")
			return
		}
		numero = r.FormValue("numero")
		title = r.FormValue("title")
		composer = r.FormValue("composer")
		category = r.FormValue("category")
		tonalite = r.FormValue("tonalite")
		lang = r.FormValue("lang")
		paroles = r.FormValue("paroles")

		fu, fs, ft, err := handleFileUpload(r, h.supabase, maxSize)
		if err != nil {
			JSONError(w, 400, err.Error())
			return
		}
		if fu != "" {
			fileURL = &fu
			fileSize = &fs
			fileType = &ft
		}
	} else {
		var input struct {
			Numero   string `json:"numero"`
			Title    string `json:"title"`
			Composer string `json:"composer"`
			Category string `json:"category"`
			Tonalite string `json:"tonalite"`
			Lang     string `json:"lang"`
			Paroles  string `json:"paroles"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			JSONError(w, 400, "Corps de requête invalide")
			return
		}
		numero = input.Numero
		title = input.Title
		composer = input.Composer
		category = input.Category
		tonalite = input.Tonalite
		lang = input.Lang
		paroles = input.Paroles
	}

	if title == "" {
		JSONError(w, 400, "Le titre est requis")
		return
	}
	langVal := lang
	if langVal == "" {
		langVal = "mg"
	}

	var s Song
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO songs (numero, title, composer, category, tonalite, lang, paroles, file_url, file_size, file_type, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id, numero, title, composer, category, tonalite, lang, paroles, file_url, file_size, file_type, created_by, created_at, updated_at
	`, NullStr(numero), title, NullStr(composer), NullStr(category), NullStr(tonalite),
		langVal, NullStr(paroles), fileURL, fileSize, fileType, user.ID,
	).Scan(
		&s.ID, &s.Numero, &s.Title, &s.Composer, &s.Category, &s.Tonalite, &s.Lang,
		&s.Paroles, &s.FileURL, &s.FileSize, &s.FileType, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		JSONError(w, 500, "Erreur serveur")
		return
	}

	payload, _ := json.Marshal(s)
	h.pool.Exec(r.Context(),
		`INSERT INTO sync_log (table_name, record_id, action, payload) VALUES ('songs',$1,'INSERT',$2)`,
		s.ID, payload,
	)

	JSON(w, 201, s)
}

// PUT /api/songs/:id
func (h *SongsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	maxSize := int64(GetEnvInt("MAX_FILE_SIZE", 1048576))

	if err := r.ParseMultipartForm(maxSize); err != nil {
		// corps JSON sans fichier possible
	}

	setClauses := []string{}
	args := []any{}

	add := func(col string, val any) {
		args = append(args, val)
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", col, len(args)))
	}

	if r.MultipartForm != nil {
		if _, ok := r.MultipartForm.Value["numero"]; ok {
			add("numero", NullStr(r.FormValue("numero")))
		}
		if v := r.FormValue("title"); v != "" {
			add("title", v)
		}
		if _, ok := r.MultipartForm.Value["composer"]; ok {
			add("composer", NullStr(r.FormValue("composer")))
		}
		if v := r.FormValue("category"); v != "" {
			add("category", v)
		}
		if _, ok := r.MultipartForm.Value["tonalite"]; ok {
			add("tonalite", NullStr(r.FormValue("tonalite")))
		}
		if _, ok := r.MultipartForm.Value["paroles"]; ok {
			add("paroles", NullStr(r.FormValue("paroles")))
		}
	}

	// Fichier optionnel
	file, header, ferr := r.FormFile("file")
	if ferr == nil {
		defer file.Close()
		ext := strings.ToLower(filepath.Ext(header.Filename))
		allowed := map[string]bool{".pdf": true, ".jpg": true, ".jpeg": true, ".png": true}
		if !allowed[ext] {
			JSONError(w, 400, "Seuls les fichiers PDF, JPG et PNG sont acceptés")
			return
		}
		buf, err := io.ReadAll(io.LimitReader(file, maxSize+1))
		if err != nil {
			JSONError(w, 500, "Erreur lecture fichier")
			return
		}
		if int64(len(buf)) > maxSize {
			JSONError(w, 400, fmt.Sprintf("Fichier trop volumineux. Maximum : %d Ko", maxSize/1024))
			return
		}
		// Supprimer l'ancien fichier
		var oldURL *string
		h.pool.QueryRow(r.Context(), `SELECT file_url FROM songs WHERE id=$1`, id).Scan(&oldURL)
		if oldURL != nil {
			h.supabase.DeleteFile(r.Context(), *oldURL)
		}

		ct := header.Header.Get("Content-Type")
		if ct == "" {
			ct = mime.TypeByExtension(ext)
		}
		filename := fmt.Sprintf("%d-%d%s", time.Now().UnixMilli(), rand.Int63n(1e9), ext)
		url, err := h.supabase.UploadFile(r.Context(), buf, filename, ct)
		if err != nil {
			JSONError(w, 500, "Erreur upload fichier")
			return
		}
		sz := int32(len(buf))
		ft := strings.ToUpper(strings.TrimPrefix(ext, "."))
		add("file_url", url)
		add("file_size", sz)
		add("file_type", ft)
	}

	if len(setClauses) == 0 {
		JSONError(w, 400, "Aucun champ à mettre à jour")
		return
	}

	args = append(args, id)
	sql := fmt.Sprintf(`
		UPDATE songs SET %s WHERE id = $%d
		RETURNING id, numero, title, composer, category, tonalite, lang, paroles, file_url, file_size, file_type, created_by, created_at, updated_at
	`, strings.Join(setClauses, ", "), len(args))

	var s Song
	err := h.pool.QueryRow(r.Context(), sql, args...).Scan(
		&s.ID, &s.Numero, &s.Title, &s.Composer, &s.Category, &s.Tonalite, &s.Lang,
		&s.Paroles, &s.FileURL, &s.FileSize, &s.FileType, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		JSONError(w, 404, "Partition introuvable")
		return
	}

	payload, _ := json.Marshal(s)
	h.pool.Exec(r.Context(),
		`INSERT INTO sync_log (table_name, record_id, action, payload) VALUES ('songs',$1,'UPDATE',$2)`,
		s.ID, payload,
	)

	JSON(w, 200, s)
}

// DELETE /api/songs/:id
func (h *SongsHandler) Remove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var songID string
	var fileURL *string
	err := h.pool.QueryRow(r.Context(),
		`DELETE FROM songs WHERE id=$1 RETURNING id, file_url`, id,
	).Scan(&songID, &fileURL)
	if err != nil {
		JSONError(w, 404, "Partition introuvable")
		return
	}

	if fileURL != nil {
		h.supabase.DeleteFile(r.Context(), *fileURL)
	}
	h.pool.Exec(r.Context(),
		`INSERT INTO sync_log (table_name, record_id, action) VALUES ('songs',$1,'DELETE')`, id)

	JSON(w, 200, map[string]string{"message": "Partition supprimée"})
}

// POST /api/songs/:id/favorite
func (h *SongsHandler) ToggleFavorite(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	user := middleware.UserFromCtx(r.Context())

	var exists bool
	h.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM song_favorites WHERE user_id=$1 AND song_id=$2)`,
		user.ID, id,
	).Scan(&exists)

	if exists {
		h.pool.Exec(r.Context(),
			`DELETE FROM song_favorites WHERE user_id=$1 AND song_id=$2`, user.ID, id)
		JSON(w, 200, map[string]bool{"is_favorite": false})
	} else {
		h.pool.Exec(r.Context(),
			`INSERT INTO song_favorites (user_id, song_id) VALUES ($1,$2)`, user.ID, id)
		JSON(w, 200, map[string]bool{"is_favorite": true})
	}
}

// handleFileUpload lit et valide le fichier multipart, l'envoie sur Supabase.
// Retourne (url, size, type, error).
func handleFileUpload(r *http.Request, supabase *config.SupabaseClient, maxSize int64) (string, int32, string, error) {
	file, header, err := r.FormFile("file")
	if err == http.ErrMissingFile {
		return "", 0, "", nil
	}
	if err != nil {
		return "", 0, "", fmt.Errorf("erreur lecture fichier")
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowed := map[string]bool{".pdf": true, ".jpg": true, ".jpeg": true, ".png": true}
	if !allowed[ext] {
		return "", 0, "", fmt.Errorf("seuls les fichiers PDF, JPG et PNG sont acceptés")
	}

	buf, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return "", 0, "", fmt.Errorf("erreur lecture fichier")
	}
	if int64(len(buf)) > maxSize {
		return "", 0, "", fmt.Errorf("fichier trop volumineux. Maximum : %d Ko", maxSize/1024)
	}

	ct := header.Header.Get("Content-Type")
	if ct == "" {
		ct = mime.TypeByExtension(ext)
	}
	filename := fmt.Sprintf("%d-%d%s", time.Now().UnixMilli(), rand.Int63n(1e9), ext)

	url, err := supabase.UploadFile(r.Context(), buf, filename, ct)
	if err != nil {
		return "", 0, "", fmt.Errorf("erreur upload fichier")
	}

	sz := int32(len(buf))
	ft := strings.ToUpper(strings.TrimPrefix(ext, "."))
	return url, sz, ft, nil
}
