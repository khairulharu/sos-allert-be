package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
	"unicode"
)

// Transcriber converts recorded audio to text. Satisfied by
// WhisperTranscriber (Groq/OpenAI-compatible endpoints).
type Transcriber interface {
	Transcribe(ctx context.Context, filename, contentType string, r io.Reader) (string, error)
}

// ErrNoSpeechDetected reports that the provider answered but the clip
// carried no recognisable speech (silence, room noise, or Whisper's
// subtitle-hallucination boilerplate). It is a normal outcome that maps
// to transcription_status "empty" (#54), not a provider failure.
var ErrNoSpeechDetected = errors.New("stt: no speech detected")

const (
	// Whisper returns a per-segment no_speech_prob. On silence and room
	// noise it invents subtitle boilerplate ("Terima kasih telah
	// menonton!"), so a segment above this threshold counts as silence.
	whisperNoSpeechThreshold = 0.6
	// avg_logprob below this is very low confidence. Combined with a
	// short output it is the usual signature of a hallucination.
	whisperLowLogprob = -1.0
	// compression_ratio above this is a repetition loop, one of the
	// documented Whisper hallucination signatures.
	whisperRepeatRatio = 2.4
	// Clips shorter than this cannot carry a meaningful utterance.
	whisperMinSpeechSeconds = 0.5
	// Hallucination phrase matching only applies to short outputs, so a
	// real transcript that happens to contain a thanks is kept.
	whisperShortTextRunes = 120
)

// whisperEmergencyPrompt biases the decoder towards the vocabulary of an
// Indonesian emergency call instead of YouTube subtitles. It stays a
// short vocabulary list because a full sentence prompt is itself prone
// to leaking into the output on silence.
const whisperEmergencyPrompt = "Panggilan darurat. Tolong, kecelakaan, jatuh, pingsan, " +
	"tidak sadar, sesak napas, berdarah, rumah sakit, ambulans, maling, begal, kebakaran, banjir."

// whisperResponse covers both response_format=verbose_json (segments
// included) and the plain json fallback (text only).
type whisperResponse struct {
	Text     string  `json:"text"`
	Duration float64 `json:"duration"`
	Segments []struct {
		Text             string  `json:"text"`
		NoSpeechProb     float64 `json:"no_speech_prob"`
		AvgLogprob       float64 `json:"avg_logprob"`
		CompressionRatio float64 `json:"compression_ratio"`
	} `json:"segments"`
}

// WhisperTranscriber calls an OpenAI-compatible /audio/transcriptions
// endpoint. Groq works out of the box with BaseURL
// https://api.groq.com/openai/v1 and model whisper-large-v3-turbo.
type WhisperTranscriber struct {
	BaseURL  string
	APIKey   string
	Model    string
	Language string
	Prompt   string
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
		Prompt:   whisperEmergencyPrompt,
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

	audio, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("stt: read audio: %w", err)
	}
	if len(audio) == 0 {
		return "", ErrNoSpeechDetected
	}

	payload, err := c.transcribeOnce(ctx, filename, contentType, audio, "verbose_json")
	if err != nil {
		// Not every OpenAI-compatible server accepts verbose_json. The
		// plain json shape still yields a transcript; only the silence
		// scores are missing, so retry once on an explicit 400 instead
		// of failing the upload.
		var httpErr *sttHTTPError
		if !errors.As(err, &httpErr) || httpErr.Status != http.StatusBadRequest {
			return "", err
		}
		payload, err = c.transcribeOnce(ctx, filename, contentType, audio, "json")
		if err != nil {
			return "", err
		}
	}
	return interpretWhisper(payload)
}

// sttHTTPError keeps the status so the caller can decide whether a
// different response_format is worth trying.
type sttHTTPError struct {
	Status int
	Detail string
}

func (e *sttHTTPError) Error() string {
	return fmt.Sprintf("stt: unexpected status %d: %s", e.Status, e.Detail)
}

