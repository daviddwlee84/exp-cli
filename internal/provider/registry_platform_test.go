package provider

import "testing"

func TestAIXBuiltInDirectCapabilitiesAreUnsupported(t *testing.T) {
	for _, capability := range []Capability{
		CapabilityRunnerPrepare,
		CapabilitySchedulerSubmit,
		CapabilitySchedulerObserve,
		CapabilitySchedulerCancel,
	} {
		if support := builtInCapabilitySupport("aix", ProviderDirect, capability); support != SupportUnsupported {
			t.Errorf("AIX direct %s support = %s", capability, support)
		}
		if support := builtInCapabilitySupport("linux", ProviderDirect, capability); support != SupportSupported {
			t.Errorf("Linux direct %s support = %s", capability, support)
		}
	}
}
