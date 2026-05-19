package vivero

import (
	"fmt"
	"io"
)

type ProgressOptions struct {
	JSON    bool
	Quiet   bool
	Verbose bool
	TTY     bool
}

type ProgressReporter struct {
	w    io.Writer
	opts ProgressOptions
}

func NewProgressReporter(w io.Writer, opts ProgressOptions) ProgressReporter {
	return ProgressReporter{w: w, opts: opts}
}

func (p ProgressReporter) Step(phase, message string) {
	if p.w == nil || p.opts.JSON || p.opts.Quiet {
		return
	}
	if phase == "" {
		fmt.Fprintln(p.w, message)
		return
	}
	fmt.Fprintf(p.w, "%s: %s\n", phase, message)
}
