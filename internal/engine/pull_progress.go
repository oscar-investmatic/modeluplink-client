package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// PullProgress reports bytes for the current model file, not the entire setup.
type PullProgress struct {
	Status    string `json:"status"`
	Digest    string `json:"digest"`
	Total     int64  `json:"total"`
	Completed int64  `json:"completed"`
	Error     string `json:"error"`
}

// PullModelWithProgress uses Ollama's NDJSON stream. A closed stream without
// success is a failure, even if every reported byte has been downloaded.
func PullModelWithProgress(ctx context.Context, baseURL, model string, report func(PullProgress)) error {
	body, err := json.Marshal(map[string]any{"model": model, "stream": true})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 2 * time.Hour}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("model download returned HTTP %d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var last PullProgress
	var lastAt time.Time
	for scanner.Scan() {
		var p PullProgress
		if err := json.Unmarshal(scanner.Bytes(), &p); err != nil {
			return fmt.Errorf("read download progress: %w", err)
		}
		if p.Error != "" {
			return errors.New(p.Error)
		}
		if p.Completed < 0 || p.Total < 0 || p.Completed > p.Total {
			return errors.New("invalid model download progress")
		}
		if report != nil && (p.Status != last.Status || p.Digest != last.Digest || p.Completed == p.Total || time.Since(lastAt) >= 250*time.Millisecond) {
			report(p)
			lastAt = time.Now()
			last = p
		}
		if p.Status == "success" {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("model download ended before completion")
}
