package middleware

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"akrifi/api/internal/httputil"
)

type contextKey string

const userKey contextKey = "auth_user"

type AuthUser struct {
	ID     string `json:"id"`
	Nom    string `json:"nom"`
	Prenom string `json:"prenom"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

type Claims struct {
	UserID string `json:"userId"`
	jwt.RegisteredClaims
}

type AuthMiddleware struct {
	pool *pgxpool.Pool
}

func NewAuthMiddleware(pool *pgxpool.Pool) *AuthMiddleware {
	return &AuthMiddleware{pool: pool}
}

func (m *AuthMiddleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			httputil.JSONError(w, 401, "Token manquant ou invalide")
			return
		}

		tokenStr := strings.TrimPrefix(header, "Bearer ")
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
			return []byte(os.Getenv("JWT_SECRET")), nil
		})
		if err != nil || !token.Valid {
			httputil.JSONError(w, 401, "Token invalide ou expiré")
			return
		}

		var u AuthUser
		var isActive bool
		err = m.pool.QueryRow(r.Context(),
			`SELECT id, nom, prenom, email, role, is_active FROM users WHERE id = $1`,
			claims.UserID,
		).Scan(&u.ID, &u.Nom, &u.Prenom, &u.Email, &u.Role, &isActive)

		if err != nil || !isActive {
			httputil.JSONError(w, 401, "Utilisateur non trouvé ou désactivé")
			return
		}

		ctx := context.WithValue(r.Context(), userKey, &u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *AuthMiddleware) RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFromCtx(r.Context())
			for _, role := range roles {
				if u != nil && u.Role == role {
					next.ServeHTTP(w, r)
					return
				}
			}
			httputil.JSONError(w, 403, "Accès refusé — droits insuffisants")
		})
	}
}

func UserFromCtx(ctx context.Context) *AuthUser {
	u, _ := ctx.Value(userKey).(*AuthUser)
	return u
}

// SignToken crée un JWT avec la durée configurée (ex: "7d", "24h").
func SignToken(userID string) (string, error) {
	secret := os.Getenv("JWT_SECRET")
	dur := parseJWTDuration(os.Getenv("JWT_EXPIRES_IN"))

	claims := Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(dur)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

func parseJWTDuration(s string) time.Duration {
	if strings.HasSuffix(s, "d") {
		if days, err := strconv.Atoi(strings.TrimSuffix(s, "d")); err == nil {
			return time.Duration(days) * 24 * time.Hour
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return 7 * 24 * time.Hour
}
