package logscan

import "time"

type Timeline struct {
	BucketSeconds int64    `json:"bucketSeconds"`
	Buckets       []Bucket `json:"buckets"`
}

type Bucket struct {
	Start string `json:"start"`
	Count int64  `json:"count"`
}

type histogram struct {
	width, earliest, latest int64
	bins                    map[int64]int64
}

func (h *histogram) add(unix int64) {
	if h.bins == nil {
		h.bins = make(map[int64]int64)
		h.earliest, h.latest = unix, unix
	} else {
		h.earliest, h.latest = min(h.earliest, unix), max(h.latest, unix)
	}
	oldWidth := h.width
	for floor(h.latest, h.width)-floor(h.earliest, h.width) >= MaxBuckets*h.width {
		h.width *= 2
	}
	if h.width != oldWidth {
		coarser := make(map[int64]int64)
		for start, count := range h.bins {
			coarser[floor(start, h.width)] += count
		}
		h.bins = coarser
	}
	h.bins[floor(unix, h.width)]++
}

func floor(unix, width int64) int64 {
	remainder := unix % width
	if remainder < 0 {
		remainder += width
	}
	return unix - remainder
}

func (h *histogram) report() Timeline {
	result := Timeline{BucketSeconds: h.width, Buckets: []Bucket{}}
	if len(h.bins) == 0 {
		return result
	}
	for start := floor(h.earliest, h.width); start <= h.latest; start += h.width {
		result.Buckets = append(result.Buckets, Bucket{iso(time.Unix(start, 0).UTC()), h.bins[start]})
	}
	return result
}
