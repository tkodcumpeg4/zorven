package agent

import "testing"

func TestSecurity_SSRF_BlockedTargetHosts(t *testing.T) {
	blocked := []string{
		"169.254.169.254", // AWS/GCP/Azure IMDSv1/v2
		"169.254.1.1",     // Link-local
		"instance-data",
		"metadata.google.internal",
		"metadata",
		"0.0.0.0",
	}

	for _, host := range blocked {
		if !isBlockedTargetHost(host) {
			t.Errorf("SSRF VULNERABILITY: host %q should be blocked but was permitted!", host)
		}
	}

	permitted := []string{
		"localhost",
		"127.0.0.1",
		"::1",
		"192.168.1.50",
		"10.0.0.5",
		"my-service.local",
	}

	for _, host := range permitted {
		if isBlockedTargetHost(host) {
			t.Errorf("legitimate local target %q was incorrectly blocked!", host)
		}
	}
}

func TestSecurity_RestrictedPorts(t *testing.T) {
	dangerous := []int{22, 23, 25, 135, 445, 3389, 5432, 3306, 6379, 27017}
	for _, p := range dangerous {
		if !isRestrictedPort(p) {
			t.Errorf("PORT SCAN VULNERABILITY: port %d should be restricted but was allowed", p)
		}
	}

	safe := []int{80, 443, 3000, 8000, 8080, 8443, 9000}
	for _, p := range safe {
		if isRestrictedPort(p) {
			t.Errorf("port %d should be permitted for web proxying", p)
		}
	}
}
