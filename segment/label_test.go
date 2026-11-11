package segment

import "testing"

func section(start, end float64, v []float64, rms float64) Section {
	return Section{Start: start, End: end, Vector: v, RMS: rms}
}

func TestHeuristicLabelsTypicalSong(t *testing.T) {
	intro := vec(11)
	verse := vec(22)
	chorus := vec(33)
	bridge := vec(44)
	outro := vec(55)

	sections := []Section{
		section(0, 10, intro, 0.2),
		section(10, 30, verse, 0.6),
		section(30, 50, chorus, 0.9),
		section(50, 70, verse, 0.6),
		section(70, 90, chorus, 0.9),
		section(90, 100, bridge, 0.8),
		section(100, 120, chorus, 0.9),
		section(120, 128, outro, 0.2),
	}

	got := HeuristicLabels(sections, HeuristicOptions{})
	want := []string{
		"Intro",
		"Verse 1",
		"Chorus 1",
		"Verse 2",
		"Chorus 2",
		"Bridge",
		"Chorus 3",
		"Outro",
	}

	if len(got) != len(want) {
		t.Fatalf("got %d labels, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("label[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

func TestHeuristicLabelsNoRepetition(t *testing.T) {
	sections := []Section{
		section(0, 20, vec(1), 0.5),
		section(20, 40, vec(2), 0.5),
		section(40, 60, vec(3), 0.5),
	}

	got := HeuristicLabels(sections, HeuristicOptions{})
	want := []string{"Section 1", "Section 2", "Section 3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("label[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

func TestHeuristicLabelsIntroOutro(t *testing.T) {
	sections := []Section{
		section(0, 6, vec(1), 0.15),   // short and quiet
		section(6, 40, vec(2), 0.7),   // verse
		section(40, 80, vec(2), 0.7),  // same part continues
		section(80, 90, vec(3), 0.15), // short and quiet close
	}
	got := HeuristicLabels(sections, HeuristicOptions{})
	if got[0] != "Intro" {
		t.Fatalf("label[0] = %q, want Intro (%v)", got[0], got)
	}
	if got[3] != "Outro" {
		t.Fatalf("label[3] = %q, want Outro (%v)", got[3], got)
	}
}

func TestHeuristicLabelsSingleAndEmpty(t *testing.T) {
	if got := HeuristicLabels(nil, HeuristicOptions{}); len(got) != 0 {
		t.Fatalf("labels for no sections = %v", got)
	}
	got := HeuristicLabels([]Section{section(0, 10, vec(1), 0.5)}, HeuristicOptions{})
	if len(got) != 1 || got[0] != "Section 1" {
		t.Fatalf("labels = %v", got)
	}
}

func TestHeuristicLabellerImplementsInterface(t *testing.T) {
	var l Labeller = HeuristicLabeller{}
	got := l.Labels([]Section{
		section(0, 10, vec(1), 0.5),
		section(10, 20, vec(2), 0.5),
	})
	if len(got) != 2 {
		t.Fatalf("got %d labels, want 2", len(got))
	}
}

func TestHeuristicOptionsDefaults(t *testing.T) {
	o := HeuristicOptions{}.withDefaults()
	d := DefaultHeuristicOptions()
	if o != d {
		t.Fatalf("withDefaults() = %+v, want %+v", o, d)
	}
}
