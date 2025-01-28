package windows

import (
	"fmt"
	"strings"

	"github.com/Azure/dalec"
)

func validateRuntimeDeps(s *dalec.Spec, targetKey string) error {
	rd := s.GetRuntimeDeps(targetKey)
	if len(rd) != 0 {
		return fmt.Errorf("targets with windows output images cannot have runtime dependencies: %q", strings.Join(rd, " "))
	}

	return nil
}
