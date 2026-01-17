package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewUIHandler(t *testing.T) {
	handler := NewUIHandler()
	if handler == nil {
		t.Fatal("handler should not be nil")
	}
}

func TestUIHandler_Index(t *testing.T) {
	handler := NewUIHandler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handler.Index(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", contentType, "text/html; charset=utf-8")
	}

	body := w.Body.String()
	if len(body) == 0 {
		t.Error("response body should not be empty")
	}
	if !strings.Contains(body, "<!DOCTYPE html>") && !strings.Contains(body, "<html") {
		t.Error("response should contain HTML content")
	}
}

func TestUIHandler_Quick(t *testing.T) {
	handler := NewUIHandler()

	req := httptest.NewRequest(http.MethodGet, "/quick", nil)
	w := httptest.NewRecorder()

	handler.Quick(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", contentType, "text/html; charset=utf-8")
	}

	body := w.Body.String()
	if len(body) == 0 {
		t.Error("response body should not be empty")
	}
}

func TestUIHandler_Smart_Desktop(t *testing.T) {
	handler := NewUIHandler()

	tests := []struct {
		name      string
		userAgent string
		wantBody  string // Substring that indicates desktop vs mobile
	}{
		{
			name:      "Chrome on Windows",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0",
		},
		{
			name:      "Firefox on Mac",
			userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:120.0) Gecko/20100101 Firefox/120.0",
		},
		{
			name:      "Safari on Mac",
			userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Safari/605.1.15",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/smart", nil)
			req.Header.Set("User-Agent", tt.userAgent)
			w := httptest.NewRecorder()

			handler.Smart(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
			}

			contentType := w.Header().Get("Content-Type")
			if contentType != "text/html; charset=utf-8" {
				t.Errorf("Content-Type = %q, want %q", contentType, "text/html; charset=utf-8")
			}

			// Desktop should return index HTML
			body := w.Body.String()
			if len(body) == 0 {
				t.Error("response body should not be empty")
			}
		})
	}
}

func TestUIHandler_Smart_Mobile(t *testing.T) {
	handler := NewUIHandler()

	tests := []struct {
		name      string
		userAgent string
	}{
		{
			name:      "iPhone Safari",
			userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 Mobile Safari/605.1.15",
		},
		{
			name:      "Android Chrome",
			userAgent: "Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 Chrome/120.0.0.0 Mobile Safari/537.36",
		},
		{
			name:      "iPad Safari",
			userAgent: "Mozilla/5.0 (iPad; CPU OS 16_0 like Mac OS X) AppleWebKit/605.1.15 Safari/605.1.15",
		},
		{
			name:      "Generic Mobile",
			userAgent: "Mozilla/5.0 Mobile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/smart", nil)
			req.Header.Set("User-Agent", tt.userAgent)
			w := httptest.NewRecorder()

			handler.Smart(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
			}

			contentType := w.Header().Get("Content-Type")
			if contentType != "text/html; charset=utf-8" {
				t.Errorf("Content-Type = %q, want %q", contentType, "text/html; charset=utf-8")
			}

			// Mobile should return quick HTML
			body := w.Body.String()
			if len(body) == 0 {
				t.Error("response body should not be empty")
			}
		})
	}
}

func TestUIHandler_AdminEvents(t *testing.T) {
	handler := NewUIHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/events", nil)
	w := httptest.NewRecorder()

	handler.AdminEvents(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", contentType, "text/html; charset=utf-8")
	}

	body := w.Body.String()
	if len(body) == 0 {
		t.Error("response body should not be empty")
	}
}

func TestUIHandler_Videos(t *testing.T) {
	handler := NewUIHandler()

	req := httptest.NewRequest(http.MethodGet, "/videos", nil)
	w := httptest.NewRecorder()

	handler.Videos(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", contentType, "text/html; charset=utf-8")
	}

	body := w.Body.String()
	if len(body) == 0 {
		t.Error("response body should not be empty")
	}
}

func TestUIHandler_Playlists(t *testing.T) {
	handler := NewUIHandler()

	req := httptest.NewRequest(http.MethodGet, "/playlists", nil)
	w := httptest.NewRecorder()

	handler.Playlists(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", contentType, "text/html; charset=utf-8")
	}

	body := w.Body.String()
	if len(body) == 0 {
		t.Error("response body should not be empty")
	}
}
