package termimage

import (
	"bytes"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-sixel"
	"golang.org/x/term"
)

// isSixel returns true if the terminal supports Sixel graphics.
// Detection order:
//  1. GLOWM_SIXEL=1 env var forces Sixel mode.
//  2. Known Sixel-capable terminal env vars (TERM_PROGRAM, TERM).
//  3. DA1 (Primary Device Attributes) terminal query for capability 4.
func isSixel() bool {
	if os.Getenv("GLOWM_SIXEL") == "1" {
		return true
	}
	if isKnownSixelTerminal() {
		return true
	}
	return querySixelViaDA1()
}

// isKnownSixelTerminal checks environment variables for terminals known to
// support Sixel that may not reliably advertise it via DA1.
func isKnownSixelTerminal() bool {
	switch os.Getenv("TERM_PROGRAM") {
	case "WezTerm":
		return true
	}
	switch os.Getenv("TERM") {
	case "mlterm", "yaft-256color", "foot", "foot-direct", "contour":
		return true
	}
	return false
}

// querySixelViaDA1 queries the terminal via DA1 (Primary Device Attributes)
// and returns true if the terminal reports Sixel capability (attribute 4).
func querySixelViaDA1() bool {
	stdoutTTY := term.IsTerminal(int(os.Stdout.Fd()))
	stdinTTY := term.IsTerminal(int(os.Stdin.Fd()))
	if os.Getenv("GLOWM_DEBUG_SIXEL") == "1" {
		fmt.Fprintf(os.Stderr, "glowm: stdout TTY=%v stdin TTY=%v\n", stdoutTTY, stdinTTY)
	}
	if !stdoutTTY || !stdinTTY {
		return false
	}

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return false
	}
	defer term.Restore(fd, oldState) //nolint:errcheck

	if _, err := os.Stdout.WriteString("\x1b[c"); err != nil {
		return false
	}

	ch := make(chan []byte, 1)
	go func() {
		var accumulated []byte
		buf := make([]byte, 64)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				accumulated = append(accumulated, buf[:n]...)
				// DA1 response is terminated by 'c'
				if strings.ContainsRune(string(accumulated), 'c') {
					ch <- accumulated
					return
				}
			}
			if err != nil {
				if len(accumulated) > 0 {
					ch <- accumulated
				} else {
					ch <- nil
				}
				return
			}
		}
	}()

	select {
	case resp := <-ch:
		if os.Getenv("GLOWM_DEBUG_SIXEL") == "1" {
			fmt.Fprintf(os.Stderr, "glowm: DA1 response (%d bytes): %q\n", len(resp), resp)
		}
		return parseSixelSupport(string(resp))
	case <-time.After(200 * time.Millisecond):
		if os.Getenv("GLOWM_DEBUG_SIXEL") == "1" {
			fmt.Fprintln(os.Stderr, "glowm: DA1 query timed out (no response within 200ms)")
		}
		return false
	}
}

// parseSixelSupport checks the DA1 response for Sixel capability (attribute 4).
// Expected format: ESC [ ? P1 ; P2 ; ... c
func parseSixelSupport(resp string) bool {
	start := strings.Index(resp, "\x1b[?")
	if start == -1 {
		return false
	}
	end := strings.Index(resp[start:], "c")
	if end == -1 {
		return false
	}
	params := resp[start+3 : start+end]
	for _, p := range strings.Split(params, ";") {
		if p == "4" {
			return true
		}
	}
	return false
}

// encodeSixel converts PNG data to a Sixel image escape sequence.
func encodeSixel(png []byte) string {
	img, _, err := image.Decode(bytes.NewReader(png))
	if err != nil {
		return ""
	}
	var buf bytes.Buffer
	enc := sixel.NewEncoder(&buf)
	if err := enc.Encode(img); err != nil {
		return ""
	}
	return buf.String()
}
