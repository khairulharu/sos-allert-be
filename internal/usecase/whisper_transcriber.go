package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

// Transcriber converts recorded audio to text. Satisfied by
// WhisperTranscriber (Groq/OpenAI-compatible endpoints).
type Transcriber interface {
	Transcribe(ctx context.Context, filename, contentType string, r io.Reader) (string, error)
}

// WhisperTranscriber calls an OpenAI-compatible /audio/transcriptions
// endpoint. Groq works out of the box with BaseURL
// https://api.groq.com/openai/v1 and model whisper-large-v3-turbo.
type WhisperTranscriber struct {
	BaseURL  string
	APIKey   string
	Model    string
	Language string
	HTTP     *http.Client
}

func NewWhisperTranscriber(baseURL, apiKey, model, language string, timeout time.Duration) *WhisperTranscriber {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &WhisperTranscriber{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		APIKey:   apiKey,
		Model:    model,
		Language: language,
		HTTP:     &http.Client{Timeout: timeout},
	}
}

func (c *WhisperTranscriber) Transcribe(ctx context.Context, filename, contentType string, r io.Reader) (string, error) {
	if c.BaseURL == "" {
		return "", fmt.Errorf("stt: base_url is not configured")
	}
	if c.Model == "" {
		return "", fmt.Errorf("stt: model is not configured")
	}
	if filename == "" {
		filename = "audio.webm"
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, escapeQuotes(filename)))
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		return "", fmt.Errorf("stt: create multipart: %w", err)
	}
	if _, err := io.Copy(part, r); err != nil {
		return "", fmt.Errorf("stt: read audio: %w", err)
	}
	_ = writer.WriteField("model", c.Model)
	_ = writer.WriteField("response_format", "json")
	_ = writer.WriteField("temperature", "0")
	if c.Language != "" {
		_ = writer.WriteField("language", c.Language)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("stt: close multipart: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", fmt.Errorf("stt: create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("stt: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("stt: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}

	var payload struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("stt: decode response: %w", err)
	}
	return strings.TrimSpace(payload.Text), nil
}

func escapeQuotes(s string) string {
	return strings.ReplaceAll(s, `"`, "")
}
