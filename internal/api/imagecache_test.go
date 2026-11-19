package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
	"github.com/rafaelvieiras/tinyfin/internal/storage/sqlite"
)

// newCachedTestServer is newTestServer with the served-image cache enabled at
// a given budget, which is what the tests below exercise.
func newCachedTestServer(t *testing.T, budget int64) (*httptest.Server, storage.Store, string) {
	t.Helper()
	st, err := sqlite.New(filepath.Join(t.TempDir(), "tinyfin.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cacheDir := filepath.Join(t.TempDir(), "cache")
	srv := startTestServer(t, st, Config{ServerName: "TestServer", ImageCacheDir: cacheDir, ImageCacheBytes: budget})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, cacheDir
}

// seedItemImage creates a library, an item and an image ref pointing at a real
// file holding data, returning the item id.
func seedItemImage(t *testing.T, st storage.Store, name string, data []byte) (storage.ItemID, string) {
	t.Helper()
	libID := createTestLibrary(t, st, name+"Lib", storage.CollectionTypeMovies)
	item := storage.Item{Type: storage.ItemTypeMovie, LibraryID: libID, Name: name}
	if err := st.UpsertItem(context.Background(), item); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	page, err := st.QueryItems(context.Background(), storage.ItemFilter{Limit: 100})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	var itemID storage.ItemID
	for _, it := range page.Items {
		if it.Name == name {
			itemID = it.ID
		}
	}
	if itemID == "" {
		t.Fatalf("item %q não apareceu na listagem", name)
	}
	imgPath := filepath.Join(t.TempDir(), name+"-poster.jpg")
	if err := os.WriteFile(imgPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := st.UpsertImageRef(context.Background(), storage.ImageRef{
		ItemID: itemID, Kind: "Primary", Index: 0, Path: imgPath, Tag: name + "tag",
	}); err != nil {
		t.Fatalf("UpsertImageRef: %v", err)
	}
	return itemID, imgPath
}

// countFiles says how many entries the cache directory holds.
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	var n int
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s): %v", dir, err)
	}
	return n
}

// TestItemImageServedFromCacheWithoutTouchingSource proves the point of the
// cache: once an image has been served, a second request is answered from the
// local copy, not from the media path. The source is made unreadable between
// the two requests — a read of it would now fail — and the second request must
// still answer 200 with the same bytes.
func TestItemImageServedFromCacheWithoutTouchingSource(t *testing.T) {
	ts, st, cacheDir := newCachedTestServer(t, 8<<20)
	user := createTestUser(t, st, "gina", "ginapass")
	token := loginToken(t, ts.URL, user.Name, "ginapass")
	get := authedClient(t, ts.URL, token)

	imgBytes := []byte("first-version-of-the-poster-bytes")
	itemID, imgPath := seedItemImage(t, st, "Cached", imgBytes)

	resp := get(http.MethodGet, "/Items/"+string(itemID)+"/Images/Primary")
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != string(imgBytes) {
		t.Fatalf("primeira requisição: status=%d bytes=%q, quero 200 e os bytes da imagem", resp.StatusCode, body)
	}
	if n := countFiles(t, cacheDir); n != 1 {
		t.Fatalf("cache tem %d arquivos depois de servir uma imagem, quero 1", n)
	}

	// A library it cannot read is exactly the case the cache exists for.
	if err := os.Chmod(imgPath, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(imgPath, 0o644) })

	resp2 := get(http.MethodGet, "/Items/"+string(itemID)+"/Images/Primary")
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK || string(body2) != string(imgBytes) {
		t.Fatalf("segunda requisição: status=%d bytes=%q, quero 200 servido do cache (a fonte está ilegível)",
			resp2.StatusCode, body2)
	}
}

// TestItemImageCacheInvalidatesOnSourceChange proves the other half: the key
// carries the source's size and mtime, so a replaced poster is served fresh —
// the cache never hands back the previous picture.
func TestItemImageCacheInvalidatesOnSourceChange(t *testing.T) {
	ts, st, _ := newCachedTestServer(t, 8<<20)
	user := createTestUser(t, st, "hugo", "hugopass")
	token := loginToken(t, ts.URL, user.Name, "hugopass")
	get := authedClient(t, ts.URL, token)

	primeira := []byte("poster-v1")
	itemID, imgPath := seedItemImage(t, st, "Trocado", primeira)

	resp := get(http.MethodGet, "/Items/"+string(itemID)+"/Images/Primary")
	defer resp.Body.Close()
	if body, _ := io.ReadAll(resp.Body); string(body) != string(primeira) {
		t.Fatalf("primeira requisição devolveu %q, quero %q", body, primeira)
	}

	// Different bytes AND different size: the key changes on either alone, and
	// using both keeps the test meaningful on filesystems with coarse mtime.
	segunda := []byte("poster-v2-with-more-bytes")
	if err := os.WriteFile(imgPath, segunda, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resp2 := get(http.MethodGet, "/Items/"+string(itemID)+"/Images/Primary")
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK || string(body2) != string(segunda) {
		t.Fatalf("depois de trocar o pôster: status=%d bytes=%q, quero 200 e %q servido do disco",
			resp2.StatusCode, body2, segunda)
	}
}

// TestImageCacheStaysWithinBudget covers the eviction side: past the budget the
// least recently used copies go away, so the cache cannot grow without bound.
func TestImageCacheStaysWithinBudget(t *testing.T) {
	dir := t.TempDir()
	// Sources live OUTSIDE the cache directory: the cache directory holds only
	// cached copies (prune walks all of it).
	srcDir := t.TempDir()
	const budget = 4096
	c := newImageCache(dir, budget)
	if c == nil {
		t.Fatal("newImageCache devolveu nil com diretório válido e orçamento positivo")
	}
	payload := make([]byte, 1024)
	for i := range payload {
		payload[i] = byte(i)
	}
	var keys []string
	for i := 0; i < 12; i++ {
		src := filepath.Join(srcDir, "src-"+string(rune('a'+i))+".jpg")
		if err := os.WriteFile(src, payload, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		key := "key" + string(rune('a'+i))
		if _, _, ok := c.fill(key, ".jpg", src); !ok {
			t.Fatalf("fill(%s) falhou", key)
		}
		keys = append(keys, key)
	}
	var total int64
	for _, k := range keys {
		if info, err := os.Stat(c.pathOf(k, ".jpg")); err == nil {
			total += info.Size()
		}
	}
	if total > budget {
		t.Errorf("cache ocupa %d bytes, acima do orçamento de %d", total, budget)
	}
	if _, err := os.Stat(c.pathOf(keys[0], ".jpg")); err == nil {
		t.Errorf("a entrada mais antiga (%s) deveria ter sido removida", keys[0])
	}
	if _, err := os.Stat(c.pathOf(keys[len(keys)-1], ".jpg")); err != nil {
		t.Errorf("a entrada mais recente deveria continuar: %v", err)
	}
}

// TestImageCacheStartsFromDiskAccount proves a restart inherits what the
// previous process wrote instead of assuming an empty directory and growing
// past the budget once more.
func TestImageCacheStartsFromDiskAccount(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "existing.jpg")
	if err := os.WriteFile(src, make([]byte, 1500), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	first := newImageCache(dir, 1<<20)
	if _, _, ok := first.fill("existente", ".jpg", src); !ok {
		t.Fatal("fill falhou")
	}

	segundo := newImageCache(dir, 1<<20)
	if got := segundo.measureLocked(); got != 1500 {
		t.Errorf("measureLocked() = %d bytes, quero 1500 (o que já estava no disco)", got)
	}
}
