package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// chooseRegion measures relay reachability from the model computer, which owns
// the long-lived tunnel. Existing endpoints retain their assigned region.
func chooseRegion(control string) string {
	return chooseRegionWithClient(control, &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
}
func chooseRegionWithClient(control string, client *http.Client) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(control, "/")+"/v1/regions", nil)
	if err != nil {
		return "eu"
	}
	resp, err := client.Do(req)
	if err != nil {
		return "eu"
	}
	defer resp.Body.Close()
	var regions map[string]string
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&regions) != nil {
		return "eu"
	}
	type result struct {
		region  string
		elapsed time.Duration
	}
	results := make(chan result, 2)
	count := 0
	for _, region := range []string{"eu", "us"} {
		address := regions[region]
		u, err := url.Parse(address)
		if err != nil || u.Scheme != "wss" || u.Host == "" || u.User != nil {
			continue
		}
		u.Scheme = "https"
		u.Path = "/healthz"
		u.RawQuery = ""
		u.Fragment = ""
		count++
		go func(region, address string) {
			best := time.Duration(1<<63 - 1)
			for i := 0; i < 2; i++ {
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
				start := time.Now()
				resp, err := client.Do(req)
				if err != nil {
					continue
				}
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				_ = resp.Body.Close()
				if resp.StatusCode == 200 && time.Since(start) < best {
					best = time.Since(start)
				}
			}
			results <- result{region, best}
		}(region, u.String())
	}
	selected := "eu"
	best := time.Duration(1<<63 - 1)
	for i := 0; i < count; i++ {
		select {
		case r := <-results:
			if r.elapsed < best {
				selected, best = r.region, r.elapsed
			}
		case <-ctx.Done():
			return selected
		}
	}
	return selected
}
