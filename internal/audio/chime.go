package audio

import (
	"encoding/binary"
	"math"
	"os"
)

const (
	sampleRate    = 44100
	bitsPerSample = 16
	numChannels   = 1
	duration      = 0.2 // seconds
	amplitude     = 0.3
	fadeMs        = 5 // fade envelope in milliseconds
	freq1         = 800.0
	freq2         = 1200.0
)

// GenerateChime creates a short ascending two-tone WAV file in a temp directory.
// The caller owns the returned file and is responsible for cleanup.
func GenerateChime() (string, error) {
	numSamples := int(sampleRate * duration)
	fadeDuration := float64(fadeMs) / 1000.0
	fadeSamples := int(float64(sampleRate) * fadeDuration)
	half := numSamples / 2

	dataSize := numSamples * numChannels * (bitsPerSample / 8)
	fileSize := 44 + dataSize

	buf := make([]byte, fileSize)

	// RIFF header
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(fileSize-8))
	copy(buf[8:12], "WAVE")

	// fmt subchunk
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16) // subchunk size
	binary.LittleEndian.PutUint16(buf[20:22], 1)  // PCM
	binary.LittleEndian.PutUint16(buf[22:24], numChannels)
	binary.LittleEndian.PutUint32(buf[24:28], sampleRate)
	binary.LittleEndian.PutUint32(buf[28:32], sampleRate*numChannels*(bitsPerSample/8)) // byte rate
	binary.LittleEndian.PutUint16(buf[32:34], numChannels*(bitsPerSample/8))            // block align
	binary.LittleEndian.PutUint16(buf[34:36], bitsPerSample)

	// data subchunk
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))

	// Generate samples
	for i := range numSamples {
		t := float64(i) / sampleRate
		freq := freq1
		if i >= half {
			freq = freq2
		}
		sample := amplitude * math.Sin(2*math.Pi*freq*t)

		// Fade envelope
		env := 1.0
		if i < fadeSamples {
			env = float64(i) / float64(fadeSamples)
		} else if i >= numSamples-fadeSamples {
			env = float64(numSamples-1-i) / float64(fadeSamples)
		}
		sample *= env

		// Convert to 16-bit PCM
		val := int16(sample * 32767)
		offset := 44 + i*2
		binary.LittleEndian.PutUint16(buf[offset:offset+2], uint16(val))
	}

	f, err := os.CreateTemp("", "refrain-chime-*.wav")
	if err != nil {
		return "", err
	}
	path := f.Name()

	if _, err := f.Write(buf); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}

	return path, nil
}
