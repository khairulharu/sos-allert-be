package usecase

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWhisperTranscriberSendsMultipart(t *testing.T) {
	var gotPath, gotAuth, gotModel, gotLanguage, gotFormat, gotFile string
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
		_, _ = fmt.Fprint(w, `{"text":"ada orang pingsan tidak sadar"}`)
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
	if gotModel != "whisper-large-v3-turbo" || gotLanguage != "id" || gotFormat != "json" {
		t.Fatalf("fields = %q/%q/%q", gotModel, gotLanguage, gotFormat)
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
