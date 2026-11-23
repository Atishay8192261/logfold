package logscan

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Atishay8192261/logfold/internal/triage"
)

var (
	textSignal   = regexp.MustCompile(triage.ErrorPattern)
	uuid         = regexp.MustCompile(`(?i)\b[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}\b`)
	hexID        = regexp.MustCompile(`(?i)\b(?:0x[0-9a-f]{6,}|[0-9a-f]{16,})\b`)
	dynamicField = regexp.MustCompile(`(?i)(?:^|[ \t,])(?:request[_-]?id|trace[_-]?id|span[_-]?id|correlation[_-]?id|duration(?:_ms)?|elapsed(?:_ms)?)[ \t]*[=:][ \t]*(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
)

type event struct {
	level, message string
	at             time.Time
}

func parseEvent(line string) event {
	if line[0] == '{' {
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &fields) == nil {
			parsed := event{
				level:   normalizeLevel(jsonField(fields, "level", "severity", "severityText", "lvl")),
				message: jsonField(fields, "message", "msg", "error", "event"),
				at:      parseTimestamp(jsonField(fields, "timestamp", "@timestamp", "time", "ts")),
			}
			if parsed.message == "" {
				parsed.message = line
			}
			return parsed
		}
	}

	prefix, rest, _ := strings.Cut(line, " ")
	at := parseTimestamp(strings.Trim(prefix, "[]"))
	if !at.IsZero() {
		line = strings.TrimSpace(rest)
	}
	levelText := strings.TrimPrefix(line, "[")
	end := strings.IndexAny(levelText, " ]:\t")
	if end < 0 {
		end = len(levelText)
	}
	if level := normalizeLevel(levelText[:end]); level != "" {
		return event{level, strings.TrimLeft(levelText[end:], "] :\t-"), at}
	}
	return event{message: line, at: at}
}

func jsonField(fields map[string]json.RawMessage, names ...string) string {
	for _, name := range names {
		value := fields[name]
		if len(value) == 0 {
			continue
		}
		var s string
		switch {
		case value[0] == '"' && json.Unmarshal(value, &s) == nil:
			if s = strings.TrimSpace(s); s != "" {
				return s
			}
		case value[0] >= '0' && value[0] <= '9':
			return string(value)
		}
	}
	return ""
}

func normalizeLevel(level string) string {
	switch strings.ToLower(level) {
	case "panic", "fatal", "critical", "crit", "alert", "emerg", "60":
		return "fatal"
	case "err", "error", "50":
		return "error"
	case "warn", "warning", "40":
		return "warn"
	case "info", "notice", "30":
		return "info"
	case "debug", "trace", "10", "20":
		return "debug"
	default:
		return ""
	}
}

func parseTimestamp(s string) time.Time {
	if len(s) < 10 {
		return time.Time{}
	}
	if s[0] >= '0' && s[0] <= '9' && (len(s) == 10 || len(s) == 13) {
		n, err := strconv.ParseInt(s, 10, 64)
		if err == nil {
			if len(s) == 13 {
				return time.UnixMilli(n).UTC()
			}
			return time.Unix(n, 0).UTC()
		}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func canonical(message string) string {
	if strings.ContainsAny(message, "=:") {
		message = dynamicField.ReplaceAllString(message, " ")
	}
	message = uuid.ReplaceAllString(message, "<id>")
	message = hexID.ReplaceAllString(message, "<id>")
	return strings.Join(strings.Fields(strings.ToLower(message)), " ")
}
