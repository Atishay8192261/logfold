//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/Atishay8192261/logfold/internal/logscan"
)

func main() {
	scan := logscan.New()
	buffer := make([]byte, 64*1024)
	bind := func(name string, fn func([]js.Value) any) {
		js.Global().Set(name, js.FuncOf(func(_ js.Value, args []js.Value) any { return fn(args) }))
	}
	bind("logfoldStartLog", func(args []js.Value) any {
		if err := scan.Start(args[0].String(), args[1].Bool()); err != nil {
			return err.Error()
		}
		return ""
	})
	bind("logfoldLogChunk", func(args []js.Value) any {
		if args[0].Get("byteLength").Int() > len(buffer) {
			return "Oversized input chunk."
		}
		n := js.CopyBytesToGo(buffer, args[0])
		if _, err := scan.Write(buffer[:n]); err != nil {
			return err.Error()
		}
		return ""
	})
	bind("logfoldEndLog", func(_ []js.Value) any {
		scan.End()
		return nil
	})
	bind("logfoldLogReport", func(_ []js.Value) any {
		data, err := json.Marshal(scan.Report())
		if err != nil {
			return ""
		}
		return string(data)
	})
	select {}
}
