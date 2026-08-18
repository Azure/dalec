package distro

import (
	"strings"
	"testing"
)

func TestRPMMinimizeScriptUsesDependencyTypeFormatter(t *testing.T) {
	if !strings.Contains(rpmMinimizeScript, "%{REQUIREFLAGS:deptype}") {
		t.Fatal("RPM minimization must use the deptype formatter for scriptlet requirements")
	}
	if strings.Contains(rpmMinimizeScript, "%{REQUIREFLAGS:depflags}") {
		t.Fatal("RPM minimization must not use the depflags formatter for scriptlet requirements")
	}
}
