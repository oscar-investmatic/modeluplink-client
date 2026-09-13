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
)

func runningModels(ctx context.Context, baseURL string) ([]string, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(baseURL, "/")+"/api/ps", nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var body struct {
		Models *[]struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body) != nil || body.Models == nil {
		return nil, errors.New("could not check loaded models")
	}
	names := make([]string, 0, len(*body.Models))
	for _, model := range *body.Models {
		names = append(names, model.Name)
	}
	return names, nil
}

// UnloadModels leaves the Ollama service and downloaded files intact. It only
// returns success once none of the endpoint's shared models remain in memory.
func UnloadModels(ctx context.Context, baseURL string, models []string) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	loaded, err := runningModels(ctx, baseURL)
	if err != nil {
		return err
	}
	if models == nil {
		models = loaded
	} // legacy endpoint shared all installed models
	shared := map[string]bool{}
	for _, name := range models {
		shared[name] = true
	}
	for _, name := range loaded {
		if !shared[name] {
			continue
		}
		body, _ := json.Marshal(map[string]any{"model": name, "keep_alive": 0, "stream": false})
		request, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(baseURL, "/")+"/api/generate", bytes.NewReader(body))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return err
		}
		var result struct {
			Done bool `json:"done"`
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || !result.Done {
			return errors.New("Ollama could not unload the model")
		}
	}
	for {
		loaded, err = runningModels(ctx, baseURL)
		if err != nil {
			return err
		}
		remaining := false
		for _, name := range loaded {
			if shared[name] {
				remaining = true
			}
		}
		if !remaining {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
