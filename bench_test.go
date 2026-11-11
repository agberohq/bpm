package bpm

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func encodeBench(samples []float32) []byte {
	buf := make([]byte, len(samples)*4)
	for i, v := range samples {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

func BenchmarkEstimate(b *testing.B) {
	p := New()
	energy := p.Energy(clickTrack(44100, 120, 60))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if est := p.Estimate(energy); !est.Valid {
			b.Fatal("no estimate")
		}
	}
}

func BenchmarkEnergyReader(b *testing.B) {
	data := encodeBench(clickTrack(44100, 120, 10))

	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := New().EnergyReader(bytes.NewReader(data)); err != nil {
			b.Fatal(err)
		}
	}
}
