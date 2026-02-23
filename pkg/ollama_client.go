package pkg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// callOllama sends the analysis prompt to Ollama and returns the model response as raw JSON.
func callOllama(ctx context.Context, baseURL, prompt string) ([]byte, error) {
	start := time.Now()
	status := "ok"
	defer func() {
		observeOllamaRequest(status, time.Since(start))
	}()

	url := strings.TrimRight(baseURL, "/") + "/api/generate"

	payload := ollamaGenerateRequest{
		Model:  "qwen2.5:7b",
		Prompt: prompt,
		Stream: false,
		Options: ollamaOptions{
			Temperature: 0.2,
			NumPredict:  1200,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = "timeout"
		} else {
			status = "error"
		}
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		status = strconv.Itoa(resp.StatusCode)
		return nil, errors.New("ollama request failed: status " + status)
	}

	var parsed ollamaGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		status = "decode_error"
		return nil, err
	}
	if parsed.Error != "" {
		status = "error"
		return nil, errors.New(parsed.Error)
	}

	return []byte(strings.TrimSpace(parsed.Response)), nil
}
