package song

import "github.com/agberohq/bpm/key"

// Tempo is the tempo analysis.
type Tempo struct {
	// BPM is meaningful only when Valid is true.
	BPM float64 `json:"bpm"`
	// Confidence is in [0, 1].
	Confidence float64 `json:"confidence"`
	// AtRangeEdge reports that the winner sat on the MinBPM/MaxBPM boundary.
	AtRangeEdge bool `json:"at_range_edge"`
	// Valid reports whether a tempo could be estimated.
	Valid bool `json:"valid"`
}

// KeySegment is the local key analysis of one structural section.
type KeySegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	// Label is a functional label such as "Chorus 1" when labelling is enabled.
	Label string `json:"label,omitempty"`
	// Energy is the section loudness relative to the loudest section, in [0, 1].
	Energy float64 `json:"energy"`
	// Domain is the best-scoring key of this section, or "" when none was
	// convincing.
	Domain string `json:"domain"`
	// Confidence is "strong", "moderate", "ambiguous" or "none".
	Confidence string `json:"confidence"`
	// Margin is the correlation gap between the best and second-best key.
	Margin float64 `json:"margin"`
	// Frames is the number of chroma frames behind this analysis.
	Frames int `json:"frames"`
	// Keys holds the plausible keys of this section, best first.
	Keys []key.Candidate `json:"keys"`
}

// Modulation is a change of key between two confident sections.
type Modulation struct {
	At         float64 `json:"at"`
	From       string  `json:"from"`
	To         string  `json:"to"`
	Confidence string  `json:"confidence"`
}

// KeyAnalysis is the tonal analysis of a song.
//
// Domain is the global key, computed over the whole song independently of the
// per-section analysis. Present keeps the other plausible global keys with
// their correlations. Segments holds the local analysis.
type KeyAnalysis struct {
	Domain      string          `json:"domain"`
	Confidence  string          `json:"confidence"`
	Margin      float64         `json:"margin"`
	Present     []key.Candidate `json:"present"`
	Segments    []KeySegment    `json:"segments"`
	Modulations []Modulation    `json:"modulations,omitempty"`
}

// Result is the complete analysis of one song.
type Result struct {
	Path     string      `json:"path,omitempty"`
	Duration float64     `json:"duration"`
	Tempo    Tempo       `json:"tempo"`
	Key      KeyAnalysis `json:"key"`
}
