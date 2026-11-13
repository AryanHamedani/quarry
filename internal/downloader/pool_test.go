package downloader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func TestDownloadWithCookieAndContentDisposition(t *testing.T) {
	var receivedCookie, receivedReferer string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		receivedCookie = r.Header.Get("Cookie")
		receivedReferer = r.Header.Get("Referer")
		if r.URL.Query().Get("download_file") == "42" {
			w.Header().Set("Content-Disposition", `attachment; filename="vip-database-2026.xlsx"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("xlsx-protected-payload"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	outDir := t.TempDir()
	opts := DefaultPoolOptions(outDir)
	opts.CookieHeader = "Cookie: wordpress_logged_in_test=admin123"

	tasks := []AssetTask{
		{
			ID:        42,
			SourceURL: srv.URL + "/?download_file=42&order=wc_order_abc",
			Title:     "VIP Database",
		},
	}

	summary, err := DownloadBatch(context.Background(), tasks, opts, nil)
	if err != nil || summary.Succeeded != 1 {
		t.Fatalf("DownloadBatch failed: err=%v summary=%+v", err, summary)
	}
	if receivedCookie != "wordpress_logged_in_test=admin123" {
		t.Errorf("expected normalized Cookie header, got %q", receivedCookie)
	}
	if receivedReferer == "" {
		t.Error("expected Referer header to be set on download request")
	}
	if len(summary.Results) != 1 || !strings.HasSuffix(summary.Results[0].LocalPath, "vip-database-2026.xlsx") {
		t.Fatalf("expected Content-Disposition filename vip-database-2026.xlsx, got: %+v", summary.Results)
	}
}
