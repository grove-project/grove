package groveshop

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
)

// webFiles contains the browser application shipped in the Grove Shop
// deployment artifact.
//
//go:embed web/index.html
var webFiles embed.FS

// WebHandler serves the Grove Shop browser assets without runtime files or a
// separate frontend deployment.
func WebHandler() http.Handler {
	return WebHandlerWithConfiguration(DefaultConfiguration(), "")
}

// RuntimeConfigurationView is the non-secret immutable configuration exposed
// by Grove Shop for operators and application verification.
type RuntimeConfigurationView struct {
	Revision          string `json:"revision"`
	ConfigDigest      string `json:"config_digest,omitempty"`
	CustomerName      string `json:"customer_name"`
	ClusterName       string `json:"cluster_name"`
	NodeZone          string `json:"node_zone"`
	ReservationBuffer int    `json:"reservation_buffer"`
}

// WebHandlerWithConfiguration serves embedded assets and a read-only view of
// the compiled configuration observed by this exact artifact version.
func WebHandlerWithConfiguration(configuration Configuration, configDigest string) http.Handler {
	root, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	view := RuntimeConfigurationView{
		Revision:          configuration.Revision,
		ConfigDigest:      configDigest,
		CustomerName:      configuration.Customer.Name,
		ClusterName:       configuration.Cluster.Name,
		NodeZone:          configuration.Node.Zone,
		ReservationBuffer: configuration.Inventory.ReservationBuffer,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /grove/config", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(response).Encode(view); err != nil {
			http.Error(response, "encode Grove Shop configuration", http.StatusInternalServerError)
		}
	})
	mux.Handle("GET /", http.FileServer(http.FS(root)))
	return mux
}

// WebAsset returns one embedded Grove Shop browser asset for artifact tests and
// tooling.
func WebAsset(name string) ([]byte, error) {
	return webFiles.ReadFile("web/" + name)
}
