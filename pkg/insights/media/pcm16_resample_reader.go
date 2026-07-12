package media

import (
	"encoding/binary"
	"io"
	"math"
)

type pcm16ResampleReadCloser struct {
	src        io.ReadCloser
	inputRate  int
	outputRate int

	pendingByte []byte
	outBytes    []byte
	eof         bool
}

// NewPCM16ResampleReadCloser wraps a raw PCM16 little-endian mono stream and
// resamples it from inputRate to outputRate.
//
// It returns the original reader unchanged when:
//   - src is nil
//   - inputRate <= 0
//   - outputRate <= 0
//   - inputRate == outputRate
//
// This is useful when a provider returns PCM at a different sample rate than
// the LiveKit publisher expects.
func NewPCM16ResampleReadCloser(src io.ReadCloser, inputRate, outputRate int) io.ReadCloser {
	if src == nil {
		return nil
	}

	if inputRate <= 0 || outputRate <= 0 || inputRate == outputRate {
		return src
	}

	return &pcm16ResampleReadCloser{
		src:        src,
		inputRate:  inputRate,
		outputRate: outputRate,
	}
}

func (r *pcm16ResampleReadCloser) Read(p []byte) (int, error) {
	for len(r.outBytes) == 0 && !r.eof {
		buf := make([]byte, 32*1024)

		n, err := r.src.Read(buf)
		if n > 0 {
			inputBytes := append(r.pendingByte, buf[:n]...)

			// Keep odd trailing byte for the next read because PCM16 samples
			// are 2 bytes each.
			if len(inputBytes)%2 != 0 {
				r.pendingByte = append(r.pendingByte[:0], inputBytes[len(inputBytes)-1])
				inputBytes = inputBytes[:len(inputBytes)-1]
			} else {
				r.pendingByte = r.pendingByte[:0]
			}

			if len(inputBytes) > 0 {
				inputSamples := PCM16BytesToSamples(inputBytes)
				outputSamples := ResamplePCM16Linear(inputSamples, r.inputRate, r.outputRate)
				r.outBytes = PCM16SamplesToBytes(outputSamples)
			}
		}

		if err == io.EOF {
			r.eof = true
			break
		}

		if err != nil {
			return 0, err
		}
	}

	if len(r.outBytes) == 0 && r.eof {
		return 0, io.EOF
	}

	n := copy(p, r.outBytes)
	r.outBytes = r.outBytes[n:]

	return n, nil
}

func (r *pcm16ResampleReadCloser) Close() error {
	return r.src.Close()
}

// PCM16BytesToSamples converts little-endian PCM16 bytes to int16 samples.
func PCM16BytesToSamples(data []byte) []int16 {
	samples := make([]int16, len(data)/2)

	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}

	return samples
}

// PCM16SamplesToBytes converts int16 samples to little-endian PCM16 bytes.
func PCM16SamplesToBytes(samples []int16) []byte {
	data := make([]byte, len(samples)*2)

	for i, sample := range samples {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(sample))
	}

	return data
}

// ResamplePCM16Linear converts mono PCM16 samples from inputRate to outputRate.
// It uses simple linear interpolation, which is dependency-free and suitable
// for lightweight realtime speech/TTS pipelines.
func ResamplePCM16Linear(input []int16, inputRate, outputRate int) []int16 {
	if len(input) == 0 || inputRate <= 0 || outputRate <= 0 || inputRate == outputRate {
		return input
	}

	outputLen := int(math.Round(float64(len(input)) * float64(outputRate) / float64(inputRate)))
	if outputLen <= 0 {
		return nil
	}

	if len(input) == 1 {
		out := make([]int16, outputLen)
		for i := range out {
			out[i] = input[0]
		}
		return out
	}

	out := make([]int16, outputLen)
	ratio := float64(inputRate) / float64(outputRate)

	for i := 0; i < outputLen; i++ {
		srcPos := float64(i) * ratio
		srcIdx := int(math.Floor(srcPos))
		frac := srcPos - float64(srcIdx)

		if srcIdx >= len(input)-1 {
			out[i] = input[len(input)-1]
			continue
		}

		a := float64(input[srcIdx])
		b := float64(input[srcIdx+1])
		value := a + (b-a)*frac

		if value > math.MaxInt16 {
			value = math.MaxInt16
		} else if value < math.MinInt16 {
			value = math.MinInt16
		}

		out[i] = int16(math.Round(value))
	}

	return out
}
