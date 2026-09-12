package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type mediaServer struct {
	ingestion *ingestionService
}

func main() {
	apiKey := os.Getenv("INFRAI_API_KEY")
	if apiKey == "" {
		log.Fatal(errMissingAPIKey)
	}
	bucket := envOr("MEDIA_BUCKET", "creator-media-assets")
	client := newInfraiClient(apiKey)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.createBucket(ctx, bucket); err != nil {
		log.Fatalf("initialize media bucket: %v", err)
	}

	server := &mediaServer{ingestion: newIngestionService(client, bucket)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /assets/uploads", server.startUpload)
	mux.HandleFunc("POST /assets/{assetID}/uploaded", server.confirmUpload)
	mux.HandleFunc("POST /jobs/{jobID}/complete", server.completeJob)
	mux.HandleFunc("GET /assets/{assetID}/delivery", server.delivery)

	addr := envOr("ADDR", ":8080")
	log.Printf("media asset service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func (s *mediaServer) startUpload(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CreatorID   string `json:"creator_id"`
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		RequestID   string `json:"request_id"`
	}
	if err := decodeJSON(r, &input); err != nil || !validSegment(input.CreatorID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid upload request"})
		return
	}
	ticket, err := s.ingestion.startUpload(r.Context(), input.CreatorID, input.Filename, input.ContentType, input.RequestID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ticket)
}

func (s *mediaServer) confirmUpload(w http.ResponseWriter, r *http.Request) {
	item, err := s.ingestion.confirmUpload(r.Context(), r.PathValue("assetID"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, item)
}

func (s *mediaServer) completeJob(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AssetID string `json:"asset_id"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid job result"})
		return
	}
	item, err := s.ingestion.completeJob(input.AssetID, r.PathValue("jobID"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *mediaServer) delivery(w http.ResponseWriter, r *http.Request) {
	requestID := r.URL.Query().Get("request_id")
	if requestID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request_id is required"})
		return
	}
	signed, err := s.ingestion.deliveryURL(r.Context(), r.PathValue("assetID"), requestID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"method": "GET", "download_url": signed.URL})
}

func decodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}

func writeServiceError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, errAssetNotFound):
		status = http.StatusNotFound
	case errors.Is(err, errUploadPending), errors.Is(err, errJobConflict), errors.Is(err, errAssetNotReady):
		status = http.StatusConflict
	default:
		var apiErr *InfraiError
		if errors.As(err, &apiErr) && apiErr.HTTPStatus >= 400 && apiErr.HTTPStatus < 500 {
			status = apiErr.HTTPStatus
		}
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
