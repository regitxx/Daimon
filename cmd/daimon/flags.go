package main

import (
	"fmt"
	"strings"
)

// kindsFlag captures repeated --kind flags. flag.Var calls Set once per
// occurrence, so successive --kind values append rather than overwrite.
//
// Shared between `daimon activity query` (multi-kind OR filter over the audit
// trail) and `daimon memory search --inject-preview` (multi-kind allowlist
// threaded into the SPEC §11 retrieval). Living here keeps the flag's
// validation contract — empty values rejected, comma joining for the default
// String render — in one place.
type kindsFlag []string

func (k *kindsFlag) String() string {
	if k == nil {
		return ""
	}
	return strings.Join(*k, ",")
}

func (k *kindsFlag) Set(v string) error {
	if v == "" {
		return fmt.Errorf("--kind cannot be empty")
	}
	*k = append(*k, v)
	return nil
}
