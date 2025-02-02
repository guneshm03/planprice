// Command pricegen refreshes planprice's offline AWS price snapshot.
//
//	pricegen fetch --region us-east-1 --region eu-west-1 --cache .cache/offers
//	pricegen build --region us-east-1 --region eu-west-1 --cache .cache/offers \
//	    --out internal/pricedata/aws-prices.json
//
// See docs/PRICE_SNAPSHOT.md for the full refresh procedure.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/guneshm03/planprice/internal/pricegen"
)

type regionList []string

func (r *regionList) String() string     { return strings.Join(*r, ",") }
func (r *regionList) Set(v string) error { *r = append(*r, v); return nil }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:], os.Stdout)
	stop()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "pricegen:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: pricegen fetch|build [flags]")
	}
	fs := flag.NewFlagSet("pricegen "+args[0], flag.ContinueOnError)
	var regions regionList
	fs.Var(&regions, "region", "region to include (repeatable)")
	cache := fs.String("cache", ".cache/offers", "directory for slim offer files")
	out := fs.String("out", "internal/pricedata/aws-prices.json", "snapshot output path (build)")
	generatedAt := fs.String("generated-at", time.Now().UTC().Format("2006-01-02"), "date recorded in the snapshot (build)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(regions) == 0 {
		regions = regionList{"us-east-1", "eu-west-1"}
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	switch args[0] {
	case "fetch":
		return fetch(ctx, log, regions, *cache)
	case "build":
		snap, err := pricegen.BuildFromDir(*cache, regions, *generatedAt)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(*out, func(w io.Writer) error { return pricegen.WriteSnapshot(w, snap) }); err != nil {
			return err
		}
		sum, err := pricegen.WriteChecksum(*out)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "wrote %s: %d prices, %d offers, sha256 %s\n", *out, len(snap.Prices), len(snap.Offers), sum)
		return err
	default:
		return fmt.Errorf("unknown subcommand %q (want fetch or build)", args[0])
	}
}

func fetch(ctx context.Context, log *slog.Logger, regions []string, cache string) error {
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return err
	}
	client := pricegen.NewHTTPClient()
	for _, region := range regions {
		for _, svc := range pricegen.Services {
			url := pricegen.OfferURL(svc, region)
			start := time.Now()
			log.Info("streaming offer", "service", svc, "region", region, "url", url)
			offer, err := pricegen.Fetch(ctx, client, svc, url)
			if err != nil {
				return err
			}
			path := filepath.Join(cache, pricegen.SlimFileName(svc, region))
			if err := writeFileAtomic(path, func(w io.Writer) error { return pricegen.WriteOffer(w, offer) }); err != nil {
				return err
			}
			log.Info("wrote slim offer", "path", path, "products", len(offer.Products), "version", offer.Version, "elapsed", time.Since(start).Round(time.Second))
		}
	}
	return nil
}

func writeFileAtomic(path string, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
