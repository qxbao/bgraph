// Package scancache scans imports with a cache keyed by content (docs/architecture.md, "Index and caches": the cache unit does not depend on the commit).
//
// Cache keeps an in-memory map and, if opened with Open, a durable SQLite store (<dir>/scan.sqlite) so later runs need not
// re-read unchanged files. The store is only an accelerator: any store error (lock, corruption, full disk) degrades to memory-only with a warning and
// never breaks a selection.
package scancache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/qxbao/bgraph/internal/model"
	cppscan "github.com/qxbao/bgraph/internal/scan/cpp"
	dartscan "github.com/qxbao/bgraph/internal/scan/dart"
	goscan "github.com/qxbao/bgraph/internal/scan/golang"
	jsscan "github.com/qxbao/bgraph/internal/scan/js"
	phpscan "github.com/qxbao/bgraph/internal/scan/php"
	pyscan "github.com/qxbao/bgraph/internal/scan/python"
	rbscan "github.com/qxbao/bgraph/internal/scan/ruby"
	rsscan "github.com/qxbao/bgraph/internal/scan/rust"
	sfcscan "github.com/qxbao/bgraph/internal/scan/sfc"
	swiftscan "github.com/qxbao/bgraph/internal/scan/swift"
	"github.com/qxbao/bgraph/internal/tree"
)

// ScannerVersion: bump this string whenever scanner behavior changes (lexer, import recognition rules) to invalidate the durable cache.
const ScannerVersion = "js-1+sfc-1+rs-2+py-2+go-2+php-5+rb-9+swift-6+cpp-3+dart-4"

// Cache is a caching eco.Scanner, safe for concurrent use.
type Cache struct {
	mu       sync.Mutex
	m        map[string]model.ScanResult
	dirty    map[string]struct{} // keys in m not yet written to the store
	db       *sql.DB             // nil: memory-only
	get      *sql.Stmt
	getLink  *sql.Stmt
	links    map[string]string   // blob OID of a symbolic link → its target (see LinkTarget)
	newLinks map[string]struct{} // links not yet written to the store
	warnings []string
	Hits     int
	Misses   int
}

// New creates an empty, memory-only cache.
func New() *Cache {
	return &Cache{m: map[string]model.ScanResult{}, dirty: map[string]struct{}{}, links: map[string]string{}, newLinks: map[string]struct{}{}}
}

// LinkTarget returns the target stored in the symbolic-link blob oid, if known (gitx.LinkCache). Blobs are content-addressed,
// so an entry never goes stale and does not depend on the scanner version.
func (c *Cache) LinkTarget(oid string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.links[oid]; ok {
		return t, true
	}
	if c.getLink == nil {
		return "", false
	}
	var t string
	if err := c.getLink.QueryRow(oid).Scan(&t); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			c.disableDB(err)
		}
		return "", false
	}
	c.links[oid] = t
	return t, true
}

// PutLinkTarget remembers the target of the symbolic-link blob oid; Flush writes it to the store.
func (c *Cache) PutLinkTarget(oid, target string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.links[oid]; ok {
		return
	}
	c.links[oid] = target
	c.newLinks[oid] = struct{}{}
}

// DefaultDir returns the repo's cache directory: os.UserCacheDir()/bgraph/<first 16 hex of sha256(absolute path)>.
// Never inside the repo.
func DefaultDir(repoRoot string) (string, error) {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", err
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(abs))
	dir := filepath.Join(base, "bgraph", hex.EncodeToString(h[:])[:16])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Warnings returns a copy of the warnings about the durable store (degraded to memory, corrupt DB renamed, ...).
func (c *Cache) Warnings() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.warnings...)
}

// warn records a warning; call while holding c.mu (or before c is shared).
func (c *Cache) warn(format string, a ...any) {
	c.warnings = append(c.warnings, fmt.Sprintf(format, a...))
}

// lang picks the scanner by file extension.
func lang(p string) string {
	// .inc is both a PHP and a C/C++ include extension; Scan picks by content.
	if strings.EqualFold(path.Ext(p), ".inc") {
		return "inc"
	}
	if cppscan.Native(p) {
		return "cpp"
	}
	if phpscan.Source(p) {
		return "php"
	}
	switch path.Ext(p) {
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		return "js"
	case ".vue", ".svelte", ".astro", ".mdx":
		return "sfc"
	case ".rs":
		return "rs"
	case ".py":
		return "py"
	case ".go":
		return "go"
	case ".swift":
		return "swift"
	case ".dart":
		return "dart"
	case ".rb", ".rake", ".ru", ".gemspec":
		return "rb"
	}
	switch path.Base(p) {
	case "Gemfile", "Rakefile", "rakefile":
		return "rb"
	}
	return ""
}

