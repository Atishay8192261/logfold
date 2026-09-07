// Package memory gives Praxis agents long-term recall. It embeds text into
// vectors with a from-scratch feature-hashing vectorizer (no model required)
// and indexes them in a hand-built HNSW graph for fast approximate
// nearest-neighbor search. Everything here is standard-library only.
package memory

import (
	"math"
	"strings"
	"unicode"
)

// DefaultDim is the embedding dimensionality.
const DefaultDim = 256

// Vectorizer turns text into a fixed-length, L2-normalized vector using the
// hashing trick over unigrams and bigrams. It is deterministic: the same text
// always maps to the same vector, so indexes rebuild identically.
type Vectorizer struct {
	Dim     int
	Bigrams bool
}

// NewVectorizer returns a vectorizer of the given dimension (<=0 uses default).
func NewVectorizer(dim int) *Vectorizer {
	if dim <= 0 {
		dim = DefaultDim
	}
	return &Vectorizer{Dim: dim, Bigrams: true}
}

// Embed maps text to a normalized vector.
func (v *Vectorizer) Embed(text string) []float32 {
	vec := make([]float32, v.Dim)
	toks := tokenize(text)
	add := func(term string) {
		h := fnv1a(term)
		idx := int(h % uint64(v.Dim))
		// A separate hash bit decides the sign, reducing collisions bias.
		sign := float32(1)
		if fnv1a(term+"\x00")&1 == 1 {
			sign = -1
		}
		vec[idx] += sign
	}
	for i, t := range toks {
		add(t)
		if v.Bigrams && i+1 < len(toks) {
			add(t + " " + toks[i+1])
		}
	}
	normalize(vec)
	return vec
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	var toks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return toks
}

func normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}

// fnv1a is a 64-bit FNV-1a hash (implemented here to keep the vector stack
// self-contained).
func fnv1a(s string) uint64 {
	const offset = 1469598103934665603
	const prime = 1099511628211
	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}
