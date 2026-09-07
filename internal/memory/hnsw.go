package memory

import (
	"container/heap"
	"math"
	"sort"
)

// HNSW is a hand-built Hierarchical Navigable Small World graph for approximate
// nearest-neighbor search over normalized vectors (cosine distance = 1 - dot).
//
// It is intentionally deterministic — level assignment uses a fixed-seed PRNG —
// so an index rebuilt from the same inserts is identical, which keeps Praxis
// runs reproducible.
type HNSW struct {
	dim   int
	M     int // neighbors per layer
	Mmax  int // max neighbors on layers > 0
	Mmax0 int // max neighbors on layer 0
	efC   int // candidate list size during construction
	mL    float64
	rng   *prng

	nodes []hnode
	entry int // entry point id, -1 when empty
	top   int // highest layer currently in the graph
}

type hnode struct {
	vec   []float32
	links [][]int32 // links[layer] -> neighbor ids
}

// Match is a search result: a node id and its cosine similarity to the query.
type Match struct {
	ID    int
	Score float32
}

// NewHNSW creates an index. M and efC get sane defaults when <= 0.
func NewHNSW(dim, M, efC int) *HNSW {
	if M <= 0 {
		M = 16
	}
	if efC <= 0 {
		efC = 200
	}
	return &HNSW{
		dim: dim, M: M, Mmax: M, Mmax0: 2 * M, efC: efC,
		mL:    1 / math.Log(float64(M)),
		rng:   newPRNG(0x9E3779B97F4A7C15),
		entry: -1, top: -1,
	}
}

// Len returns the number of indexed vectors.
func (h *HNSW) Len() int { return len(h.nodes) }

func dot(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// distance is cosine distance for normalized vectors.
func distance(a, b []float32) float32 { return 1 - dot(a, b) }

func (h *HNSW) randomLevel() int {
	r := h.rng.float64()
	if r <= 0 {
		r = 1e-12
	}
	lvl := int(-math.Log(r) * h.mL)
	if lvl > 31 {
		lvl = 31
	}
	return lvl
}

// Add inserts a vector and returns its id (its insertion index).
func (h *HNSW) Add(vec []float32) int {
	id := len(h.nodes)
	level := h.randomLevel()
	h.nodes = append(h.nodes, hnode{vec: vec, links: make([][]int32, level+1)})

	if h.entry == -1 {
		h.entry, h.top = id, level
		return id
	}

	ep := h.entry
	for l := h.top; l > level; l-- {
		ep = h.greedyClosest(vec, ep, l)
	}

	start := level
	if h.top < start {
		start = h.top
	}
	for l := start; l >= 0; l-- {
		cands := h.searchLayer(vec, []int{ep}, h.efC, l)
		neighbors := closestIDs(cands, h.M)
		for _, nb := range neighbors {
			h.nodes[id].links[l] = append(h.nodes[id].links[l], int32(nb))
			h.nodes[nb].links[l] = append(h.nodes[nb].links[l], int32(id))
			h.prune(nb, l)
		}
		if len(neighbors) > 0 {
			ep = neighbors[0]
		}
	}
	if level > h.top {
		h.top, h.entry = level, id
	}
	return id
}

// Search returns the k most similar vectors to q, most similar first.
func (h *HNSW) Search(q []float32, k, ef int) []Match {
	if h.entry == -1 || k <= 0 {
		return nil
	}
	if ef < k {
		ef = k
	}
	ep := h.entry
	for l := h.top; l > 0; l-- {
		ep = h.greedyClosest(q, ep, l)
	}
	res := h.searchLayer(q, []int{ep}, ef, 0)
	sort.Slice(res, func(i, j int) bool { return res[i].dist < res[j].dist })
	if len(res) > k {
		res = res[:k]
	}
	out := make([]Match, len(res))
	for i, it := range res {
		out[i] = Match{ID: it.id, Score: 1 - it.dist}
	}
	return out
}

func (h *HNSW) greedyClosest(q []float32, ep, layer int) int {
	best := ep
	bestD := distance(q, h.nodes[ep].vec)
	for {
		improved := false
		if layer < len(h.nodes[best].links) {
			for _, nb := range h.nodes[best].links[layer] {
				if d := distance(q, h.nodes[nb].vec); d < bestD {
					bestD, best, improved = d, int(nb), true
				}
			}
		}
		if !improved {
			return best
		}
	}
}

func (h *HNSW) searchLayer(q []float32, eps []int, ef, layer int) []item {
	visited := make(map[int]bool, ef*2)
	cand := &minHeap{}
	res := &maxHeap{}
	for _, e := range eps {
		d := distance(q, h.nodes[e].vec)
		visited[e] = true
		heap.Push(cand, item{e, d})
		heap.Push(res, item{e, d})
	}
	for cand.Len() > 0 {
		c := heap.Pop(cand).(item)
		if res.Len() >= ef && c.dist > (*res)[0].dist {
			break
		}
		if layer < len(h.nodes[c.id].links) {
			for _, nb := range h.nodes[c.id].links[layer] {
				id := int(nb)
				if visited[id] {
					continue
				}
				visited[id] = true
				d := distance(q, h.nodes[id].vec)
				if res.Len() < ef || d < (*res)[0].dist {
					heap.Push(cand, item{id, d})
					heap.Push(res, item{id, d})
					if res.Len() > ef {
						heap.Pop(res)
					}
				}
			}
		}
	}
	out := make([]item, res.Len())
	copy(out, *res)
	return out
}

func (h *HNSW) prune(id, layer int) {
	max := h.Mmax
	if layer == 0 {
		max = h.Mmax0
	}
	links := h.nodes[id].links[layer]
	if len(links) <= max {
		return
	}
	base := h.nodes[id].vec
	items := make([]item, len(links))
	for i, nb := range links {
		items[i] = item{int(nb), distance(base, h.nodes[nb].vec)}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].dist < items[j].dist })
	kept := make([]int32, max)
	for i := 0; i < max; i++ {
		kept[i] = int32(items[i].id)
	}
	h.nodes[id].links[layer] = kept
}

func closestIDs(cands []item, m int) []int {
	sort.Slice(cands, func(i, j int) bool { return cands[i].dist < cands[j].dist })
	if len(cands) > m {
		cands = cands[:m]
	}
	ids := make([]int, len(cands))
	for i, c := range cands {
		ids[i] = c.id
	}
	return ids
}

// --- small heaps over (id, distance) ---

type item struct {
	id   int
	dist float32
}

type minHeap []item // ascending distance (nearest pops first)

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i].dist < h[j].dist }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(item)) }
func (h *minHeap) Pop() any          { o := *h; n := len(o); it := o[n-1]; *h = o[:n-1]; return it }

type maxHeap []item // descending distance (farthest at root)

func (h maxHeap) Len() int           { return len(h) }
func (h maxHeap) Less(i, j int) bool { return h[i].dist > h[j].dist }
func (h maxHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *maxHeap) Push(x any)        { *h = append(*h, x.(item)) }
func (h *maxHeap) Pop() any          { o := *h; n := len(o); it := o[n-1]; *h = o[:n-1]; return it }

// --- deterministic PRNG (xorshift64) ---

type prng struct{ s uint64 }

func newPRNG(seed uint64) *prng {
	if seed == 0 {
		seed = 0x9E3779B97F4A7C15
	}
	return &prng{s: seed}
}

func (r *prng) next() uint64 {
	r.s ^= r.s << 13
	r.s ^= r.s >> 7
	r.s ^= r.s << 17
	return r.s
}

func (r *prng) float64() float64 { return float64(r.next()>>11) / float64(1<<53) }
