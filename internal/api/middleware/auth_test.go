package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIKeyAuth_ValidKey_Header(t *testing.T) {
	apiKey := "test-api-key"
	middleware := APIKeyAuth(apiKey)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "success" {
		t.Errorf("body = %q, want %q", w.Body.String(), "success")
	}
}

func TestAPIKeyAuth_ValidKey_BearerToken(t *testing.T) {
	apiKey := "test-api-key"
	middleware := APIKeyAuth(apiKey)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestAPIKeyAuth_ValidKey_QueryParam(t *testing.T) {
	apiKey := "test-api-key"
	middleware := APIKeyAuth(apiKey)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name  string
		query string
	}{
		{"key param", "?key=" + apiKey},
		{"api_key param", "?api_key=" + apiKey},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/test"+tt.query, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
			}
		})
	}
}

func TestAPIKeyAuth_MissingKey(t *testing.T) {
	apiKey := "test-api-key"
	middleware := APIKeyAuth(apiKey)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
}

func TestAPIKeyAuth_InvalidKey(t *testing.T) {
	apiKey := "correct-api-key"
	middleware := APIKeyAuth(apiKey)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-API-Key", "wrong-api-key")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestAPIKeyAuth_ShortBearerToken(t *testing.T) {
	apiKey := "test-api-key"
	middleware := APIKeyAuth(apiKey)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bear") // Too short for "Bearer "
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestCORS(t *testing.T) {
	handler := CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Check CORS headers
	tests := []struct {
		header string
		want   string
	}{
		{"Access-Control-Allow-Origin", "*"},
		{"Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS"},
		{"Access-Control-Allow-Headers", "Content-Type, X-API-Key, Authorization"},
		{"Access-Control-Max-Age", "86400"},
	}

	for _, tt := range tests {
		if got := w.Header().Get(tt.header); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestCORS_Preflight(t *testing.T) {
	called := false
	handler := CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	if called {
		t.Error("next handler should not be called for OPTIONS request")
	}
}

func TestAPIKeyAuth_EmptyAPIKey(t *testing.T) {
	apiKey := ""
	middleware := APIKeyAuth(apiKey)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Test with empty header - should fail because no key provided
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Should fail because no key is provided (empty string from query param with ?key= is still empty)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestAPIKeyAuth_BearerTokenCaseVariations(t *testing.T) {
	apiKey := "test-key"
	middleware := APIKeyAuth(apiKey)

	tests := []struct {
		name   string
		header string
		want   int
	}{
		{"lowercase bearer", "bearer " + apiKey, http.StatusUnauthorized},
		{"mixed case bearer", "BeArEr " + apiKey, http.StatusUnauthorized},
		{"uppercase bearer", "BEARER " + apiKey, http.StatusUnauthorized},
		{"correct bearer", "Bearer " + apiKey, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			req.Header.Set("Authorization", tt.header)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != tt.want {
				t.Errorf("status = %d, want %d", w.Code, tt.want)
			}
		})
	}
}

func TestAPIKeyAuth_QueryParamFallback(t *testing.T) {
	apiKey := "correct-key"
	middleware := APIKeyAuth(apiKey)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Query param should work when header is missing
	req := httptest.NewRequest(http.MethodGet, "/api/test?key="+apiKey, nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestAPIKeyAuth_HeaderPrecedence(t *testing.T) {
	apiKey := "correct-key"
	middleware := APIKeyAuth(apiKey)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Header should be checked first and succeed
	req := httptest.NewRequest(http.MethodGet, "/api/test?key=wrong-key", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestAPIKeyAuth_BearerPrecedence(t *testing.T) {
	apiKey := "correct-key"
	middleware := APIKeyAuth(apiKey)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Bearer is checked before query param
	req := httptest.NewRequest(http.MethodGet, "/api/test?key=wrong-key", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestCORS_AllMethods(t *testing.T) {
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			handler := CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(method, "/api/test", nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
			}

			origin := w.Header().Get("Access-Control-Allow-Origin")
			if origin != "*" {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", origin, "*")
			}
		})
	}
}
