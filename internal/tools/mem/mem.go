// Package mem exposes the semantic memory store as agent tools, so a reasoning
// model can deliberately store and recall notes across runs.
package mem

import (
	"context"
	"fmt"
	"strings"

	"github.com/Atishay8192261/praxis/internal/memory"
	"github.com/Atishay8192261/praxis/internal/tool"
)

// Register installs the remember/recall tools backed by store.
func Register(reg *tool.Registry, store *memory.Store) {
	reg.Register(&remember{store})
	reg.Register(&recall{store})
}

type remember struct{ s *memory.Store }

func (t *remember) Name() string { return "remember" }
func (t *remember) Description() string {
	return "Store a note in long-term memory so future runs can recall it."
}
func (t *remember) Schema() tool.Schema {
	return tool.Schema{Params: []tool.Param{
		{Name: "text", Type: tool.TypeString, Required: true, Description: "The note to remember."},
		{Name: "tags", Type: tool.TypeString, Description: "Optional comma-separated tags."},
	}}
}
func (t *remember) Invoke(_ context.Context, a tool.Args) (string, error) {
	meta := map[string]string{}
	if tags := a.String("tags"); tags != "" {
		meta["tags"] = tags
	}
	id := t.s.Add(a.String("text"), meta)
	return fmt.Sprintf("stored memory #%d", id), nil
}

type recall struct{ s *memory.Store }

func (t *recall) Name() string { return "recall" }
func (t *recall) Description() string {
	return "Recall notes from long-term memory similar to a query."
}
func (t *recall) Schema() tool.Schema {
	return tool.Schema{Params: []tool.Param{
		{Name: "query", Type: tool.TypeString, Required: true, Description: "What to search memory for."},
		{Name: "k", Type: tool.TypeInt, Default: int64(3), Description: "How many notes to return."},
	}}
}
func (t *recall) Invoke(_ context.Context, a tool.Args) (string, error) {
	hits := t.s.Search(a.String("query"), int(a.Int("k")))
	if len(hits) == 0 {
		return "(no relevant memories)", nil
	}
	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "[%.2f] %s\n", h.Score, h.Record.Text)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
