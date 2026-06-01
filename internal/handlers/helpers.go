package handlers

import (
	"net/http"
	"os"
	"regexp"
	"strconv"

	"akrifi/api/internal/httputil"
)

// JSON et JSONError sont des alias vers httputil pour la commodité interne.
func JSON(w http.ResponseWriter, status int, data any) {
	httputil.JSON(w, status, data)
}

func JSONError(w http.ResponseWriter, status int, message string) {
	httputil.JSONError(w, status, message)
}

func GetEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func GetEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func NullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ParseDate convertit DD/MM/YYYY → YYYY-MM-DD (ou retourne la valeur telle quelle).
func ParseDate(value string) *string {
	if value == "" {
		return nil
	}
	re := regexp.MustCompile(`^(\d{2})/(\d{2})/(\d{4})$`)
	if m := re.FindStringSubmatch(value); m != nil {
		s := m[3] + "-" + m[2] + "-" + m[1]
		return &s
	}
	return &value
}
