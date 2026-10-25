package acquisition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"meshvianex/core/health"
	"meshvianex/core/packages"
)

type Source interface {
	ID() string
	Size(context.Context) (int64, error)
	ReadRange(context.Context, int64, int64) ([]byte, error)
}
type ChunkState struct {
	Hash   string `json:"hash"`
	Source string `json:"source,omitempty"`
}
type ResumeState struct {
	ObjectHash string             `json:"object_hash"`
	TotalSize  int64              `json:"total_size"`
	ChunkSize  int64              `json:"chunk_size"`
	Chunks     map[int]ChunkState `json:"chunks"`
	UpdatedAt  time.Time          `json:"updated_at"`
}
type Options struct {
	ObjectHash  string
	TotalSize   int64
	ChunkSize   int64
	Concurrency int
	StateDir    string
	Sources     []Source
	ChunkHashes map[int]string
	Health      *health.Store
	Priority    string
}

type Downloader struct{ Store packages.Store }

func (d Downloader) Acquire(ctx context.Context, o Options) (string, error) {
	if len(o.Sources) == 0 {
		return "", fmt.Errorf("at least one acquisition source is required")
	}
	if o.ChunkSize <= 0 {
		o.ChunkSize = 256 << 10
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 2
	}
	if o.Health != nil {
		o.Concurrency = AdaptiveConcurrency(o.Health.Get("network"), o.Concurrency)
	}
	if o.Concurrency < 1 {
		o.Concurrency = 1
	}
	size := o.TotalSize
	if size <= 0 {
		var err error
		size, err = o.Sources[0].Size(ctx)
		if err != nil {
			return "", err
		}
	}
	if size <= 0 {
		return "", fmt.Errorf("source returned invalid size")
	}
	if o.ObjectHash == "" {
		return "", fmt.Errorf("object hash is required")
	}
	statePath := ""
	if o.StateDir != "" {
		if err := os.MkdirAll(o.StateDir, 0o755); err != nil {
			return "", err
		}
		statePath = filepath.Join(o.StateDir, safeName(o.ObjectHash)+".resume.json")
	}
	state := ResumeState{ObjectHash: o.ObjectHash, TotalSize: size, ChunkSize: o.ChunkSize, Chunks: map[int]ChunkState{}}
	if statePath != "" {
		if b, err := os.ReadFile(statePath); err == nil {
			var saved ResumeState
			if json.Unmarshal(b, &saved) == nil && saved.ObjectHash == o.ObjectHash && saved.TotalSize == size && saved.ChunkSize == o.ChunkSize {
				state = saved
			}
		}
	}
	tmpDir := o.StateDir
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", err
	}
	tmpPath := filepath.Join(tmpDir, safeName(o.ObjectHash)+".part")
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err = f.Truncate(size); err != nil {
		return "", err
	}
	count := int((size + o.ChunkSize - 1) / o.ChunkSize)
	pending := make(chan int, count)
	var mu sync.Mutex
	for n := 0; n < count; n++ {
		if !d.validSavedChunk(f, n, state.Chunks[n], o.ChunkHashes[n], o.ChunkSize, size) {
			delete(state.Chunks, n)
			pending <- n
		}
	}
	close(pending)
	var wg sync.WaitGroup
	var first error
	var errMu sync.Mutex
	worker := func() {
		defer wg.Done()
		for n := range pending {
			var last error
			for attempt := 0; attempt < len(o.Sources); attempt++ {
				src := o.Sources[(n+attempt)%len(o.Sources)]
				offset := int64(n) * o.ChunkSize
				length := o.ChunkSize
				if remaining := size - offset; remaining < length {
					length = remaining
				}
				b, e := src.ReadRange(ctx, offset, length)
				if e == nil && int64(len(b)) == length {
					sum := sha256.Sum256(b)
					got := hex.EncodeToString(sum[:])
					if want := o.ChunkHashes[n]; want != "" && got != want {
						e = fmt.Errorf("chunk %d hash mismatch", n)
					} else if _, e = f.WriteAt(b, offset); e == nil {
						mu.Lock()
						state.Chunks[n] = ChunkState{Hash: got, Source: src.ID()}
						state.UpdatedAt = time.Now().UTC()
						e = saveState(statePath, state)
						mu.Unlock()
					}
				}
				if e == nil {
					last = nil
					break
				}
				last = e
			}
			if last != nil {
				errMu.Lock()
				if first == nil {
					first = fmt.Errorf("chunk %d: %w", n, last)
				}
				errMu.Unlock()
			}
		}
	}
	for n := 0; n < o.Concurrency; n++ {
		wg.Add(1)
		go worker()
	}
	wg.Wait()
	if first != nil {
		return "", first
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	sum := sha256.New()
	if _, err = io.Copy(sum, f); err != nil {
		return "", err
	}
	actual := hex.EncodeToString(sum.Sum(nil))
	expected := o.ObjectHash
	if len(expected) > 7 && expected[:7] == "sha256:" {
		expected = expected[7:]
	}
	if actual != expected {
		return "", fmt.Errorf("object hash mismatch: got %s", actual)
	}
	if d.Store.Root != "" {
		src, err := os.Open(tmpPath)
		if err != nil {
			return "", err
		}
		defer src.Close()
		path, err := d.Store.PutObject(expected, src)
		if err != nil {
			return "", err
		}
		_ = os.Remove(tmpPath)
		if statePath != "" {
			_ = os.Remove(statePath)
		}
		return path, nil
	}
	return tmpPath, nil
}

