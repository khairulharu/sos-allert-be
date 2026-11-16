package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWhisperTranscriberSendsMultipart(t *testing.T) {
	var gotPath, gotAuth, gotModel, gotLanguage, gotFormat, gotPrompt, gotFile string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotModel = r.FormValue("model")
		gotLanguage = r.FormValue("language")
		gotFormat = r.FormValue("response_format")
		gotPrompt = r.FormValue("prompt")
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("form file: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		gotFile = header.Filename + ":" + string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"text":"ada orang pingsan tidak sadar","duration":4.2,"segments":[
			{"text":"ada orang pingsan tidak sadar","no_speech_prob":0.02,"avg_logprob":-0.2,"compression_ratio":1.1}]}`)
	}))
	defer server.Close()

	client := NewWhisperTranscriber(server.URL, "gsk_test", "whisper-large-v3-turbo", "id", 5*time.Second)
	text, err := client.Transcribe(context.Background(), "panic.webm", "audio/webm", strings.NewReader("voice-bytes"))
	if err != nil {
		t.Fatalf("Transcribe error = %v", err)
	}
	if text != "ada orang pingsan tidak sadar" {
		t.Fatalf("text = %q", text)
	}
	if gotPath != "/audio/transcriptions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer gsk_test" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if gotModel != "whisper-large-v3-turbo" || gotLanguage != "id" || gotFormat != "verbose_json" {
		t.Fatalf("fields = %q/%q/%q", gotModel, gotLanguage, gotFormat)
	}
	if gotPrompt != whisperEmergencyPrompt {
		t.Fatalf("prompt = %q, want the emergency vocabulary", gotPrompt)
	}
	if gotFile != "panic.webm:voice-bytes" {
		t.Fatalf("file = %q", gotFile)
	}
}

func TestWhisperTranscriberOmitsAuthorizationWithoutKey(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = fmt.Fprint(w, `{"text":"ok"}`)
	}))
	defer server.Close()

	client := NewWhisperTranscriber(server.URL, "", "whisper-large-v3-turbo", "id", 5*time.Second)
	if _, err := client.Transcribe(context.Background(), "test.webm", "audio/webm", strings.NewReader("x")); err != nil {
		t.Fatalf("Transcribe error = %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("authorization = %q, want empty", gotAuth)
	}
}

func TestWhisperTranscriberReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":"invalid api key"}`)
	}))
	defer server.Close()

	client := NewWhisperTranscriber(server.URL, "bad", "whisper-large-v3-turbo", "id", 5*time.Second)
	if _, err := client.Transcribe(context.Background(), "test.webm", "audio/webm", strings.NewReader("x")); err == nil {
		t.Fatal("Transcribe error = nil, want HTTP error")
	}
}

// Silence and room noise must not become a transcript just because the
// provider returned text (#54).
func TestWhisperTranscriberRejectsSilence(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "every segment is silence",
			body: `{"text":"Terima kasih telah menonton!","duration":10.2,"segments":[
				{"text":"Terima kasih telah menonton!","no_speech_prob":0.97,"avg_logprob":-0.9,"compression_ratio":0.9}]}`,
		},
		{
			name: "repetition loop",
			body: `{"text":"SOMETHING HAPPEN SOMETHING HAPPEN SOMETHING HAPPEN","duration":9.5,"segments":[
				{"text":"SOMETHING HAPPEN","no_speech_prob":0.2,"avg_logprob":-0.4,"compression_ratio":3.1}]}`,
		},
		{
			name: "low confidence short text",
			body: `{"text":"Hai","duration":8.0,"segments":[
				{"text":"Hai","no_speech_prob":0.2,"avg_logprob":-1.6,"compression_ratio":1.0}]}`,
		},
		{
			name: "hallucination phrase without segments",
			body: `{"text":"Terima kasih telah menonton!"}`,
		},
		{
			name: "repeated hallucination phrase without segments",
			body: `{"text":"something happen something happen something happen"}`,
		},
		{
			name: "too short to speak",
			body: `{"text":"Tolong","duration":0.2,"segments":[
				{"text":"Tolong","no_speech_prob":0.1,"avg_logprob":-0.3,"compression_ratio":1.0}]}`,
		},
		{
			name: "empty text",
			body: `{"text":"   ","duration":5.0}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer server.Close()

			client := NewWhisperTranscriber(server.URL, "gsk_test", "whisper-large-v3-turbo", "id", 5*time.Second)
			text, err := client.Transcribe(context.Background(), "silence.webm", "audio/webm", strings.NewReader("noise"))
			if !errors.Is(err, ErrNoSpeechDetected) {
				t.Fatalf("error = %v, want ErrNoSpeechDetected", err)
			}
			if text != "" {
				t.Fatalf("text = %q, want empty", text)
			}
		})
	}
}

// Short real speech must survive the hallucination filter.
func TestWhisperTranscriberKeepsShortRealSpeech(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"text":"Tolong!","duration":1.4,"segments":[
			{"text":"Tolong!","no_speech_prob":0.1,"avg_logprob":-0.25,"compression_ratio":1.0}]}`)
	}))
	defer server.Close()

	client := NewWhisperTranscriber(server.URL, "gsk_test", "whisper-large-v3-turbo", "id", 5*time.Second)
	text, err := client.Transcribe(context.Background(), "shout.webm", "audio/webm", strings.NewReader("voice"))
	if err != nil {
		t.Fatalf("Transcribe error = %v", err)
	}
	if text != "Tolong!" {
		t.Fatalf("text = %q, want the real shout", text)
	}
}

// A provider that does not support verbose_json gets one retry with the
// plain json shape instead of failing the upload.
func TestWhisperTranscriberFallsBackToJSON(t *testing.T) {
	var formats []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		format := r.FormValue("response_format")
		formats = append(formats, format)
		if format == "verbose_json" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":"response_format not supported"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"text":"ada orang kecelakaan"}`)
	}))
	defer server.Close()

	client := NewWhisperTranscriber(server.URL, "gsk_test", "whisper-large-v3-turbo", "id", 5*time.Second)
	text, err := client.Transcribe(context.Background(), "crash.webm", "audio/webm", strings.NewReader("voice"))
	if err != nil {
		t.Fatalf("Transcribe error = %v", err)
	}
	if text != "ada orang kecelakaan" {
		t.Fatalf("text = %q", text)
	}
	if len(formats) != 2 || formats[0] != "verbose_json" || formats[1] != "json" {
		t.Fatalf("formats = %v, want one verbose_json then one json attempt", formats)
	}
}

func TestIsHallucinationPhrase(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"Terima kasih telah menonton!", true},
		{"  TERIMA KASIH...  ", true},
		{"thank you for watching", true},
		{"Hai", true},
		{"something happen something happen", true},
		{"hidup", false},
		{"Tolong, ada orang pingsan", false},
		{"terima kasih telah menonton video ini", true},
		{"", true},
	}
	for _, tc := range cases {
		if got := isHallucinationPhrase(tc.text); got != tc.want {
			t.Errorf("isHallucinationPhrase(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
