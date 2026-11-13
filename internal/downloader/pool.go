package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// AssetTask represents a single media URL to download.
type AssetTask struct {
	ID        int
	SourceURL string
	Title     string
	MimeType  string
	Date      time.Time
}

// AssetResult reports the outcome of downloading a single AssetTask.
type AssetResult struct {
	Task      AssetTask `json:"task"`
	LocalPath string    `json:"local_path"`
	Bytes     int64     `json:"bytes"`
	SHA256    string    `json:"sha256,omitempty"`
	Skipped   bool      `json:"skipped"`
	Err       error     `json:"-"`
	ErrorMsg  string    `json:"error,omitempty"`
}

// Summary aggregates statistics for a batch download run.
type Summary struct {
	Total      int           `json:"total"`
	Succeeded  int           `json:"succeeded"`
	Skipped    int           `json:"skipped"`
	Failed     int           `json:"failed"`
	TotalBytes int64         `json:"total_bytes"`
	Duration   time.Duration `json:"duration_ms"`
	Results    []AssetResult `json:"results"`
}

// ProgressUpdate is emitted as workers complete files.
type ProgressUpdate struct {
	Completed  int
	Total      int
	Succeeded  int
	Skipped    int
	Failed     int
	TotalBytes int64
	LastResult AssetResult
}

// PoolOptions configures the concurrent media downloader pool.
type PoolOptions struct {
	Concurrency int
	OutputDir   string
	FlatLayout  bool // if false, preserves year/month or wp-content/uploads relative structure
	Resume      bool // skip existing non-empty files
	Timeout     time.Duration
	UserAgent   string
}

func DefaultPoolOptions(outDir string) PoolOptions {
	return PoolOptions{
		Concurrency: 6,
		OutputDir:   outDir,
		FlatLayout:  false,
		Resume:      true,
		Timeout:     60 * time.Second,
		UserAgent:   "Quarry/0.1.0 (+https://github.com/AryanHamedani/quarry; WordPress Media Archiver)",
	}
}

// DownloadBatch downloads a list of AssetTasks concurrently using a bounded worker pool.
func DownloadBatch(ctx context.Context, tasks []AssetTask, opts PoolOptions, onProgress func(ProgressUpdate)) (Summary, error) {
	start := time.Now()
	if opts.Concurrency <= 0 {
		opts.Concurrency = 6
	}
	if opts.Concurrency > 32 {
		opts.Concurrency = 32
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	if opts.UserAgent == "" {
		opts.UserAgent = DefaultPoolOptions(opts.OutputDir).UserAgent
	}

	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return Summary{}, fmt.Errorf("creating media output directory %s: %w", opts.OutputDir, err)
	}

	// Deduplicate tasks by SourceURL
	unique := make([]AssetTask, 0, len(tasks))
	seen := make(map[string]struct{}, len(tasks))
	for _, t := range tasks {
		u := strings.TrimSpace(t.SourceURL)
		if u == "" {
			continue
		}
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		unique = append(unique, t)
	}

	total := len(unique)
	if total == 0 {
		return Summary{Duration: time.Since(start)}, nil
	}

	client := &http.Client{
		Timeout: opts.Timeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: opts.Concurrency * 2,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	taskCh := make(chan AssetTask, total)
	for _, t := range unique {
		taskCh <- t
	}
	close(taskCh)

	results := make([]AssetResult, 0, total)
	var mu sync.Mutex
	var completed, succeeded, skipped, failed int32
	var totalBytes int64

	var wg sync.WaitGroup
	workers := opts.Concurrency
	if workers > total {
		workers = total
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskCh {
				select {
				case <-ctx.Done():
					return
				default:
				}
				res := downloadOne(ctx, client, task, opts)

				c := int(atomic.AddInt32(&completed, 1))
				var s, sk, f int
				if res.Err != nil {
					res.ErrorMsg = res.Err.Error()
					f = int(atomic.AddInt32(&failed, 1))
					s = int(atomic.LoadInt32(&succeeded))
					sk = int(atomic.LoadInt32(&skipped))
				} else if res.Skipped {
					sk = int(atomic.AddInt32(&skipped, 1))
					s = int(atomic.LoadInt32(&succeeded))
					f = int(atomic.LoadInt32(&failed))
				} else {
					s = int(atomic.AddInt32(&succeeded, 1))
					sk = int(atomic.LoadInt32(&skipped))
					f = int(atomic.LoadInt32(&failed))
				}
				tb := atomic.AddInt64(&totalBytes, res.Bytes)

				mu.Lock()
				results = append(results, res)
				mu.Unlock()

				if onProgress != nil {
					onProgress(ProgressUpdate{
						Completed:  c,
						Total:      total,
						Succeeded:  s,
						Skipped:    sk,
						Failed:     f,
						TotalBytes: tb,
						LastResult: res,
					})
				}
			}
		}()
	}

	wg.Wait()

	return Summary{
		Total:      total,
		Succeeded:  int(succeeded),
		Skipped:    int(skipped),
		Failed:     int(failed),
		TotalBytes: totalBytes,
		Duration:   time.Since(start),
		Results:    results,
	}, ctx.Err()
}

