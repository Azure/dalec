package distro

import "testing"

// TestZypperInstallTemplateRenders ensures the inline install-script template
// parses and executes across the combinations of includeDocs and the
// container-assembly root path (template.Must executes at call time, so a
// broken template would panic here rather than at build).
func TestZypperInstallTemplateRenders(t *testing.T) {
	for _, inc := range []bool{true, false} {
		for _, root := range []string{"", "/tmp/rootfs"} {
			cfg := &dnfInstallConfig{includeDocs: inc, root: root}
			// Should not panic; returns a valid RunOption.
			if got := ZypperInstall(cfg, "", []string{"pkg"}); got == nil {
				t.Fatalf("nil RunOption for includeDocs=%v root=%q", inc, root)
			}
		}
	}
}
