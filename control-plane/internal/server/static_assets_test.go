package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLandingStaticAssetsAreServed(t *testing.T) {
	_, handler, _ := newRepoCredentialTestServer(t)

	for _, path := range []string{"/site.webmanifest", "/forgeLogos/favicon.svg"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("GET %s: expected %d, got %d body=%s", path, http.StatusOK, res.Code, res.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/site.webmanifest", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if !strings.Contains(res.Body.String(), `"name": "Forge"`) {
		t.Fatalf("manifest response did not contain Forge app name: %s", res.Body.String())
	}
}
