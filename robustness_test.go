package bpm

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	valid := New()
	if err := valid.Validate(); err != nil {
		t.Fatalf("default configuration invalid: %v", err)
	}

	cases := map[string]*Processor{
		"zero rate":        New(WithRate(0)),
		"negative rate":    New(WithRate(-44100)),
		"zero interval":    New(WithInterval(0)),
		"negative ivl":     New(WithInterval(-128)),
		"zero min bpm":     New(WithTempoRange(0, 146)),
		"zero max bpm":     New(WithTempoRange(84, 0)),
		"inverted range":   New(WithTempoRange(146, 84)),
		"equal range":      New(WithTempoRange(120, 120)),
		"zero steps":       New(WithScanSteps(0)),
		"zero samples":     New(WithScanSamples(0)),
		"unbounded scan":   New(WithScanSteps(1<<20), WithScanSamples(1<<20)),
		"interval too big": New(WithTempoRange(84, 100000)),
	}

	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			err := p.Validate()
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Validate() = %v, want ErrInvalidConfig", err)
			}
			if !strings.HasPrefix(err.Error(), "bpm: ") {
				t.Fatalf("error %q is not namespaced", err)
			}
		})
	}

	var nilProc *Processor
	if !errors.Is(nilProc.Validate(), ErrInvalidConfig) {
		t.Fatal("nil processor should be invalid")
	}
}

func TestNewChecked(t *testing.T) {
	if _, err := NewChecked(); err != nil {
		t.Fatalf("NewChecked() with defaults: %v", err)
	}
	if _, err := NewChecked(WithInterval(0)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewChecked(WithInterval(0)) = %v, want ErrInvalidConfig", err)
	}
}

// TestInvalidConfigDoesNotPanicOrHang covers the regressions found during the
// production review: integer divide by zero and an unbounded scan loop.
func TestInvalidConfigDoesNotPanicOrHang(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)

		if got := New(WithInterval(0)).Energy([]float32{1, 2, 3}); got != nil {
			t.Errorf("Energy with zero interval = %v, want nil", got)
		}
		if got := New(WithInterval(-4)).Energy([]float32{1, 2, 3}); got != nil {
			t.Errorf("Energy with negative interval = %v, want nil", got)
		}
		if _, err := New(WithInterval(0)).EnergyReader(bytes.NewReader(nil)); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("EnergyReader with zero interval = %v, want ErrInvalidConfig", err)
		}
		if got := New(WithRate(0)).DetectSamples(make([]float32, 100000)); !math.IsNaN(float64(got)) {
			t.Errorf("DetectSamples with zero rate = %v, want NaN", got)
		}
		if got := New(WithScanSteps(1<<20), WithScanSamples(1<<20)).Scan(make([]float32, 1000)); !math.IsNaN(float64(got)) {
			t.Errorf("Scan with absurd step budget = %v, want NaN", got)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("invalid configuration blocked the analysis, want fast failure")
	}
}

func TestEnergyIgnoresScanBudget(t *testing.T) {
	// Energy extraction must work even when the tempo scan settings are
	// unusual; only Estimate needs a sane scan budget.
	p := New(WithScanSteps(1<<20), WithScanSamples(1<<20))
	if err := p.Validate(); err == nil {
		t.Fatal("Validate accepted an absurd scan budget")
	}
	got := p.Energy(make([]float32, DefaultInterval*4))
	if len(got) != 4 {
		t.Fatalf("Energy returned %d samples, want 4", len(got))
	}
}

func TestScanRejectsSilenceAndShortInput(t *testing.T) {
	p := New()

	if got := p.Scan(nil); !math.IsNaN(float64(got)) {
		t.Errorf("Scan(nil) = %v, want NaN", got)
	}
	if got := p.Scan(make([]float32, 44100*10)); !math.IsNaN(float64(got)) {
		t.Errorf("Scan(silence) = %v, want NaN", got)
	}
	constant := make([]float32, 44100*10)
	for i := range constant {
		constant[i] = 0.5
	}
	if got := p.Scan(constant); !math.IsNaN(float64(got)) {
		t.Errorf("Scan(constant) = %v, want NaN", got)
	}
	if got := p.Scan(make([]float32, 20)); !math.IsNaN(float64(got)) {
		t.Errorf("Scan(too short) = %v, want NaN", got)
	}
	if got := p.DetectSamples(make([]float32, 44100)); !math.IsNaN(float64(got)) {
		t.Errorf("DetectSamples(1s) = %v, want NaN", got)
	}
}
