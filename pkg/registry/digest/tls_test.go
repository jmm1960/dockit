package digest_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/containrrr/watchtower/pkg/registry/digest"
)

func TestDigestRejectsUntrustedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:untrusted")
	}))
	defer server.Close()
	if _, err := digest.GetDigest(server.URL, "Bearer test"); err == nil {
		t.Fatal("accepted untrusted TLS certificate")
	}
}
