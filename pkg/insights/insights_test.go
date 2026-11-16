package insights

import (
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func TestAIAgentServiceTypeRoundTrip(t *testing.T) {
	serviceType, err := ToServiceType(plugnmeet.InsightsServiceType_INSIGHTS_SERVICE_TYPE_AI_AGENT)
	if err != nil {
		t.Fatalf("ToServiceType: %v", err)
	}
	if serviceType != ServiceTypeAIAgent {
		t.Fatalf("expected %q, got %q", ServiceTypeAIAgent, serviceType)
	}

	protoType, err := FromServiceType(ServiceTypeAIAgent)
	if err != nil {
		t.Fatalf("FromServiceType: %v", err)
	}
	if protoType != plugnmeet.InsightsServiceType_INSIGHTS_SERVICE_TYPE_AI_AGENT {
		t.Fatalf("expected the AI agent enum, got %s", protoType)
	}
}
