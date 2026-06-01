package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type SupabaseClient struct {
	url            string
	serviceRoleKey string
	bucket         string
	http           *http.Client
}

func NewSupabaseClient() *SupabaseClient {
	return &SupabaseClient{
		url:            os.Getenv("SUPABASE_URL"),
		serviceRoleKey: os.Getenv("SUPABASE_SERVICE_ROLE_KEY"),
		bucket:         "partitions",
		http:           &http.Client{},
	}
}

func (s *SupabaseClient) UploadFile(ctx context.Context, data []byte, filename, contentType string) (string, error) {
	if s.url == "" {
		return "", fmt.Errorf("SUPABASE_URL non configuré")
	}

	url := fmt.Sprintf("%s/storage/v1/object/%s/%s", s.url, s.bucket, filename)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.serviceRoleKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("supabase upload échoué (%d) : %s", resp.StatusCode, string(body))
	}

	publicURL := fmt.Sprintf("%s/storage/v1/object/public/%s/%s", s.url, s.bucket, filename)
	return publicURL, nil
}

func (s *SupabaseClient) DeleteFile(ctx context.Context, fileURL string) error {
	if fileURL == "" || s.url == "" {
		return nil
	}

	marker := fmt.Sprintf("/storage/v1/object/public/%s/", s.bucket)
	idx := strings.Index(fileURL, marker)
	if idx == -1 {
		return nil
	}
	filePath := fileURL[idx+len(marker):]

	bodyData := map[string][]string{"prefixes": {filePath}}
	bodyJSON, _ := json.Marshal(bodyData)

	url := fmt.Sprintf("%s/storage/v1/object/%s", s.url, s.bucket)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, bytes.NewReader(bodyJSON))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.serviceRoleKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