func (c *WhisperTranscriber) transcribeOnce(ctx context.Context, filename, contentType string, audio []byte, responseFormat string) (*whisperResponse, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, escapeQuotes(filename)))
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, fmt.Errorf("stt: create multipart: %w", err)
	}
	if _, err := part.Write(audio); err != nil {
		return nil, fmt.Errorf("stt: write audio: %w", err)
	}
	_ = writer.WriteField("model", c.Model)
	_ = writer.WriteField("response_format", responseFormat)
	_ = writer.WriteField("temperature", "0")
	if c.Language != "" {
		_ = writer.WriteField("language", c.Language)
	}
	if c.Prompt != "" {
		_ = writer.WriteField("prompt", c.Prompt)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("stt: close multipart: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/audio/transcriptions", &body)
	if err != nil {
		return nil, fmt.Errorf("stt: create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stt: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &sttHTTPError{Status: resp.StatusCode, Detail: strings.TrimSpace(string(detail))}
	}

	var payload whisperResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("stt: decode response: %w", err)
	}
	return &payload, nil
}

// interpretWhisper decides whether the provider actually heard speech.
// Whisper happily transcribes silence into subtitle boilerplate, so a
// non-empty text is not enough evidence (#54).
func interpretWhisper(payload *whisperResponse) (string, error) {
	text := strings.TrimSpace(payload.Text)
	if text == "" {
		return "", ErrNoSpeechDetected
	}
	if payload.Duration > 0 && payload.Duration < whisperMinSpeechSeconds {
		return "", ErrNoSpeechDetected
	}
	if len(payload.Segments) > 0 {
		// A clip whose every segment is probably silence is silence,
		// whatever text the decoder produced.
		spoke := false
		repetitionOnly := true
		bestLogprob := whisperLowLogprob - 1
		for _, segment := range payload.Segments {
			if segment.NoSpeechProb >= whisperNoSpeechThreshold {
				continue
			}
			spoke = true
			if segment.CompressionRatio <= whisperRepeatRatio {
				repetitionOnly = false
			}
			if segment.AvgLogprob > bestLogprob {
				bestLogprob = segment.AvgLogprob
			}
		}
		if !spoke || repetitionOnly {
			return "", ErrNoSpeechDetected
		}
		// Very low confidence on a short output: a hallucinated phrase.
		if bestLogprob <= whisperLowLogprob && len([]rune(text)) <= whisperShortTextRunes {
			return "", ErrNoSpeechDetected
		}
	}
	if isHallucinationPhrase(text) {
		return "", ErrNoSpeechDetected
	}
	return text, nil
}

// hallucinationPhrases are the stock lines Whisper emits for silence and
// room noise (mostly YouTube/streaming subtitle artifacts). Matching is
// case/punctuation-insensitive and only applied to short transcripts.
var hallucinationPhrases = []string{
	"terima kasih",
	"terima kasih telah menonton",
	"terima kasih sudah menonton",
	"terima kasih telah menonton video ini",
	"terima kasih sudah menonton video ini",
	"terima kasih telah menonton sampai akhir",
	"selamat menonton",
	"jangan lupa subscribe",
	"jangan lupa like dan subscribe",
	"like dan subscribe",
	"subscribe",
	"sampai jumpa",
	"hai",
	"halo",
	"hello",
	"hi",
	"hmm",
	"you",
	"bye",
	"thank you",
	"thank you for watching",
	"thanks for watching",
	"something happen",
	"www youtube com",
}

// isHallucinationPhrase reports whether a short transcript is exactly a
// stock hallucination line, or that line repeated (Whisper's
// "SOMETHING HAPPEN SOMETHING HAPPEN ..." loop).
func isHallucinationPhrase(text string) bool {
	normalized := normalizeSpeechText(text)
	if normalized == "" {
		return true
	}
	if len([]rune(normalized)) > whisperShortTextRunes {
		return false
	}
	for _, phrase := range hallucinationPhrases {
		if repeatsPhrase(normalized, phrase) {
			return true
		}
	}
	return false
}

// repeatsPhrase matches a whole-word phrase once or repeated back to
// back, so "hai" does not match "hidup" and "terima kasih" still catches
// "terima kasih terima kasih".
func repeatsPhrase(text, phrase string) bool {
	if text == phrase {
		return true
	}
	prefix := phrase + " "
	if !strings.HasPrefix(text, prefix) {
		return false
	}
	return repeatsPhrase(strings.TrimPrefix(text, prefix), phrase)
}

// normalizeSpeechText lowercases and reduces every non-alphanumeric rune
// to a single space, so punctuation and casing cannot hide a match.
func normalizeSpeechText(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteByte(' ')
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func escapeQuotes(s string) string {
	return strings.ReplaceAll(s, `"`, "")
}
