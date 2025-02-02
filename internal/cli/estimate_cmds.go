package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"

	"github.com/guneshm03/planprice/internal/estimate"
	"github.com/guneshm03/planprice/internal/mapper"
	"github.com/guneshm03/planprice/internal/money"
	"github.com/guneshm03/planprice/internal/render"
	"github.com/guneshm03/planprice/internal/tfplan"
	"github.com/guneshm03/planprice/internal/usage"
)

func runBreakdown(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("breakdown", stderr)
	var c common
	c.register(fs)
	planPath := fs.String("plan", "", "plan JSON from `terraform show -json` (required)")
	view := fs.String("view", "after", "which side of the plan to price: after (planned) or before (prior state)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := requireFlag("plan", *planPath); err != nil {
		return err
	}
	format, err := render.ParseFormat(c.format)
	if err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}
	v := tfplan.After
	switch *view {
	case "after":
	case "before":
		v = tfplan.Before
	default:
		return fmt.Errorf("%w: --view must be after or before", errUsage)
	}
	log := c.logger(stderr)
	env, err := newEnv(&c, log)
	if err != nil {
		return err
	}
	plan, err := tfplan.ParseFile(*planPath)
	if err != nil {
		return err
	}
	est, err := env.estimate(*planPath, plan, v)
	if err != nil {
		return err
	}
	env.warnUnusedUsage(plan)
	w, closeFn, err := output(c.out, stdout)
	if err != nil {
		return err
	}
	if err := render.Breakdown(w, est, format); err != nil {
		_ = closeFn()
		return err
	}
	return closeFn()
}

func runDiff(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("diff", stderr)
	var c common
	c.register(fs)
	basePath := fs.String("base", "", "plan JSON for the base (e.g. main branch)")
	headPath := fs.String("head", "", "plan JSON for the head (e.g. pull request branch)")
	planPath := fs.String("plan", "", "single plan JSON: diff its prior state against its planned state")
	maxIncrease := fs.String("fail-above-increase", "", "exit 3 if the monthly increase exceeds this many USD")
	maxPercent := fs.String("fail-above-percent", "", "exit 3 if the monthly increase exceeds this percentage")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	single := *planPath != ""
	if single == (*basePath != "" || *headPath != "") {
		return fmt.Errorf("%w: give either --plan, or both --base and --head", errUsage)
	}
	if !single && (*basePath == "" || *headPath == "") {
		return fmt.Errorf("%w: --base and --head are both required", errUsage)
	}
	format, err := render.ParseFormat(c.format)
	if err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}
	thrUSD, thrPct, err := parseThresholds(*maxIncrease, *maxPercent)
	if err != nil {
		return err
	}
	log := c.logger(stderr)
	env, err := newEnv(&c, log)
	if err != nil {
		return err
	}
	var base, head *estimate.Estimate
	if single {
		plan, err := tfplan.ParseFile(*planPath)
		if err != nil {
			return err
		}
		if base, err = env.estimate(*planPath+" (before)", plan, tfplan.Before); err != nil {
			return err
		}
		if head, err = env.estimate(*planPath+" (after)", plan, tfplan.After); err != nil {
			return err
		}
		env.warnUnusedUsage(plan)
	} else {
		basePlan, err := tfplan.ParseFile(*basePath)
		if err != nil {
			return err
		}
		headPlan, err := tfplan.ParseFile(*headPath)
		if err != nil {
			return err
		}
		if base, err = env.estimate(*basePath, basePlan, tfplan.After); err != nil {
			return err
		}
		if head, err = env.estimate(*headPath, headPlan, tfplan.After); err != nil {
			return err
		}
		env.warnUnusedUsage(basePlan, headPlan)
	}
	d := estimate.Compare(base, head)
	w, closeFn, err := output(c.out, stdout)
	if err != nil {
		return err
	}
	if err := render.DiffReport(w, d, format); err != nil {
		_ = closeFn()
		return err
	}
	if err := closeFn(); err != nil {
		return err
	}
	if d.Exceeds(thrUSD, thrPct) {
		return fmt.Errorf("%w: monthly change %s", errThreshold, money.FormatSignedUSD(d.Delta, 2))
	}
	return nil
}

func parseThresholds(usd, pct string) (*money.Decimal, *float64, error) {
	var thrUSD *money.Decimal
	var thrPct *float64
	if usd != "" {
		d, err := money.Parse(usd)
		if err != nil || d.Sign() < 0 {
			return nil, nil, fmt.Errorf("%w: --fail-above-increase must be a non-negative number", errUsage)
		}
		thrUSD = &d
	}
	if pct != "" {
		f, err := strconv.ParseFloat(pct, 64)
		if err != nil || f < 0 {
			return nil, nil, fmt.Errorf("%w: --fail-above-percent must be a non-negative number", errUsage)
		}
		thrPct = &f
	}
	return thrUSD, thrPct, nil
}

// env bundles what every estimate needs.
type env struct {
	opts          estimate.Options
	defaultRegion string
	usage         *usage.File
	log           *slog.Logger
}

func newEnv(c *common, log *slog.Logger) (*env, error) {
	snap, err := loadSnapshot(c.snapshotPath)
	if err != nil {
		return nil, err
	}
	reg := mapper.NewRegistry()
	u, err := loadUsage(c.usagePath, reg, log)
	if err != nil {
		return nil, err
	}
	return &env{
		opts:          estimate.Options{Snapshot: snap, Registry: reg, Usage: u, Logger: log},
		defaultRegion: c.defaultRegion, usage: u, log: log,
	}, nil
}

func (e *env) estimate(label string, plan *tfplan.Plan, view tfplan.View) (*estimate.Estimate, error) {
	resources, err := plan.Resources(view, e.defaultRegion, e.log)
	if err != nil {
		return nil, err
	}
	return estimate.Run(label, resources, e.opts)
}

// warnUnusedUsage reports usage-file entries that match no resource in any
// of the plans, which usually means a typo or a renamed resource.
func (e *env) warnUnusedUsage(plans ...*tfplan.Plan) {
	if e.usage == nil {
		return
	}
	for _, entry := range e.usage.Entries() {
		matched := false
		for _, p := range plans {
			for _, rc := range p.ResourceChanges {
				if usage.MatchPattern(entry, rc.Address) {
					matched = true
					break
				}
			}
		}
		if !matched {
			e.log.Warn("usage entry matches no resource in the plan", "entry", entry)
		}
	}
}
