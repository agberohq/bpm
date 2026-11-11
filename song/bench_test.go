package song

import "testing"

func BenchmarkAnalyzePCM(b *testing.B) {
	// 30 seconds of a 120 BPM chord pattern at 44.1 kHz.
	pcm := chordHits(44100, 30, 120, 261.63)

	b.ReportAllocs()
	b.SetBytes(int64(len(pcm) * 4))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := AnalyzePCM(pcm, DefaultOptions()); err != nil {
			b.Fatal(err)
		}
	}
}
