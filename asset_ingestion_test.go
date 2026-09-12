package main

import (
	"context"
	"errors"
	"testing"
)

type fakeObjectStore struct {
	found bool
}

func (f fakeObjectStore) presignPut(context.Context, string, string, string, string, int64) (presignResult, error) {
	return presignResult{URL: "https://uploads.example.test/signed"}, nil
}

func (f fakeObjectStore) presignGet(context.Context, string, string, string, string) (presignResult, error) {
	return presignResult{URL: "https://downloads.example.test/signed"}, nil
}

func (f fakeObjectStore) objectExists(context.Context, string, string) (bool, error) {
	return f.found, nil
}

func TestConfirmUploadQueuesOnlyStoredAssets(t *testing.T) {
	tests := []struct {
		name      string
		found     bool
		wantState assetState
		wantErr   error
	}{
		{name: "uploaded object queues processing", found: true, wantState: stateQueued},
		{name: "missing object stays pending", found: false, wantState: stateAwaitingUpload, wantErr: errUploadPending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newIngestionService(fakeObjectStore{found: tt.found}, "media")
			service.assets["asset_1"] = asset{ID: "asset_1", ObjectKey: "creators/7/source/asset_1/clip.mp4", State: stateAwaitingUpload}

			got, err := service.confirmUpload(context.Background(), "asset_1")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("confirmUpload() error = %v, want %v", err, tt.wantErr)
			}
			if got.State != tt.wantState {
				t.Fatalf("confirmUpload() state = %q, want %q", got.State, tt.wantState)
			}
			if tt.found && got.JobID == "" {
				t.Fatal("confirmUpload() did not create a processing job")
			}
		})
	}
}
