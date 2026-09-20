package app

import "testing"

// The host-descriptor probe parsers (pure seams).
func TestHostProbeParsers(t *testing.T) {
	got := osPrettyNameOf("NAME=\"Ubuntu\"\nPRETTY_NAME=\"Ubuntu 22.04.4 LTS\"\nVERSION_ID=\"22.04\"\n")
	if got != "Ubuntu 22.04.4 LTS" {
		t.Errorf("osPrettyNameOf = %q", got)
	}
	if got := osPrettyNameOf(""); got != "" {
		t.Errorf("missing os-release should be empty, got %q", got)
	}
	if got := memTotalGBOf("MemTotal:       263343048 kB\nSwapTotal:             0 kB\n"); got != 251 {
		t.Errorf("memTotalGBOf = %d, want 251", got)
	}
	if got := memTotalGBOf(""); got != 0 {
		t.Errorf("missing meminfo should be 0, got %d", got)
	}
	// On this Linux host the real probes find something (never an error).
	if env := gatherEnv(); env.Kernel == "" || env.Host == "" {
		t.Errorf("gatherEnv found nothing: %+v", env)
	}
}
