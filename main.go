package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/joho/godotenv"

	"akrifi/api/internal/config"
	"akrifi/api/internal/handlers"
	"akrifi/api/internal/middleware"
)

func main() {
	godotenv.Load()

	// Refus de démarrer sans JWT_SECRET — un secret vide permettrait de forger n'importe quel token
	if os.Getenv("JWT_SECRET") == "" {
		log.Fatal("FATAL: la variable d'environnement JWT_SECRET est vide ou absente. Démarrage annulé.")
	}

	port := getEnv("PORT", "3000")

	ctx := context.Background()
	pool, err := config.NewPool(ctx)
	if err != nil {
		log.Fatalf("Connexion DB échouée : %v", err)
	}
	defer pool.Close()

	if err := config.InitDB(ctx, pool); err != nil {
		log.Fatalf("Initialisation DB échouée : %v", err)
	}

	supabase := config.NewSupabaseClient()

	os.MkdirAll(getEnv("UPLOAD_DIR", "uploads/partitions"), 0755)

	authMW := middleware.NewAuthMiddleware(pool)
	authH := handlers.NewAuthHandler(pool)
	songsH := handlers.NewSongsHandler(pool, supabase)
	usersH := handlers.NewUsersHandler(pool)
	eventsH := handlers.NewEventsHandler(pool)
	notifsH := handlers.NewNotificationsHandler(pool)

	r := chi.NewRouter()

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type"},
	}))
	r.Use(securityHeaders)
	r.Use(requestLogger)

	r.Handle("/uploads/*", http.StripPrefix("/uploads", http.FileServer(http.Dir("uploads"))))

	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		handlers.JSON(w, 200, map[string]string{"status": "ok", "version": "1.0.0", "app": "AKRIFI API"})
	})
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		handlers.JSON(w, 200, map[string]string{"status": "ok", "version": "1.0.0", "app": "AKRIFI API"})
	})
	r.Get("/api/sync/timestamp", func(w http.ResponseWriter, r *http.Request) {
		handlers.JSON(w, 200, map[string]string{"timestamp": time.Now().UTC().Format(time.RFC3339)})
	})

	// Auth
	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/register", authH.Register)
		r.Post("/login", authH.Login)
		r.Post("/forgot-password", authH.ForgotPassword)
		r.Post("/reset-password", authH.ResetPassword)
		r.Group(func(r chi.Router) {
			r.Use(authMW.Authenticate)
			r.Get("/me", authH.Me)
		})
	})

	// Songs
	r.Route("/api/songs", func(r chi.Router) {
		r.Use(authMW.Authenticate)
		r.Get("/", songsH.List)
		r.Get("/categories", songsH.Categories)
		r.Post("/{id}/favorite", songsH.ToggleFavorite)
		r.Get("/{id}", songsH.GetOne)
		r.Group(func(r chi.Router) {
			r.Use(authMW.RequireRole("admin", "super"))
			r.Post("/", songsH.Create)
			r.Put("/{id}", songsH.Update)
		})
		r.Group(func(r chi.Router) {
			r.Use(authMW.RequireRole("super"))
			r.Delete("/{id}", songsH.Remove)
		})
	})

	// Users
	r.Route("/api/users", func(r chi.Router) {
		r.Use(authMW.Authenticate)
		r.Put("/me/profile", usersH.UpdateMyProfile)
		r.Put("/me/password", usersH.ChangePassword)
		r.Group(func(r chi.Router) {
			r.Use(authMW.RequireRole("admin", "super"))
			r.Get("/stats", usersH.Stats)
			r.Get("/{id}", usersH.GetOne)
		})
		r.Group(func(r chi.Router) {
			r.Use(authMW.RequireRole("super"))
			r.Get("/", usersH.List)
			r.Put("/{id}/role", usersH.UpdateRole)
			r.Put("/{id}/profile", usersH.UpdateProfile)
			r.Delete("/{id}", usersH.Deactivate)
		})
	})

	// Events
	r.Route("/api/events", func(r chi.Router) {
		r.Use(authMW.Authenticate)
		r.Get("/", eventsH.List)
		r.Get("/{id}", eventsH.GetOne)
		r.Group(func(r chi.Router) {
			r.Use(authMW.RequireRole("admin", "super"))
			r.Post("/", eventsH.Create)
			r.Put("/{id}", eventsH.Update)
		})
		r.Group(func(r chi.Router) {
			r.Use(authMW.RequireRole("super"))
			r.Delete("/{id}", eventsH.Remove)
		})
	})

	// Notifications
	r.Route("/api/notifications", func(r chi.Router) {
		r.Use(authMW.Authenticate)
		r.Get("/", notifsH.List)
		r.Post("/mark-all-read", notifsH.MarkAllRead)
		r.Get("/{id}", notifsH.GetOne)
		r.Group(func(r chi.Router) {
			r.Use(authMW.RequireRole("admin", "super"))
			r.Post("/", notifsH.Create)
			r.Put("/{id}", notifsH.Update)
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		handlers.JSONError(w, 404, "Endpoint introuvable")
	})

	fmt.Printf("\n🎶 AKRIFI API (Go) démarrée sur http://localhost:%s\n", port)
	fmt.Printf("   Environnement : %s\n\n", getEnv("NODE_ENV", "development"))

	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("Serveur échoué : %v", err)
	}
}

type wrappedWriter struct {
	http.ResponseWriter
	status int
}

func (ww *wrappedWriter) WriteHeader(code int) {
	ww.status = code
	ww.ResponseWriter.WriteHeader(code)
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &wrappedWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(ww, r)
		ms := time.Since(start).Milliseconds()
		level := "INFO"
		if ww.status >= 500 {
			level = "ERROR"
		} else if ww.status >= 400 {
			level = "WARN"
		}
		log.Printf("[%s] %s %s %d %dms ip=%s", level, r.Method, r.URL.Path, ww.status, ms, r.RemoteAddr)
	})
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// securityHeaders ajoute les en-têtes HTTP de sécurité recommandés.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		// Limite la taille du body à 2 Mo pour prévenir les attaques DoS
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		next.ServeHTTP(w, r)
	})
}
