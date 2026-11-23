//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/Atishay8192261/logfold/internal/triage"
)

func main() {
	js.Global().Set("logfoldAnalyze", js.FuncOf(analyze))
	select {}
}

func analyze(_ js.Value, args []js.Value) any {
	var input triage.Input
	if len(args) != 1 || json.Unmarshal([]byte(args[0].String()), &input) != nil {
		return `{"error":"Could not read the incident input."}`
	}
	report, err := triage.Analyze(input)
	response := struct {
		Report *triage.Report `json:"report,omitempty"`
		Error  string         `json:"error,omitempty"`
	}{Report: &report}
	if err != nil {
		response.Report = nil
		response.Error = err.Error()
	}
	out, err := json.Marshal(response)
	if err != nil {
		return `{"error":"Could not prepare the incident report."}`
	}
	return string(out)
}
