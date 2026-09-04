package ai

import "testing"

func TestAuditModelParametersUsesLatestStatePerAttempt(t *testing.T) {
	records := []ModelAttemptRecord{
		{ID: "attempt-1", ConfigID: 1, Attempt: 1, MaxAttempts: 2, Status: "waiting_response"},
		{ID: "attempt-1", ConfigID: 1, Attempt: 1, MaxAttempts: 2, Status: "failed"},
		{ID: "attempt-2", ConfigID: 2, APIProtocol: "responses", MaxTokens: 4096, Temperature: 0.2, RequestTimeoutSeconds: 30, InactivityTimeoutSeconds: 15, Attempt: 2, MaxAttempts: 3, FallbackIndex: 2, FallbackCount: 2, ForcedConfig: true, PreviousResponseIDPresent: true},
	}

	parameters := AuditModelParameters(records)
	if parameters["providerAttemptCount"] != 2 {
		t.Fatalf("providerAttemptCount=%v", parameters["providerAttemptCount"])
	}
	if parameters["configId"] != uint(2) || parameters["apiProtocol"] != "responses" || parameters["attempt"] != 2 {
		t.Fatalf("parameters=%+v", parameters)
	}
	if parameters["forcedConfig"] != true || parameters["previousResponseIdPresent"] != true {
		t.Fatalf("parameters=%+v", parameters)
	}
}

func TestAuditModelParametersHandlesNoAttempts(t *testing.T) {
	parameters := AuditModelParameters(nil)
	if len(parameters) != 1 || parameters["providerAttemptCount"] != 0 {
		t.Fatalf("parameters=%+v", parameters)
	}
}
