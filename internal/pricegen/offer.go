// Package pricegen builds planprice price snapshots from AWS Price List Bulk
// API offer files.
//
// Building is two stages so that each large download happens once and rule
// changes can be iterated offline:
//
//  1. Fetch streams a full offer file (the EC2 one is ~480 MB) through a
//     coarse product filter and writes a "slim" offer file in the same JSON
//     format, containing only candidate products and their on-demand terms.
//     The full file is never held in memory or written to disk.
//  2. Build applies precise per-service rules to slim offer files and emits a
//     pricing.Snapshot with normalised attributes.
package pricegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
)

// Product is one entry of an offer file's "products" map.
type Product struct {
	SKU           string            `json:"sku"`
	ProductFamily string            `json:"productFamily,omitempty"`
	Attributes    map[string]string `json:"attributes"`
}

// Term is one on-demand offer term for a SKU.
type Term struct {
	OfferTermCode   string                    `json:"offerTermCode"`
	SKU             string                    `json:"sku"`
	EffectiveDate   string                    `json:"effectiveDate"`
	PriceDimensions map[string]PriceDimension `json:"priceDimensions"`
}

// PriceDimension is one price band of a term.
type PriceDimension struct {
	RateCode     string            `json:"rateCode"`
	Description  string            `json:"description"`
	BeginRange   string            `json:"beginRange"`
	EndRange     string            `json:"endRange"`
	Unit         string            `json:"unit"`
	PricePerUnit map[string]string `json:"pricePerUnit"`
}

// Offer is a (possibly slimmed) offer file: metadata, products and their
// on-demand terms keyed by SKU then by term code.
type Offer struct {
	FormatVersion   string                     `json:"formatVersion"`
	OfferCode       string                     `json:"offerCode"`
	Version         string                     `json:"version"`
	PublicationDate string                     `json:"publicationDate"`
	Products        map[string]Product         `json:"products"`
	OnDemand        map[string]map[string]Term `json:"-"`
}

// KeepFunc decides whether a product is retained while streaming.
type KeepFunc func(Product) bool

// ReadOffer streams an offer file, keeping only products accepted by keep and
// the on-demand terms of those products. Reserved and other term types are
// skipped entry by entry, so memory use is bounded by the kept subset plus
// the largest single entry. The format requires "products" to precede
// "terms", which holds for every AWS offer file.
func ReadOffer(r io.Reader, keep KeepFunc) (*Offer, error) {
	dec := json.NewDecoder(r)
	o := &Offer{Products: map[string]Product{}, OnDemand: map[string]map[string]Term{}}
	if err := expectDelim(dec, '{'); err != nil {
		return nil, fmt.Errorf("offer file: %w", err)
	}
	sawProducts := false
	for dec.More() {
		key, err := readKey(dec)
		if err != nil {
			return nil, err
		}
		switch key {
		case "formatVersion":
			err = dec.Decode(&o.FormatVersion)
		case "offerCode":
			err = dec.Decode(&o.OfferCode)
		case "version":
			err = dec.Decode(&o.Version)
		case "publicationDate":
			err = dec.Decode(&o.PublicationDate)
		case "products":
			sawProducts = true
			err = eachEntry(dec, func(sku string) error {
				var p Product
				if err := dec.Decode(&p); err != nil {
					return fmt.Errorf("product %s: %w", sku, err)
				}
				if keep(p) {
					o.Products[sku] = p
				}
				return nil
			})
		case "terms":
			if !sawProducts {
				return nil, errors.New("offer file: \"terms\" before \"products\" is not supported")
			}
			err = eachEntry(dec, func(termType string) error {
				if termType != "OnDemand" {
					return eachEntry(dec, func(string) error { return skipValue(dec) })
				}
				return eachEntry(dec, func(sku string) error {
					if _, ok := o.Products[sku]; !ok {
						return skipValue(dec)
					}
					var terms map[string]Term
					if err := dec.Decode(&terms); err != nil {
						return fmt.Errorf("terms for %s: %w", sku, err)
					}
					o.OnDemand[sku] = terms
					return nil
				})
			})
		default:
			err = skipValue(dec)
		}
		if err != nil {
			return nil, fmt.Errorf("offer file key %q: %w", key, err)
		}
	}
	if o.OfferCode == "" {
		return nil, errors.New("offer file: missing offerCode")
	}
	return o, nil
}

// WriteOffer writes o in the AWS offer-file layout (products, then
// terms.OnDemand) with SKUs sorted, so slim files are byte-stable.
func WriteOffer(w io.Writer, o *Offer) error {
	type terms struct {
		OnDemand map[string]map[string]Term `json:"OnDemand"`
	}
	out := struct {
		FormatVersion   string             `json:"formatVersion"`
		OfferCode       string             `json:"offerCode"`
		Version         string             `json:"version"`
		PublicationDate string             `json:"publicationDate"`
		Products        map[string]Product `json:"products"`
		Terms           terms              `json:"terms"`
	}{o.FormatVersion, o.OfferCode, o.Version, o.PublicationDate, o.Products, terms{o.OnDemand}}
	// encoding/json sorts map keys, which gives the byte stability we want.
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	return enc.Encode(out)
}

// SKUs returns the kept product SKUs in sorted order.
func (o *Offer) SKUs() []string {
	return slices.Sorted(maps.Keys(o.Products))
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("expected %q, got %v", want, tok)
	}
	return nil
}

func readKey(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	key, ok := tok.(string)
	if !ok {
		return "", fmt.Errorf("expected object key, got %v", tok)
	}
	return key, nil
}

// eachEntry walks a JSON object, calling fn with each key; fn must consume
// the value.
func eachEntry(dec *json.Decoder, fn func(key string) error) error {
	if err := expectDelim(dec, '{'); err != nil {
		return err
	}
	for dec.More() {
		key, err := readKey(dec)
		if err != nil {
			return err
		}
		if err := fn(key); err != nil {
			return err
		}
	}
	return expectDelim(dec, '}')
}

// skipValue consumes one JSON value. Objects are skipped entry by entry so a
// huge object (such as Reserved terms) is never buffered whole.
func skipValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil // scalar
	}
	switch d {
	case '{':
		for dec.More() {
			if _, err := readKey(dec); err != nil {
				return err
			}
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return err
			}
		}
		return expectDelim(dec, '}')
	case '[':
		for dec.More() {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return err
			}
		}
		return expectDelim(dec, ']')
	default:
		return fmt.Errorf("unexpected delimiter %v", d)
	}
}
