package windows

import (
	"fmt"

	"github.com/project-dalec/dalec"
)

func validateRuntimeDeps(s *dalec.Spec, targetKey string) error {
	rd := s.GetPackageDeps(targetKey).GetRuntime()
	if len(rd) != 0 {
		return fmt.Errorf("targets with windows output images cannot have runtime dependencies")
	}

	return nil
}
