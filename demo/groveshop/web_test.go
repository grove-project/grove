package groveshop_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grove-project/grove/demo/groveshop"
)

func TestWebHandlerServesEmbeddedIndex(t *testing.T) {
	server := httptest.NewServer(groveshop.WebHandler())
	defer server.Close()
	response, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Errorf("GET / status = %d; want %d", response.StatusCode, http.StatusOK)
	}
	if !strings.Contains(string(body), `data-grove-artifact="grove-shop-ui-v1"`) {
		t.Errorf("GET / body does not contain Grove Shop UI fingerprint: %q", body)
	}
}

func TestWebAsset(t *testing.T) {
	asset, err := groveshop.WebAsset("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(asset), "Grove Cluster Status") {
		t.Errorf("embedded index = %q", asset)
	}
	if _, err := groveshop.WebAsset("missing.html"); err == nil {
		t.Fatal("missing embedded asset returned nil error")
	}
}
