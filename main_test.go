package main

import (
	"slices"
	"testing"
)

func TestCommand(t *testing.T) {
	cases := []struct {
		args    []string
		cmd     string
		cmdArgs []string
	}{
		{nil, "serve", nil},
		{[]string{"serve"}, "serve", []string{}},
		{[]string{"once", "news", "count=2", "-dry-run"}, "once", []string{"news", "count=2", "-dry-run"}},
		// a leading flag never starts the director (#39)
		{[]string{"--help"}, "help", nil},
		{[]string{"-h"}, "help", nil},
		{[]string{"-dry-run"}, "-dry-run", []string{}},
	}
	for _, c := range cases {
		cmd, args := command(c.args)
		if cmd != c.cmd || !slices.Equal(args, c.cmdArgs) || (len(args) == 0) != (len(c.cmdArgs) == 0) {
			t.Errorf("command(%q) = %q %q, want %q %q", c.args, cmd, args, c.cmd, c.cmdArgs)
		}
	}
}
