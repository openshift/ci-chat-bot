package main

import (
	"testing"

	"github.com/sbstjn/hanu"
)

func TestHanu(t *testing.T) {
	t.Skip("I am broken")
	cmd := hanu.NewCommand(`input <test> ([^\s]+)`, "", func(hanu.ConversationInterface) {})
	re := cmd.Get().Expression()
	t.Logf("re: %s", re)
	m, err := cmd.Get().Match("input blah blah")
	if err != nil {
		t.Fatal(err)
	}
	match, err := m.Match(2)
	t.Logf("m: %#v %v", match, err)
}

// test upgrade 4.0.1 4.0.0-0.ci-blah-blah
// test e2e,aws <image>
// test gcp <image>
