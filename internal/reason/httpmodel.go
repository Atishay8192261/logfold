package reason

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Atishay8192261/praxis/internal/core"
	"github.com/Atishay8192261/praxis/internal/tool"
)

// HTTPModel talks to an OpenAI-compatible chat endpoint (llama.cpp server,
// Ollama's /v1 compatibility layer, LM Studio, vLLM, etc.). It does not use
// the vendor's native function-calling; instead Praxis defines its own compact
// JSON action protocol and parses it, so behavior is identical across models.
type HTTPModel struct {
	BaseURL string // e.g. http://127.0.0.1:11434/v1
	Model   string
	APIKey  string
	HTTP    *http.Client

	// MaxObservation caps how many characters of each observation are fed back
	// to the model, to keep prompts bounded.
	MaxObservation int
}

// NewHTTPModel constructs a model backend. Missing values get sane defaults.
func NewHTTPModel(baseURL, model, apiKey string) *HTTPModel {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434/v1"
	}
	if model == "" {
		model = "llama3.1"
	}
	return &HTTPModel{
		BaseURL:        strings.TrimRight(baseURL, "/"),
		Model:          model,
		APIKey:         apiKey,
		HTTP:           &http.Client{Timeout: 120 * time.Second},
		MaxObservation: 2000,
	}
}

func (m *HTTPModel) Name() string { return "http-model(" + m.Model + ")" }

type apiMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatReq struct {
	Model       string   `json:"model"`
	Messages    []apiMsg `json:"messages"`
	Temperature float64  `json:"temperature"`
	Stream      bool     `json:"stream"`
}

type chatResp struct {
	Choices []struct {
		Message apiMsg `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (m *HTTPModel) Next(ctx context.Context, req Request) (Decision, error) {
	body, err := json.Marshal(chatReq{
		Model:       m.Model,
		Temperature: 0,
		Stream:      false,
		Messages: []apiMsg{
			{Role: "system", Content: systemPrompt(req.Tools)},
			{Role: "user", Content: m.userPrompt(req)},
		},
	})
	if err != nil {
		return Decision{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Decision{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if m.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+m.APIKey)
	}

	resp, err := m.HTTP.Do(httpReq)
	if err != nil {
		return Decision{}, fmt.Errorf("model request failed: %w", err)
	}
	defer resp.Body.Close()

	var out chatResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Decision{}, fmt.Errorf("decode model response: %w", err)
	}
	if out.Error != nil {
		return Decision{}, fmt.Errorf("model error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return Decision{}, fmt.Errorf("model returned no choices")
	}
	return parseDecision(out.Choices[0].Message.Content), nil
}

func systemPrompt(tools []tool.Spec) string {
	specs, _ := json.MarshalIndent(tools, "", "  ")
	return `You are Praxis, an autonomous DevOps engineer. You diagnose and fix broken
services by reasoning step by step and using tools.

You have these tools (JSON schema):
` + string(specs) + `

Respond with EXACTLY ONE JSON object and nothing else. Two shapes are allowed:

To use a tool:
{"thought": "<short reasoning>", "action": {"tool": "<tool name>", "args": {<arguments>}}}

When you have solved or fully diagnosed the goal:
{"thought": "<short reasoning>", "final": "<your findings and the fix>"}

Rules:
- Use one tool per step and observe the result before deciding the next action.
- Prefer read-only inspection (list_dir, read_file, tail_log, grep, check_port,
  http_probe) before making changes with write_file or run_cmd.
- Do not repeat a tool call that you have already made with the same arguments.
- Keep "thought" to one sentence. Output no prose outside the JSON object.`
}

func (m *HTTPModel) userPrompt(req Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "GOAL: %s\n\n", req.Goal)
	if len(req.Memories) > 0 {
		b.WriteString("Relevant notes from past runs:\n")
		for _, note := range req.Memories {
			fmt.Fprintf(&b, "- %s\n", note)
		}
		b.WriteString("\n")
	}
	if len(req.Steps) == 0 {
		b.WriteString("No actions taken yet. Decide the first action.\n")
		return b.String()
	}
	b.WriteString("Transcript so far:\n")
	for _, s := range req.Steps {
		switch s.Kind {
		case core.StepPlan:
			fmt.Fprintf(&b, "THOUGHT: %s\n", s.Thought)
		case core.StepToolCall:
			if s.ToolCall != nil {
				args, _ := json.Marshal(s.ToolCall.Args)
				fmt.Fprintf(&b, "ACTION: %s %s\n", s.ToolCall.Name, string(args))
			}
		case core.StepObservation:
			if s.Obs != nil {
				out := s.Obs.Output
				if s.Obs.Failed() {
					out = "ERROR: " + s.Obs.Error
				}
				fmt.Fprintf(&b, "OBSERVATION: %s\n", m.truncate(out))
			}
		}
	}
	b.WriteString("\nDecide the next action.\n")
	return b.String()
}

func (m *HTTPModel) truncate(s string) string {
	max := m.MaxObservation
	if max <= 0 {
		max = 2000
	}
	if len(s) > max {
		return s[:max] + "...[truncated]"
	}
	return s
}

// parseDecision extracts the agent's decision from a model response. It is
// forgiving: it scans every balanced JSON object in the response (even when the
// model wraps its answer in prose or code fences, or restates the format as an
// example first) and returns the decision from the LAST object that carries a
// real action or a non-empty final. Free text with no usable object becomes a
// final answer.
func parseDecision(content string) Decision {
	var best *Decision
	for _, js := range candidateObjects(content) {
		var ra struct {
			Thought string `json:"thought"`
			Action  *struct {
				Tool string         `json:"tool"`
				Args map[string]any `json:"args"`
			} `json:"action"`
			Final string `json:"final"`
		}
		if err := json.Unmarshal([]byte(js), &ra); err != nil {
			continue
		}
		if ra.Action != nil && ra.Action.Tool != "" {
			args := ra.Action.Args
			if args == nil {
				args = map[string]any{}
			}
			d := Decision{Thought: ra.Thought, ToolCall: &core.ToolCall{Name: ra.Action.Tool, Args: args}}
			best = &d
		} else if strings.TrimSpace(ra.Final) != "" {
			d := Decision{Thought: ra.Thought, Final: ra.Final}
			best = &d
		}
	}
	if best != nil {
		return *best
	}
	return Decision{Final: strings.TrimSpace(content)}
}

// candidateObjects returns every balanced {...} object in s, in order of
// appearance (including nested ones, which simply fail to match the decision
// shape and are skipped by the caller).
func candidateObjects(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		if obj, ok := balancedFrom(s, i); ok {
			out = append(out, obj)
		}
	}
	return out
}

// balancedFrom returns the balanced {...} object beginning at index start,
// correctly skipping braces inside strings.
func balancedFrom(s string, start int) (string, bool) {
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}
