package codeexec

// NewServiceFromSettings selects a Runner from configuration and wraps it in a
// Service with the given platform default/maximum limits. It returns the
// no-op stub runner unless the feature is enabled AND a real runner is named,
// so a misconfiguration degrades to the harmless stub rather than executing
// untrusted code with no sandbox.
//
// Only the stub is built in: a real sandbox runner (container/gVisor/nsjail
// backed) is a deployment concern and registers itself here by name. Until
// one is wired, selecting any runner name still yields the stub, and the
// feature stays safely dark even if an operator flips LMS_CODE_EXEC_ENABLED.
func NewServiceFromSettings(enabled bool, runner string, defaults Limits) *Service {
	// No real runner is compiled in yet, so every configuration resolves to
	// the stub. A real sandbox runner slots in here, e.g.:
	//
	//   if enabled && runner == "gvisor" {
	//       return NewService(NewGvisorRunner(...), defaults)
	//   }
	//
	// keeping the stub as the safe default fall-through.
	return NewService(NewStubRunner(), defaults)
}