func resolveDestPath(outDir string, task AssetTask, flat bool) string {
	parsed, err := url.Parse(task.SourceURL)
	if err != nil || parsed.Path == "" {
		return filepath.Join(outDir, fmt.Sprintf("media-%d.bin", task.ID))
	}

	cleanURLPath := path.Clean(strings.TrimLeft(parsed.Path, "/"))
	baseName := path.Base(cleanURLPath)
	if baseName == "." || baseName == "/" || baseName == "" {
		baseName = fmt.Sprintf("media-%d.bin", task.ID)
	}

	if flat {
		if task.ID > 0 {
			return filepath.Join(outDir, fmt.Sprintf("%d-%s", task.ID, baseName))
		}
		return filepath.Join(outDir, baseName)
	}

	// Preserve relative structure after /wp-content/uploads/ if present
	if idx := strings.Index(cleanURLPath, "wp-content/uploads/"); idx != -1 {
		rel := strings.TrimPrefix(cleanURLPath[idx:], "wp-content/uploads/")
		if rel != "" && !strings.Contains(rel, "..") {
			return filepath.Join(outDir, filepath.FromSlash(rel))
		}
	}
	if !task.Date.IsZero() {
		return filepath.Join(outDir, task.Date.Format("2006"), task.Date.Format("01"), baseName)
	}
	return filepath.Join(outDir, baseName)
}

func downloadOne(ctx context.Context, client *http.Client, task AssetTask, opts PoolOptions) AssetResult {
	dest := resolveDestPath(opts.OutputDir, task, opts.FlatLayout)
	res := AssetResult{
		Task:      task,
		LocalPath: dest,
	}

	if opts.Resume {
		if st, err := os.Stat(dest); err == nil && st.Size() > 0 {
			res.Bytes = st.Size()
			res.Skipped = true
			return res
		}
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		res.Err = err
		return res
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, task.SourceURL, nil)
	if err != nil {
		res.Err = err
		return res
	}
	req.Header.Set("User-Agent", opts.UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		res.Err = err
		return res
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		res.Err = fmt.Errorf("HTTP %d for %s", resp.StatusCode, task.SourceURL)
		return res
	}

	tmpPath := dest + ".part"
	f, err := os.Create(tmpPath)
	if err != nil {
		res.Err = err
		return res
	}

	hasher := sha256.New()
	mw := io.MultiWriter(f, hasher)
	written, copyErr := io.Copy(mw, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		res.Err = copyErr
		return res
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		res.Err = closeErr
		return res
	}

	if err := os.Rename(tmpPath, dest); err != nil {
		_ = os.Remove(tmpPath)
		res.Err = err
		return res
	}

	res.Bytes = written
	res.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	return res
}
