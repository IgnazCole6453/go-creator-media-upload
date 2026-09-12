package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

const maxAssetBytes int64 = 2 << 30

type objectStore interface {
	presignPut(context.Context, string, string, string, string, int64) (presignResult, error)
	presignGet(context.Context, string, string, string, string) (presignResult, error)
	objectExists(context.Context, string, string) (bool, error)
}

type assetState string

const (
	stateAwaitingUpload assetState = "awaiting_upload"
	stateQueued         assetState = "queued"
	stateReady          assetState = "ready"
)

type asset struct {
	ID          string     `json:"asset_id"`
	CreatorID   string     `json:"creator_id"`
	Filename    string     `json:"filename"`
	ContentType string     `json:"content_type"`
	ObjectKey   string     `json:"object_key"`
	State       assetState `json:"state"`
	JobID       string     `json:"job_id,omitempty"`
}

type uploadTicket struct {
	AssetID   string     `json:"asset_id"`
	ObjectKey string     `json:"object_key"`
	State     assetState `json:"state"`
	Method    string     `json:"method"`
	UploadURL string     `json:"upload_url"`
}

type ingestionService struct {
	store  objectStore
	bucket string
	mu     sync.RWMutex
	assets map[string]asset
}

func newIngestionService(store objectStore, bucket string) *ingestionService {
	return &ingestionService{store: store, bucket: bucket, assets: make(map[string]asset)}
}

func (s *ingestionService) startUpload(ctx context.Context, creatorID, filename, contentType, requestID string) (uploadTicket, error) {
	if creatorID == "" || filename == "" || contentType == "" || requestID == "" {
		return uploadTicket{}, errors.New("creator_id, filename, content_type, and request_id are required")
	}
	assetID, err := randomID("asset")
	if err != nil {
		return uploadTicket{}, err
	}
	cleanName := filepath.Base(filename)
	key := "creators/" + creatorID + "/source/" + assetID + "/" + cleanName
	signed, err := s.store.presignPut(ctx, s.bucket, key, contentType, requestID, maxAssetBytes)
	if err != nil {
		return uploadTicket{}, err
	}
	item := asset{ID: assetID, CreatorID: creatorID, Filename: cleanName, ContentType: contentType, ObjectKey: key, State: stateAwaitingUpload}
	s.mu.Lock()
	s.assets[assetID] = item
	s.mu.Unlock()
	return uploadTicket{AssetID: assetID, ObjectKey: key, State: item.State, Method: "PUT", UploadURL: signed.URL}, nil
}

func (s *ingestionService) confirmUpload(ctx context.Context, assetID string) (asset, error) {
	s.mu.RLock()
	item, ok := s.assets[assetID]
	s.mu.RUnlock()
	if !ok {
		return asset{}, errAssetNotFound
	}
	found, err := s.store.objectExists(ctx, s.bucket, item.ObjectKey)
	if err != nil {
		return asset{}, err
	}
	if !found {
		return item, errUploadPending
	}
	jobID, err := randomID("job")
	if err != nil {
		return asset{}, err
	}
	item.State = stateQueued
	item.JobID = jobID
	s.mu.Lock()
	s.assets[assetID] = item
	s.mu.Unlock()
	return item, nil
}

func (s *ingestionService) completeJob(assetID, jobID string) (asset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.assets[assetID]
	if !ok {
		return asset{}, errAssetNotFound
	}
	if item.State != stateQueued || item.JobID != jobID {
		return asset{}, errJobConflict
	}
	item.State = stateReady
	s.assets[assetID] = item
	return item, nil
}

func (s *ingestionService) deliveryURL(ctx context.Context, assetID, requestID string) (presignResult, error) {
	s.mu.RLock()
	item, ok := s.assets[assetID]
	s.mu.RUnlock()
	if !ok {
		return presignResult{}, errAssetNotFound
	}
	if item.State != stateReady {
		return presignResult{}, errAssetNotReady
	}
	return s.store.presignGet(ctx, s.bucket, item.ObjectKey, item.Filename, requestID)
}

func randomID(prefix string) (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(raw[:]), nil
}

func validSegment(value string) bool {
	return value != "" && !strings.ContainsAny(value, "/\\")
}

var (
	errAssetNotFound = errors.New("asset not found")
	errUploadPending = errors.New("upload is still pending")
	errJobConflict   = errors.New("job does not match queued asset")
	errAssetNotReady = errors.New("asset is not ready")
)
