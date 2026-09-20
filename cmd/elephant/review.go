package main

import (
	"fmt"
	"io"

	"elephant/internal/app"
)

func printReviewHuman(out io.Writer, findings []app.ReviewFinding) {
	if len(findings) == 0 {
		fmt.Fprintln(out, "elephant review: no findings")
		return
	}
	for _, f := range findings {
		where := f.Source
		if f.EntryID != "" {
			where += " " + f.EntryID
		}
		fmt.Fprintf(out, "[%s/%s] %s (%s): %s — action: %s\n", f.Severity, f.Code, where, f.Source, f.Detail, f.Action)
	}
}
