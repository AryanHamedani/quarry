package downloader

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
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
	KnownSize int64 // filesize reported by WordPress REST API media_details.filesize (0 if unknown)
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
	Concurrency     int
	OutputDir       string
	FlatLayout      bool // if false, preserves year/month or wp-content/uploads relative structure
	Resume          bool // skip existing non-empty files
	Timeout         time.Duration
	UserAgent       string
	CookieHeader    string // Optional raw Cookie header value for authenticated downloads
	BearerToken     string // Optional Bearer token for authenticated downloads
	InsecureSkipTLS bool
	CustomHeaders   map[string]string
}

func decorateDownloadRequest(req *http.Request, opts PoolOptions) {
	req.Header.Set("User-Agent", opts.UserAgent)
	if c := strings.TrimSpace(opts.CookieHeader); c != "" {
		if strings.HasPrefix(strings.ToLower(c), "cookie:") {
			c = strings.TrimSpace(c[len("cookie:"):])
		}
		if c != "" {
			req.Header.Set("Cookie", c)
		}
	}
	if opts.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+opts.BearerToken)
	}
	for k, v := range opts.CustomHeaders {
		req.Header.Set(k, v)
	}
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

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: opts.Concurrency * 2,
		IdleConnTimeout:     90 * time.Second,
	}
	if opts.InsecureSkipTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	client := &http.Client{
		Timeout:   opts.Timeout,
		Transport: transport,
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
	decorateDownloadRequest(req, opts)

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

// SizeEstimate summarizes pre-download size calculation across a batch of AssetTasks.
type SizeEstimate struct {
	TotalFiles      int              `json:"total_files"`
	KnownSizeFiles  int              `json:"known_size_files"`
	UnknownFiles    int              `json:"unknown_size_files"`
	TotalBytes      int64            `json:"total_bytes"`
	ByTypeBytes     map[string]int64 `json:"by_type_bytes"`
	ByTypeCounts    map[string]int   `json:"by_type_counts"`
}

// EstimateBatchSize calculates the total download size of tasks.
// It uses `KnownSize` from WordPress `media_details.filesize` first, and if `probeHEAD` is true,
// performs concurrent HTTP HEAD requests to fetch `Content-Length` for any items missing size metadata.
func EstimateBatchSize(ctx context.Context, tasks []AssetTask, opts PoolOptions, probeHEAD bool, onProgress func(done, total int)) SizeEstimate {
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

	est := SizeEstimate{
		TotalFiles:   len(unique),
		ByTypeBytes:  make(map[string]int64),
		ByTypeCounts: make(map[string]int),
	}
	if len(unique) == 0 {
		return est
	}

	if opts.Concurrency <= 0 {
		opts.Concurrency = 8
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 20 * time.Second
	}
	if opts.UserAgent == "" {
		opts.UserAgent = DefaultPoolOptions(opts.OutputDir).UserAgent
	}

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: opts.Concurrency * 2,
		IdleConnTimeout:     60 * time.Second,
	}
	if opts.InsecureSkipTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	client := &http.Client{
		Timeout:   opts.Timeout,
		Transport: transport,
	}

	type idxTask struct {
		idx  int
		task AssetTask
	}
	var needProbe []idxTask
	sizes := make([]int64, len(unique))
	for i, t := range unique {
		if t.KnownSize > 0 {
			sizes[i] = t.KnownSize
		} else if probeHEAD {
			needProbe = append(needProbe, idxTask{idx: i, task: t})
		}
	}

	if len(needProbe) > 0 {
		ch := make(chan idxTask, len(needProbe))
		for _, it := range needProbe {
			ch <- it
		}
		close(ch)

		var doneCount int32
		var wg sync.WaitGroup
		workers := min(opts.Concurrency*2, len(needProbe))
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for item := range ch {
					select {
					case <-ctx.Done():
						return
					default:
					}
					req, err := http.NewRequestWithContext(ctx, http.MethodHead, item.task.SourceURL, nil)
					if err == nil {
						decorateDownloadRequest(req, opts)
						if resp, err := client.Do(req); err == nil {
							_ = resp.Body.Close()
							if resp.ContentLength > 0 {
								sizes[item.idx] = resp.ContentLength
							}
						}
					}
					d := int(atomic.AddInt32(&doneCount, 1))
					if onProgress != nil {
						onProgress(d, len(needProbe))
					}
				}
			}()
		}
		wg.Wait()
	}

	for i, t := range unique {
		sz := sizes[i]
		group := t.MimeType
		if group == "" {
			group = "unknown"
		}
		est.ByTypeCounts[group]++
		if sz > 0 {
			est.KnownSizeFiles++
			est.TotalBytes += sz
			est.ByTypeBytes[group] += sz
		} else {
			est.UnknownFiles++
		}
	}
	return est
}

// FormatBytes renders a byte count in human-readable units (KB, MB, GB).
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
