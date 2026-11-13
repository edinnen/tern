package tern_test

import (
	"fmt"

	"github.com/edinnen/tern"
)

func Example() {
	// Turn a payload into audio: 8000 samples a second, mono, peak 1.
	audio, err := tern.Modulate(tern.T31, 1500, "N0CALL", []byte("hello, world"), false)
	if err != nil {
		panic(err)
	}

	// A receiver takes audio as it arrives and returns what it decodes. It needs a
	// little audio after the end of a transmission before it reports it.
	rx, _ := tern.NewReceiver(tern.Config{})
	frames := rx.Feed(audio)
	frames = append(frames, rx.Feed(make([]float32, tern.SampleRate))...)
	for _, f := range frames {
		fmt.Printf("%s at %s: %s\n", f.Call, f.Profile.Name, f.Data)
	}
	// Output: N0CALL at TERN31: hello, world
}
