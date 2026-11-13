package downloader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestDownloadBatchAndResume(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/wp-content/uploads/2026/11/hero.png", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake-png-binary-payload-12345"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	outDir := t.TempDir()
	tasks := []AssetTask{
		{
			ID:        10,
			SourceURL: srv.URL + "/wp-content/uploads/2026/11/hero.png",
			Title:     "Hero Image",
			MimeType:  "image/png",
		},
	}

	opts := DefaultPoolOptions(outDir)
	opts.Concurrency = 2

	summary1, err := DownloadBatch(context.Background(), tasks, opts, nil)
	if err != nil {
		t.Fatalf("DownloadBatch 1 failed: %v", err)
	}
	if summary1.Succeeded != 1 || summary1.Skipped != 0 {
		t.Fatalf("unexpected summary1: %+v", summary1)
	}
	if _, err := os.Stat(summary1.Results[0].LocalPath); err != nil {
		t.Fatalf("expected downloaded file at %s: %v", summary1.Results[0].LocalPath, err)
	}

	// Second run should skip existing file via Resume=true
	summary2, err := DownloadBatch(context.Background(), tasks, opts, nil)
	if err != nil {
		t.Fatalf("DownloadBatch 2 failed: %v", err)
	}
	if summary2.Skipped != 1 || summary2.Succeeded != 0 {
		t.Fatalf("expected resume skip on second run, got: %+v", summary2)
	}
}
