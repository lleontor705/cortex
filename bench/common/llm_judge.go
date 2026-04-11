package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// JudgeConfig configures the LLM-as-Judge evaluator.
type JudgeConfig struct {
	Provider string // "openai" or "anthropic"
	APIKey   string
	Model    string // e.g. "gpt-4o" or "claude-sonnet-4-20250514"
}

// DefaultJudgeConfig returns config from environment variables.
func DefaultJudgeConfig() *JudgeConfig {
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		return &JudgeConfig{Provider: "openai", APIKey: key, Model: "gpt-4o"}
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return &JudgeConfig{Provider: "anthropic", APIKey: key, Model: "claude-sonnet-4-20250514"}
	}
	return nil
}

// JudgeAnswer uses an LLM to judge if the answer is correct given the reference.
// Returns a score between 0.0 and 1.0.
func JudgeAnswer(cfg *JudgeConfig, question, expected, got string) (float64, error) {
	if cfg == nil {
		return -1, fmt.Errorf("no LLM judge configured (set OPENAI_API_KEY or ANTHROPIC_API_KEY)")
	}

	prompt := fmt.Sprintf(
		"You are evaluating a memory system's answer.\n\n"+
			"Question: %s\n"+
			"Expected answer: %s\n"+
			"System answer: %s\n\n"+
			"Is the system answer correct? Reply with ONLY 'CORRECT' or 'INCORRECT'.",
		question, expected, got,
	)

	var response string
	var err error

	switch cfg.Provider {
	case "openai":
		response, err = callOpenAI(cfg, prompt)
	case "anthropic":
		response, err = callAnthropic(cfg, prompt)
	default:
		return -1, fmt.Errorf("unknown judge provider: %s", cfg.Provider)
	}

	if err != nil {
		return -1, err
	}

	response = strings.TrimSpace(strings.ToUpper(response))
	if strings.Contains(response, "CORRECT") && !strings.Contains(response, "INCORRECT") {
		return 1.0, nil
	}
	return 0.0, nil
}

func callOpenAI(cfg *JudgeConfig, prompt string) (string, error) {
	body := map[string]any{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 10,
	}
	data, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("openai decode: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("openai: no choices returned")
	}
	return result.Choices[0].Message.Content, nil
}

func callAnthropic(cfg *JudgeConfig, prompt string) (string, error) {
	body := map[string]any{
		"model":      cfg.Model,
		"max_tokens": 10,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	data, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cfg.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("anthropic decode: %w", err)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("anthropic: no content returned")
	}
	return result.Content[0].Text, nil
}
