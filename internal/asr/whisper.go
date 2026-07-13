//go:build whisper

// This file is the real transcriber: a thin cgo wrapper over the vendored
// whisper.cpp (third_party/whisper.cpp, a pinned submodule). It builds ONLY
// under `-tags whisper` and only after `make whisper` has produced the static
// libraries. The default build uses stub.go instead so the rest of the module
// compiles without a C toolchain (§2.3, §13).
//
// We deliberately do NOT import the upstream Go module path (org-move churn,
// §2.3 / §16.2); this wrapper isolates the C API so upstream changes are
// absorbed in one place.
package asr

/*
#cgo CXXFLAGS: -std=c++17
#cgo CFLAGS: -I${SRCDIR}/../../third_party/whisper.cpp/include -I${SRCDIR}/../../third_party/whisper.cpp/ggml/include
// whisper.cpp v1.9 splits ggml into per-backend static libs; each backend lives
// in its own build subdirectory and registers itself with the core ggml lib.
#cgo LDFLAGS: -L${SRCDIR}/../../third_party/whisper.cpp/build/src
#cgo LDFLAGS: -L${SRCDIR}/../../third_party/whisper.cpp/build/ggml/src
#cgo LDFLAGS: -lwhisper -lggml -lggml-cpu -lm -lstdc++
// darwin: BLAS (Accelerate) and Metal backends plus their frameworks.
#cgo darwin LDFLAGS: -L${SRCDIR}/../../third_party/whisper.cpp/build/ggml/src/ggml-blas
#cgo darwin LDFLAGS: -L${SRCDIR}/../../third_party/whisper.cpp/build/ggml/src/ggml-metal
#cgo darwin LDFLAGS: -lggml-blas -lggml-metal -lggml-base
#cgo darwin LDFLAGS: -framework Accelerate -framework Metal -framework MetalKit -framework Foundation -framework CoreGraphics -framework QuartzCore
// linux/windows: CPU-only static build.
#cgo linux LDFLAGS: -lggml-base
#cgo windows LDFLAGS: -lggml-base
#include <stdlib.h>
#include <math.h>
#include "ggml.h"
#include "whisper.h"

// whisper.cpp and ggml log verbosely to stderr through ggml's default log
// callback (whisper_model_load:…, ggml_metal_init:…). That corrupts the TUI's
// alt screen and the headless JSON stream, so we install a no-op callback.
static void voxthief_log_noop(enum ggml_log_level level, const char * text, void * user_data) {
    (void)level; (void)text; (void)user_data;
}
static void voxthief_silence_logs(void) {
    whisper_log_set(voxthief_log_noop, NULL);
    ggml_log_set(voxthief_log_noop, NULL);
}
*/
import "C"

import (
	"fmt"
	"math"
	"unsafe"
)

// Available reports that this binary was built with whisper support.
const Available = true

type whisperTranscriber struct {
	ctx    *C.struct_whisper_context
	params Params
	// cLang / cPrompt / cVADPath are kept alive for the lifetime of the
	// transcriber because whisper stores the pointers.
	cLang    *C.char
	cPrompt  *C.char
	cVADPath *C.char
}

// New loads the whisper model (and VAD model) and returns a ready transcriber.
func New(p Params) (Transcriber, error) {
	C.voxthief_silence_logs() // keep whisper/ggml chatter off the TUI and stdout

	cpath := C.CString(p.ModelPath)
	defer C.free(unsafe.Pointer(cpath))

	cparams := C.whisper_context_default_params()
	ctx := C.whisper_init_from_file_with_params(cpath, cparams)
	if ctx == nil {
		return nil, fmt.Errorf("whisper: failed to load model %q", p.ModelPath)
	}

	t := &whisperTranscriber{ctx: ctx, params: p}
	if p.Language != "" {
		t.cLang = C.CString(p.Language)
	}
	if p.InitialPrompt != "" {
		t.cPrompt = C.CString(p.InitialPrompt)
	}
	if p.VAD && p.VADModelPath != "" {
		t.cVADPath = C.CString(p.VADModelPath)
	}
	return t, nil
}

