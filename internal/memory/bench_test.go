package memory

import (
	"math/rand"
	"testing"
)

func benchVecs(n, dim int) [][]float32 {
	rng := rand.New(rand.NewSource(1))
	vs := make([][]float32, n)
	for i := range vs {
		vs[i] = randUnit(rng, dim)
	}
	return vs
}

func BenchmarkHNSWBuild2k(b *testing.B) {
	vs := benchVecs(2000, 128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h := NewHNSW(128, 16, 200)
		for _, v := range vs {
			h.Add(v)
		}
	}
}

func BenchmarkHNSWSearch10k(b *testing.B) {
	vs := benchVecs(10000, 128)
	h := NewHNSW(128, 16, 200)
	for _, v := range vs {
		h.Add(v)
	}
	q := benchVecs(1, 128)[0]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.Search(q, 10, 64)
	}
}

func BenchmarkEmbed(b *testing.B) {
	v := NewVectorizer(256)
	txt := "ERROR connection refused on 127.0.0.1:8080 while probing the health endpoint"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = v.Embed(txt)
	}
}
