package fideo

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestParseArgs pins the CLI grammar: options and paths in any order, both
// --flag=value and --flag value, and — the regression this guards — rejection
// of unknown options (which used to be tried as file names and surface as a
// baffling "stat --verbose: no such file"), with "--" to name dashed files.
func TestParseArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    cliOptions
		wantErr string // substring of the expected error; empty means success
	}{
		{
			"flags in any order",
			[]string{"a.mp4", "--hw", "--format=mkv", "--size", "sm", "b.mov"},
			cliOptions{format: "mkv", size: "sm", hw: true, paths: []string{"a.mp4", "b.mov"}}, "",
		},
		{"help", []string{"--help"}, cliOptions{help: true}, ""},
		{"short help", []string{"-h"}, cliOptions{help: true}, ""},
		{"unknown long option is rejected", []string{"--verbose", "a.mp4"}, cliOptions{}, `unknown option "--verbose"`},
		{"unknown short option is rejected", []string{"-x"}, cliOptions{}, `unknown option "-x"`},
		{"boolean flag with a value is rejected", []string{"--hw=true"}, cliOptions{}, "takes no value"},
		{
			"double dash passes dashed names through",
			[]string{"--hw", "--", "-clip.mp4", "--not-a-flag.mov"},
			cliOptions{hw: true, paths: []string{"-clip.mp4", "--not-a-flag.mov"}}, "",
		},
		{"missing separate value", []string{"--format"}, cliOptions{}, "--format requires a value"},
		{"empty inline value", []string{"--size="}, cliOptions{}, "--size requires a value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseArgs(%v) error = %v, want one containing %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseArgs(%v) unexpected error: %v", tt.args, err)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(cliOptions{})); diff != "" {
				t.Errorf("parseArgs(%v) mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}
