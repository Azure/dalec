package distro

import "testing"

// TestZypperInstallTemplateRenders ensures the inline install-script template
// parses and executes for both includeDocs values (template.Must executes at
// call time, so a broken template would panic here rather than at build).
func TestZypperInstallTemplateRenders(t *testing.T) {
	for _, inc := range []bool{true, false} {
		cfg := &dnfInstallConfig{includeDocs: inc}
		// Should not panic; returns a valid RunOption.
		if got := ZypperInstall(cfg, "", []string{"pkg"}); got == nil {
			t.Fatalf("nil RunOption for includeDocs=%v", inc)
		}
	}
}
