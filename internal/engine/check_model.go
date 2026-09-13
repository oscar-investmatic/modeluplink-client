package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/internal/service"
)

// checkModelTimeout allows for a cold model load on a slow disk.
const checkModelTimeout = 4 * time.Minute

// CheckModel verifies a local Ollama generation and requests unloading afterward
// with keep_alive=0. It does not send inference through the hosted service.
func CheckModel(ctx context.Context, baseURL, model string) error {
	ctx, cancel := context.WithTimeout(ctx, checkModelTimeout)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"model": model, "prompt": "Reply OK.", "stream": false, "keep_alive": 0, "options": map[string]int{"num_predict": 1, "num_ctx": 2048}})
	request, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(baseURL, "/")+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var result struct {
		Response string `json:"response"`
		// A one-token reasoning-model probe can finish without visible text.
		// Accept completion metadata as well as response or thinking content.
		Thinking   string `json:"thinking"`
		Done       bool   `json:"done"`
		DoneReason string `json:"done_reason"`
		EvalCount  int    `json:"eval_count"`
		Error      string `json:"error"`
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result) != nil || result.Error != "" || !result.Done {
		return errors.New("the selected model could not answer on this computer; choose a smaller model or free memory")
	}
	if result.Response == "" && result.Thinking == "" && result.DoneReason == "" && result.EvalCount == 0 {
		return errors.New("the selected model could not answer on this computer; choose a smaller model or free memory")
	}
	return nil
}

// ConfigureManagedOllama applies conservative settings to our managed Ollama
// service, then waits for it to become available.
func ConfigureManagedOllama(ctx context.Context) error {
	path, err := findOllama()
	if err != nil {
		return err
	}
	changed, err := service.RefreshManagedOllama(path)
	if err != nil || !changed {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	for {
		if _, err = Probe(ctx, "http://127.0.0.1:11434"); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