// phpInclude reports whether an .inc file holds PHP code: PHP is only parsed
// inside an open tag, so a file without one has no PHP references.
func phpInclude(b []byte) bool {
	return bytes.Contains(bytes.ToLower(b), []byte("<?php")) || bytes.Contains(b, []byte("<?="))
}

// cacheKey: the key combines scanner version, language, file extension (the extension changes how lexing works: JSX in .tsx/.ts) and the content key.
func cacheKey(l, p, key string) string {
	return ScannerVersion + ":" + l + ":" + strings.ToLower(path.Ext(p)) + ":" + key
}

// lookup searches memory, then the durable store. Does not touch Hits/Misses.
func (c *Cache) lookup(ck string) (model.ScanResult, bool) {
	c.mu.Lock()
	if r, ok := c.m[ck]; ok {
		c.mu.Unlock()
		return r, true
	}
	stmt := c.get
	c.mu.Unlock()
	if stmt == nil {
		return model.ScanResult{}, false
	}
	var blob []byte
	err := stmt.QueryRow(ck).Scan(&blob)
	if err == nil {
		r, derr := decode(blob)
		c.mu.Lock()
		defer c.mu.Unlock()
		if derr != nil { // corrupt row: treat as a miss, Flush will overwrite it
			c.warn("scancache: skipping corrupt cache row: %v", derr)
			return model.ScanResult{}, false
		}
		c.m[ck] = r
		return r, true
	}
	if !errors.Is(err, sql.ErrNoRows) {
		c.mu.Lock()
		c.disableDB(err)
		c.mu.Unlock()
	}
	return model.ScanResult{}, false
}

// Scan scans file p of tree t.
func (c *Cache) Scan(t tree.Tree, p string) (model.ScanResult, error) {
	l := lang(p)
	if l == "" {
		return model.ScanResult{}, fmt.Errorf("no scanner for %s", p)
	}
	key := t.Key(p)
	ck := ""
	if key != "" {
		ck = cacheKey(l, p, key)
		if r, ok := c.lookup(ck); ok {
			c.mu.Lock()
			c.Hits++
			c.mu.Unlock()
			return r, nil
		}
	}
	b, err := t.Read(p)
	if err != nil {
		return model.ScanResult{}, err
	}
	var r model.ScanResult
	switch l {
	case "inc":
		if phpInclude(b) {
			r = phpscan.Scan(b)
		} else {
			r = cppscan.Source(b)
		}
	case "cpp":
		r = cppscan.Source(b)
	case "sfc":
		r = sfcscan.Scan(p, b)
	case "rs":
		r = rsscan.Scan(p, b)
	case "py":
		r = pyscan.Scan(p, b)
	case "go":
		r = goscan.Scan(p, b)
	case "rb":
		r = rbscan.Scan(p, b)
	case "php":
		r = phpscan.Scan(b)
	case "swift":
		r = swiftscan.Scan(b)
	case "dart":
		r = dartscan.Scan(b)
	default:
		r = jsscan.Scan(p, b)
	}
	c.mu.Lock()
	c.Misses++
	if ck != "" {
		c.m[ck] = r
		c.dirty[ck] = struct{}{}
	}
	c.mu.Unlock()
	return r, nil
}

// Prefetch scans in parallel (workers goroutines, default 16) every file in paths whose key is not yet in the cache (memory or
// store), so later Scan calls all hit. Files with no scanner are skipped; file read errors are non-fatal (Scan will return them later).
// Returns an error only when ctx is cancelled.
func (c *Cache) Prefetch(ctx context.Context, t tree.Tree, paths []string, workers int) error {
	if workers <= 0 {
		workers = 16
	}
	ch := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range ch {
				if ctx.Err() != nil {
					continue
				}
				_, _ = c.Scan(t, p) // Scan skips the read itself on a cache hit; read errors are left for a later Scan to report
			}
		}()
	}
	for _, p := range paths {
		if lang(p) == "" {
			continue
		}
		select {
		case ch <- p:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(ch)
	wg.Wait()
	return ctx.Err()
}
