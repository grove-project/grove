package groveshop_test

import (
	"encoding/json"
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

func TestWebHandlerExposesReadOnlyRuntimeConfiguration(t *testing.T) {
	configuration := groveshop.DefaultConfiguration()
	configuration.Revision = "acme-r42"
	configuration.Customer.Name = "Acme Retail"
	configuration.Cluster.Name = "production"
	configuration.Node.Zone = "edge"
	configuration.Inventory.ReservationBuffer = 7
	server := httptest.NewServer(groveshop.WebHandlerWithConfiguration(configuration, "sha256:config"))
	defer server.Close()
	response, err := http.Get(server.URL + "/grove/config")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var view groveshop.RuntimeConfigurationView
	if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if view.Revision != "acme-r42" || view.ConfigDigest != "sha256:config" || view.CustomerName != "Acme Retail" || view.ClusterName != "production" || view.NodeZone != "edge" || view.ReservationBuffer != 7 {
		t.Errorf("runtime configuration view = %#v", view)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/grove/config", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /grove/config status = %d; want %d", response.StatusCode, http.StatusMethodNotAllowed)
	}
}
