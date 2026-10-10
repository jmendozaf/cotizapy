// Package syncer brings the database up to date with both sources.
// SET publishes its whole history on one page, so it is always read in full.
// BCP serves one page per year and currency: the first sync reads every year, later ones only the current year
// (and the previous one during January, when late corrections to December still show up).
package syncer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/jmendozaf/cotizapy/internal/bcp"
	"github.com/jmendozaf/cotizapy/internal/rates"
	"github.com/jmendozaf/cotizapy/internal/set"
	"github.com/jmendozaf/cotizapy/internal/store"
)

// SETSource fetches the SET page.
type SETSource interface {
	Fetch(ctx context.Context) ([]rates.Rate, []string, error)
}

// BCPSource fetches the BCP history pages.
type BCPSource interface {
	Form(ctx context.Context) (bcp.Form, error)
	Year(ctx context.Context, year int, currency string) ([]rates.Rate, error)
}

// Options tunes a sync.
type Options struct {
	Sources []string // rates.SET, rates.BCP; empty means both
	Full    bool     // re-read every BCP year even if the database already has history
	Workers int      // parallel BCP requests (default 3)
	Now     func() time.Time
	Logf    func(format string, args ...any)
}

// Result summarises a sync.
type Result struct {
	Source   string        `json:"source"`
	Rates    int           `json:"rates"`
	Changed  int           `json:"changed"`
	Requests int           `json:"requests"`
	Warnings []string      `json:"warnings,omitempty"`
	Duration time.Duration `json:"duration"`
}

// Run syncs the requested sources. A failing source does not stop the other one; their errors are joined.
func Run(ctx context.Context, st *store.Store, setSrc SETSource, bcpSrc BCPSource, opts Options) ([]Result, error) {
	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	sources := opts.Sources
	if len(sources) == 0 {
		sources = rates.Sources
	}

	var (
		results []Result
		errs    []error
	)
	for _, source := range sources {
		var (
			res Result
			err error
		)
		switch source {
		case rates.SET:
			res, err = syncSET(ctx, st, setSrc, opts)
		case rates.BCP:
			res, err = syncBCP(ctx, st, bcpSrc, opts)
		default:
			err = fmt.Errorf("unknown source %q", source)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", source, err))
			continue
		}
		if err := st.SetMeta(ctx, "synced_at:"+source, opts.Now().UTC().Format(time.RFC3339)); err != nil {
			return results, err
		}
		results = append(results, res)
	}
	return results, errors.Join(errs...)
}

func syncSET(ctx context.Context, st *store.Store, src SETSource, opts Options) (Result, error) {
	start := time.Now()
	opts.Logf("set: downloading %s", set.DefaultURL)
	list, warnings, err := src.Fetch(ctx)
	if err != nil {
		return Result{}, err
	}
	changed, err := st.Upsert(ctx, list)
	if err != nil {
		return Result{}, err
	}
	return Result{Source: rates.SET, Rates: len(list), Changed: changed, Requests: 1, Warnings: warnings,
		Duration: time.Since(start).Round(time.Millisecond)}, nil
}

func syncBCP(ctx context.Context, st *store.Store, src BCPSource, opts Options) (Result, error) {
	start := time.Now()
	form, err := src.Form(ctx)
	if err != nil {
		return Result{}, err
	}
	existing, err := st.Count(ctx, rates.BCP)
	if err != nil {
		return Result{}, err
	}

	now := opts.Now().In(rates.Location)
	years := form.Years
	if existing > 0 && !opts.Full {
		years = []int{now.Year()}
		if now.Month() == time.January {
			years = []int{now.Year() - 1, now.Year()}
		}
		years = slices.DeleteFunc(years, func(y int) bool { return !slices.Contains(form.Years, y) })
	}

	type job struct {
		year     int
		currency string
	}
	jobs := make(chan job)
	var (
		mu      sync.Mutex
		all     []rates.Rate
		firstEr error
		done    int
	)
	total := len(years) * len(form.Currencies)
	opts.Logf("bcp: %d years × %d currencies = %d pages", len(years), len(form.Currencies), total)

	var wg sync.WaitGroup
	for range opts.Workers {
		wg.Go(func() {
			for j := range jobs {
				list, err := src.Year(ctx, j.year, j.currency)
				mu.Lock()
				done++
				if err != nil && firstEr == nil {
					firstEr = fmt.Errorf("%d %s: %w", j.year, j.currency, err)
				}
				all = append(all, list...)
				if done%25 == 0 || done == total {
					opts.Logf("bcp: %d/%d pages", done, total)
				}
				mu.Unlock()
			}
		})
	}
	for _, y := range years {
		for _, c := range form.Currencies {
			select {
			case jobs <- job{y, c}:
			case <-ctx.Done():
			}
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if firstEr != nil {
		return Result{}, firstEr
	}

	changed, err := st.Upsert(ctx, all)
	if err != nil {
		return Result{}, err
	}
	return Result{Source: rates.BCP, Rates: len(all), Changed: changed, Requests: total + 1,
		Duration: time.Since(start).Round(time.Millisecond)}, nil
}
