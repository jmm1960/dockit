package dockit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBarkJSONAndApplicationErrors(t *testing.T) {
	code := 200
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/push" {
			t.Error("wrong endpoint")
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["device_key"] != "test-key" || payload["title"] != "重启" || payload["body"] != "容器已重启" || payload["group"] != "Dockit" {
			t.Errorf("payload: %v", payload)
		}
		fmt.Fprintf(w, `{"code":%d}`, code)
	}))
	defer server.Close()
	b, err := newBark(config{barkServer: server.URL, barkKey: "test-key", barkGroup: "Dockit"})
	if err != nil {
		t.Fatal(err)
	}
	b.client = server.Client()
	if err = b.send(context.Background(), notification{"重启", "容器已重启"}); err != nil {
		t.Fatal(err)
	}
	code = 400
	if err = b.send(context.Background(), notification{"重启", "容器已重启"}); err == nil {
		t.Fatal("accepted Bark application error")
	}
}

func TestBarkDoesNotFollowRedirects(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("credentials sent to redirect target") }))
	defer target.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	b, err := newBark(config{barkServer: server.URL, barkKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	b.client.Transport = server.Client().Transport
	if err = b.send(context.Background(), notification{"test", "test"}); err == nil {
		t.Fatal("accepted redirect")
	}
}
