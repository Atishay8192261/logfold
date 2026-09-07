package memory

import (
	"encoding/json"
	"os"
	"sync"
)

// Record is one stored memory.
type Record struct {
	ID   int               `json:"id"`
	Text string            `json:"text"`
	Meta map[string]string `json:"meta,omitempty"`
}

// Hit is a search result: a record and its similarity score.
type Hit struct {
	Record Record  `json:"record"`
	Score  float32 `json:"score"`
}

// Store is the high-level semantic memory: it embeds text, indexes it in HNSW,
// and answers similarity queries. It is safe for concurrent use.
type Store struct {
	mu      sync.RWMutex
	vec     *Vectorizer
	index   *HNSW
	records []Record
}

// NewStore creates an empty semantic memory of the given dimension.
func NewStore(dim int) *Store {
	if dim <= 0 {
		dim = DefaultDim
	}
	return &Store{
		vec:   NewVectorizer(dim),
		index: NewHNSW(dim, 16, 200),
	}
}

// Add embeds and stores text with optional metadata, returning the record id.
func (s *Store) Add(text string, meta map[string]string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.index.Add(s.vec.Embed(text))
	s.records = append(s.records, Record{ID: id, Text: text, Meta: meta})
	return id
}

// Search returns up to k records most similar to the query, most similar first.
func (s *Store) Search(query string, k int) []Hit {
	s.mu.RLock()
	defer s.mu.RUnlock()
	matches := s.index.Search(s.vec.Embed(query), k, maxInt(k*4, 64))
	hits := make([]Hit, 0, len(matches))
	for _, m := range matches {
		if m.ID >= 0 && m.ID < len(s.records) {
			hits = append(hits, Hit{Record: s.records[m.ID], Score: m.Score})
		}
	}
	return hits
}

// Len returns the number of stored memories.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

// Records returns a copy of all stored records.
func (s *Store) Records() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, len(s.records))
	copy(out, s.records)
	return out
}

// Save writes the records to path as JSON. The index is not serialized; it is
// rebuilt deterministically on Load, since embeddings are deterministic.
func (s *Store) Save(path string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// Load reads records from path and rebuilds the index. A missing file yields an
// empty store (not an error), so first runs just start fresh.
func LoadStore(path string, dim int) (*Store, error) {
	s := NewStore(dim)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var records []Record
	if err := json.Unmarshal(b, &records); err != nil {
		return nil, err
	}
	for _, r := range records {
		s.Add(r.Text, r.Meta)
	}
	return s, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
