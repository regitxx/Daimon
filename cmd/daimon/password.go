package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// stdinReader is a single shared buffered reader over os.Stdin. Process-wide
// because two consecutive readPassword() calls (e.g. password + confirmation
// in `daimon init`) need to read consecutive lines without losing buffered
// bytes — a fresh bufio.NewReader per call would over-read on the first call
// and the second call would never see the second line.
var stdinReader = bufio.NewReader(os.Stdin)

// readPassword reads a single line from the terminal with echo disabled. If
// stdin is not a TTY (piped input, CI, tests), the line is read with echo —
// this is the conventional behaviour for CLI tools and matches openssl, gpg,
// ssh, etc.: scripts that pipe passwords are explicitly opting out of the
// no-echo protection.
//
// Returns the password as a byte slice; the caller should zero it after use.
// The trailing newline is stripped.
func readPassword(prompt string) ([]byte, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		pw, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr) // ReadPassword swallows the newline
		if err != nil {
			return nil, fmt.Errorf("read password: %w", err)
		}
		return pw, nil
	}
	// Non-interactive stdin: read a line via the shared reader.
	line, err := stdinReader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read password: %w", err)
	}
	return []byte(strings.TrimRight(line, "\r\n")), nil
}
