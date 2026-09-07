package memory

import (
	"math"
	"math/rand"
	"path/filepath"
	"sort"
	"testing"
)

func TestVectorizerDeterministicAndSimilarity(t *testing.T) {
	v := NewVectorizer(256)
	a1 := v.Embed("database connection refused on port 8080")
	a2 := v.Embed("database connection refused on port 8080")
	for i := range a1 {
		if a1[i] != a2[i] {
			t.Fatal("embedding is not deterministic")
		}
	}
	near := v.Embed("connection refused to the database")
	far := v.Embed("the quick brown fox jumps over the lazy dog")
	if dot(a1, near) <= dot(a1, far) {
		t.Fatalf("similar text should score higher: near=%.3f far=%.3f", dot(a1, near), dot(a1, far))
	}
}

func randUnit(rng *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	var sum float64
	for i := range v {
		v[i] = float32(rng.NormFloat64())
		sum += float64(v[i]) * float64(v[i])
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
	return v
}

func bruteTopK(vecs [][]float32, q []float32, k int) []int {
	type sc struct {
		id int
		d  float32
	}
	scores := make([]sc, len(vecs))
	for i, v := range vecs {
		scores[i] = sc{i, distance(q, v)}
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].d < scores[j].d })
	ids := make([]int, 0, k)
	for i := 0; i < k && i < len(scores); i++ {
		ids = append(ids, scores[i].id)
	}
	return ids
}

func TestHNSWRecallVsBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const n, dim, k = 800, 64, 10

	vecs := make([][]float32, n)
	h := NewHNSW(dim, 16, 200)
	for i := 0; i < n; i++ {
		vecs[i] = randUnit(rng, dim)
		h.Add(vecs[i])
	}

	var total, hitSum float64
	for q := 0; q < 50; q++ {
		query := randUnit(rng, dim)
		want := bruteTopK(vecs, query, k)
		got := h.Search(query, k, 100)

		gotSet := map[int]bool{}
		for _, m := range got {
			gotSet[m.ID] = true
		}
		hits := 0
		for _, id := range want {
			if gotSet[id] {
				hits++
			}
		}
		hitSum += float64(hits)
		total += float64(k)
	}
	recall := hitSum / total
	if recall < 0.85 {
		t.Fatalf("recall@%d too low: %.3f (want >= 0.85)", k, recall)
	}
	t.Logf("HNSW recall@%d = %.3f", k, recall)
}

func TestStoreSearchAndPersistence(t *testing.T) {
	s := NewStore(256)
	texts := []string{
		"the cat sat on the mat",
		"database connection refused",
		"out of memory error in worker",
		"nginx failed to bind to port 8080",
	}
	for _, tx := range texts {
		s.Add(tx, map[string]string{"kind": "note"})
	}

	hits := s.Search("db connection was refused", 2)
	if len(hits) == 0 || hits[0].Record.Text != "database connection refused" {
		t.Fatalf("unexpected top hit: %+v", hits)
	}

	path := filepath.Join(t.TempDir(), "mem.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	reloaded, err := LoadStore(path, 256)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if reloaded.Len() != len(texts) {
		t.Fatalf("reloaded %d records, want %d", reloaded.Len(), len(texts))
	}
	rh := reloaded.Search("db connection was refused", 1)
	if len(rh) == 0 || rh[0].Record.Text != "database connection refused" {
		t.Fatalf("reloaded search mismatch: %+v", rh)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	s, err := LoadStore(filepath.Join(t.TempDir(), "nope.json"), 256)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if s.Len() != 0 {
		t.Fatal("expected empty store")
	}
}
