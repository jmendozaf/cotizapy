// Command cotiza downloads, stores and serves the guaraní exchange rates published by the DNIT (ex SET) and the BCP.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/jmendozaf/cotizapy/internal/api"
	"github.com/jmendozaf/cotizapy/internal/bcp"
	"github.com/jmendozaf/cotizapy/internal/export"
	"github.com/jmendozaf/cotizapy/internal/rates"
	"github.com/jmendozaf/cotizapy/internal/set"
	"github.com/jmendozaf/cotizapy/internal/store"
	"github.com/jmendozaf/cotizapy/internal/syncer"
)

var version = "dev"

const usage = `cotiza: cotizaciones del guaraní (DNIT y BCP)

Uso:
  cotiza sync     [--source set|bcp] [--full]          descarga las cotizaciones y actualiza la base
  cotiza get      <moneda> [fecha] [--source set|bcp]  cotización vigente en una fecha (por defecto hoy)
  cotiza history  <moneda> [--from] [--to] [--source] [--csv]
  cotiza latest                                        última cotización de cada moneda
  cotiza series                                        monedas y rango de fechas disponibles
  cotiza export   [--dir site]                         genera el dataset estático (JSON y CSV)
  cotiza serve    [--addr :8080] [--sync-every 6h]     API HTTP
  cotiza version

La base por defecto es $COTIZA_DB o ./cotiza.db. Fechas en formato YYYY-MM-DD.
Si la fecha no tiene cotización (fin de semana, feriado), se usa la última publicada antes.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "sync":
		err = runSync(ctx, args)
	case "get":
		err = runGet(ctx, args)
	case "history":
		err = runHistory(ctx, args)
	case "latest":
		err = withStore(args, func(st *store.Store) error {
			list, err := st.Latest(ctx)
			if err != nil {
				return err
			}
			return printJSON(list)
		})
	case "series":
		err = withStore(args, func(st *store.Store) error {
			series, err := st.Series(ctx)
			if err != nil {
				return err
			}
			return printJSON(series)
		})
	case "export":
		err = runExport(ctx, args)
	case "serve":
		err = runServe(ctx, args)
	case "version", "--version":
		fmt.Println("cotiza", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "cotiza: comando desconocido %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	var exit exitError
	switch {
	case errors.As(err, &exit):
		os.Exit(int(exit))
	case err != nil:
		fmt.Fprintln(os.Stderr, "cotiza:", err)
		os.Exit(1)
	}
}

// exitError ends the program with a status code and no extra message.
type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func defaultDB() string { return envOr("COTIZA_DB", "cotiza.db") }

// parse lets flags appear before or after positional arguments ("cotiza get usd --source bcp").
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && !isBoolFlag(fs, a) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	if err := fs.Parse(flags); err != nil {
		return nil, err
	}
	return positional, nil
}

func isBoolFlag(fs *flag.FlagSet, arg string) bool {
	f := fs.Lookup(strings.TrimLeft(arg, "-"))
	if f == nil {
		return false
	}
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

func openStore(path string) (*store.Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no existe %s; corré primero: cotiza sync", path)
	}
	return store.Open(path)
}

func withStore(args []string, fn func(*store.Store) error) error {
	fs := flag.NewFlagSet("cotiza", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "archivo de base de datos")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	st, err := openStore(*db)
	if err != nil {
		return err
	}
	defer st.Close()
	return fn(st)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func checkSource(source string) error {
	if source != "" && !slices.Contains(rates.Sources, source) {
		return fmt.Errorf("--source debe ser set o bcp")
	}
	return nil
}

func runSync(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "archivo de base de datos")
	source := fs.String("source", "", "solo esta fuente: set o bcp")
	full := fs.Bool("full", false, "volver a bajar todo el historial del BCP")
	workers := fs.Int("workers", 3, "pedidos en paralelo al BCP")
	quiet := fs.Bool("quiet", false, "sin progreso")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if err := checkSource(*source); err != nil {
		return err
	}

	st, err := store.Open(*db)
	if err != nil {
		return err
	}
	defer st.Close()

	opts := syncer.Options{Full: *full, Workers: *workers}
	if *source != "" {
		opts.Sources = []string{*source}
	}
	if !*quiet {
		opts.Logf = func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
	}
	results, err := syncer.Run(ctx, st, set.NewClient(), bcp.NewClient(), opts)
	for _, r := range results {
		fmt.Fprintf(os.Stderr, "%s: %d cotizaciones, %d nuevas o corregidas, %d pedidos, %s\n",
			r.Source, r.Rates, r.Changed, r.Requests, r.Duration)
		for _, w := range r.Warnings {
			fmt.Fprintf(os.Stderr, "  aviso: %s\n", w)
		}
	}
	return err
}

func runGet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "archivo de base de datos")
	source := fs.String("source", "", "solo esta fuente: set o bcp")
	rest, err := parse(fs, args)
	if err != nil || len(rest) < 1 || len(rest) > 2 {
		return errors.New("uso: cotiza get <moneda> [fecha]")
	}
	if err := checkSource(*source); err != nil {
		return err
	}
	currency := rates.NormalizeCurrency(rest[0])
	date := rates.Today()
	if len(rest) == 2 {
		date = rest[1]
	}
	if !rates.ValidDate(date) {
		return errors.New("la fecha debe ser YYYY-MM-DD")
	}

	st, err := openStore(*db)
	if err != nil {
		return err
	}
	defer st.Close()

	sources := rates.Sources
	if *source != "" {
		sources = []string{*source}
	}
	out := map[string]any{}
	for _, s := range sources {
		q, err := st.Find(ctx, s, currency, date)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		out[s] = q
	}
	if len(out) == 0 {
		fmt.Fprintf(os.Stderr, "sin cotización de %s para %s\n", currency, date)
		return exitError(3)
	}
	if *source != "" {
		return printJSON(out[*source])
	}
	return printJSON(out)
}

func runHistory(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("history", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "archivo de base de datos")
	source := fs.String("source", rates.SET, "fuente: set o bcp")
	to := fs.String("to", rates.Today(), "hasta (YYYY-MM-DD)")
	from := fs.String("from", "", "desde (YYYY-MM-DD, por defecto 30 días antes de --to)")
	asCSV := fs.Bool("csv", false, "salida en CSV")
	rest, err := parse(fs, args)
	if err != nil || len(rest) != 1 {
		return errors.New("uso: cotiza history <moneda> [--from] [--to] [--source] [--csv]")
	}
	if err := checkSource(*source); err != nil {
		return err
	}
	end, err := time.Parse(time.DateOnly, *to)
	if err != nil {
		return errors.New("--to debe ser YYYY-MM-DD")
	}
	if *from == "" {
		*from = end.AddDate(0, 0, -30).Format(time.DateOnly)
	}
	if !rates.ValidDate(*from) {
		return errors.New("--from debe ser YYYY-MM-DD")
	}

	st, err := openStore(*db)
	if err != nil {
		return err
	}
	defer st.Close()
	list, err := st.Range(ctx, *source, rates.NormalizeCurrency(rest[0]), *from, *to)
	if err != nil {
		return err
	}
	if !*asCSV {
		return printJSON(list)
	}
	w := csv.NewWriter(os.Stdout)
	w.Write([]string{"date", "buy", "sell", "reference"})
	for _, r := range list {
		w.Write([]string{r.Date, string(r.Buy), string(r.Sell), string(r.Reference)})
	}
	w.Flush()
	return w.Error()
}

func runExport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "archivo de base de datos")
	dir := fs.String("dir", "site", "carpeta de salida")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	st, err := openStore(*db)
	if err != nil {
		return err
	}
	defer st.Close()
	stats, err := export.Write(ctx, st, *dir, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s: %d archivos, %d actualizados\n", *dir, stats.Files, stats.Written)
	return nil
}

func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	db := fs.String("db", defaultDB(), "archivo de base de datos")
	addr := fs.String("addr", envOr("COTIZA_ADDR", ":8080"), "dirección HTTP")
	every := fs.Duration("sync-every", envDuration("COTIZA_SYNC_EVERY", 0), "sincronizar cada (0 = nunca), ej: 6h")
	if _, err := parse(fs, args); err != nil {
		return err
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	st, err := store.Open(*db)
	if err != nil {
		return err
	}
	defer st.Close()

	if *every > 0 {
		go keepSynced(ctx, st, *every, log)
	}

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           api.New(st, log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdown)
	}()

	log.Info("listening", "addr", *addr, "db", *db, "sync_every", every.String())
	if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// keepSynced syncs right away, then every interval.
func keepSynced(ctx context.Context, st *store.Store, every time.Duration, log *slog.Logger) {
	run := func() {
		log.Info("sync started")
		results, err := syncer.Run(ctx, st, set.NewClient(), bcp.NewClient(), syncer.Options{})
		for _, r := range results {
			log.Info("sync finished", "source", r.Source, "rates", r.Rates, "changed", r.Changed,
				"warnings", len(r.Warnings), "duration", r.Duration.String())
		}
		if err != nil {
			log.Error("sync failed", "err", err)
		}
	}
	run()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return d
	}
	return fallback
}