func (d Downloader) validSavedChunk(f *os.File, n int, saved ChunkState, expected string, chunkSize, size int64) bool {
	if saved.Hash == "" {
		return false
	}
	offset := int64(n) * chunkSize
	length := chunkSize
	if remaining := size - offset; remaining < length {
		length = remaining
	}
	b := make([]byte, length)
	if _, err := f.ReadAt(b, offset); err != nil && err != io.EOF {
		return false
	}
	sum := sha256.Sum256(b)
	got := hex.EncodeToString(sum[:])
	return got == saved.Hash && (expected == "" || expected == got)
}
func saveState(path string, state ResumeState) error {
	if path == "" {
		return nil
	}
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func safeName(s string) string {
	out := ""
	for _, c := range s {
		if (c >= 'a' && c <= 'f') || (c >= '0' && c <= '9') {
			out += string(c)
		}
	}
	if out == "" {
		return "object"
	}
	return out
}
func AdaptiveConcurrency(s health.State, configured int) int {
	if configured < 1 {
		configured = 1
	}
	switch s.Status {
	case "failed":
		return 1
	case "degraded":
		if configured > 2 {
			return 2
		}
	case "unstable":
		if configured > 3 {
			return 3
		}
	}
	if s.LossPercent > 10 && configured > 2 {
		return 2
	}
	return configured
}

type FileSource struct {
	Name string
	Path string
}

func (s FileSource) ID() string { return s.Name }
func (s FileSource) Size(context.Context) (int64, error) {
	st, err := os.Stat(s.Path)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}
func (s FileSource) ReadRange(ctx context.Context, offset, length int64) ([]byte, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	b := make([]byte, length)
	n, err := io.ReadFull(f, b)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return b[:n], nil
}

type HTTPSource struct {
	Name   string
	URL    string
	Client *http.Client
}

func (s HTTPSource) ID() string { return s.Name }
func (s HTTPSource) Size(ctx context.Context) (int64, error) {
	c := s.Client
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, s.URL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return 0, fmt.Errorf("source returned %s", resp.Status)
	}
	return resp.ContentLength, nil
}
func (s HTTPSource) ReadRange(ctx context.Context, offset, length int64) ([]byte, error) {
	c := s.Client
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && !(offset == 0 && resp.StatusCode == http.StatusOK) {
		return nil, fmt.Errorf("range request returned %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, length))
}

func SortSources(s []Source) []Source {
	out := append([]Source(nil), s...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}