func (t *whisperTranscriber) Transcribe(samples []float32) (Result, error) {
	if len(samples) == 0 {
		return Result{Language: t.params.Language}, nil
	}

	// Beam search per §6.
	fp := C.whisper_full_default_params(C.WHISPER_SAMPLING_BEAM_SEARCH)
	fp.n_threads = C.int(t.params.Threads)
	fp.beam_search.beam_size = C.int(t.params.BeamSize)
	fp.no_context = C.bool(t.params.NoContext) // §6: disjoint radio traffic
	fp.token_timestamps = C.bool(false)        // §6
	fp.print_progress = C.bool(false)
	fp.print_realtime = C.bool(false)
	fp.single_segment = C.bool(false)
	if t.cLang != nil {
		fp.language = t.cLang
	}
	if t.cPrompt != nil {
		fp.initial_prompt = t.cPrompt
	}

	// whisper.cpp integrated Silero VAD (§6). When enabled, whisper maps the
	// returned segment timestamps back onto the ORIGINAL untrimmed timeline —
	// the correctness property asserted by the integration test.
	if t.cVADPath != nil {
		fp.vad = C.bool(true)
		fp.vad_model_path = t.cVADPath
		fp.vad_params.threshold = C.float(0.5)
		fp.vad_params.min_speech_duration_ms = C.int(250)
		fp.vad_params.min_silence_duration_ms = C.int(100)
		fp.vad_params.speech_pad_ms = C.int(30)
	}

	if rc := C.whisper_full(t.ctx, fp, (*C.float)(unsafe.Pointer(&samples[0])), C.int(len(samples))); rc != 0 {
		return Result{}, fmt.Errorf("whisper_full failed: rc=%d", int(rc))
	}

	n := int(C.whisper_full_n_segments(t.ctx))
	res := Result{Language: t.params.Language, Segments: make([]Segment, 0, n)}
	for i := 0; i < n; i++ {
		ci := C.int(i)
		text := C.GoString(C.whisper_full_get_segment_text(t.ctx, ci))
		// t0/t1 are in centiseconds relative to the (untrimmed) input.
		t0 := float64(C.whisper_full_get_segment_t0(t.ctx, ci)) / 100.0
		t1 := float64(C.whisper_full_get_segment_t1(t.ctx, ci)) / 100.0
		res.Segments = append(res.Segments, Segment{
			Start:        t0,
			End:          t1,
			Text:         text,
			AvgLogprob:   t.segmentAvgLogprob(ci),
			NoSpeechProb: float64(C.whisper_full_get_segment_no_speech_prob(t.ctx, ci)),
		})
	}
	return res, nil
}

// segmentAvgLogprob returns the mean natural-log token probability over the
// segment (§6: mean ln(p) over segment tokens).
func (t *whisperTranscriber) segmentAvgLogprob(seg C.int) float64 {
	nt := int(C.whisper_full_n_tokens(t.ctx, seg))
	if nt == 0 {
		return 0
	}
	var sum float64
	var count int
	for j := 0; j < nt; j++ {
		p := float64(C.whisper_full_get_token_p(t.ctx, seg, C.int(j)))
		if p <= 0 {
			continue
		}
		sum += math.Log(p)
		count++
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

func (t *whisperTranscriber) Close() error {
	if t.ctx != nil {
		C.whisper_free(t.ctx)
		t.ctx = nil
	}
	if t.cLang != nil {
		C.free(unsafe.Pointer(t.cLang))
	}
	if t.cPrompt != nil {
		C.free(unsafe.Pointer(t.cPrompt))
	}
	if t.cVADPath != nil {
		C.free(unsafe.Pointer(t.cVADPath))
	}
	return nil
}
