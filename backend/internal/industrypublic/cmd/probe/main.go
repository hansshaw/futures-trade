// Command probe performs a single bounded manual check of the public collector.
// It never writes a database or places an order. It is not part of the scheduler.
package main

import (
	"context"
	"cornwatch/internal/industrypublic"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func main() {
	c := industrypublic.New()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, kind := range []string{"prices", "arrivals"} {
		f := c.Prices
		if kind == "arrivals" {
			f = c.Arrivals
		}
		start := time.Now()
		o, e, err := f(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, kind, err)
			os.Exit(1)
		}
		sample := o
		if len(sample) > 2 {
			sample = sample[:2]
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"kind": kind, "observations": len(o), "events": len(e), "elapsed_ms": time.Since(start).Milliseconds(), "sample": sample, "event_summaries": e})
	}
}
