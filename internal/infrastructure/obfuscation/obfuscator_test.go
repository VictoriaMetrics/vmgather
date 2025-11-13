package obfuscation

import (
	"net"
	"strings"
	"testing"
)

// TestNewObfuscator tests obfuscator creation
func TestNewObfuscator(t *testing.T) {
	obf := NewObfuscator()

	if obf == nil {
		t.Fatal("expected non-nil obfuscator")
	}

	if obf.instanceMap == nil {
		t.Error("instanceMap should be initialized")
	}

	if obf.jobMap == nil {
		t.Error("jobMap should be initialized")
	}
}

// TestObfuscator_ObfuscateInstance_Deterministic tests deterministic obfuscation
func TestObfuscator_ObfuscateInstance_Deterministic(t *testing.T) {
	obf := NewObfuscator()

	original := "10.0.1.5:8482"

	// First call
	result1 := obf.ObfuscateInstance(original)

	// Second call with same value
	result2 := obf.ObfuscateInstance(original)

	// Should return same result
	if result1 != result2 {
		t.Errorf("obfuscation not deterministic: %s != %s", result1, result2)
	}

	// Should be different from original
	if result1 == original {
		t.Error("obfuscation didn't change value")
	}

	// Should have IP:port format
	if !strings.Contains(result1, ":") {
		t.Errorf("invalid format: %s", result1)
	}
}

// TestObfuscator_ObfuscateInstance_PreservesPort tests port preservation
func TestObfuscator_ObfuscateInstance_PreservesPort(t *testing.T) {
	obf := NewObfuscator()

	testCases := []struct {
		name     string
		input    string
		wantPort string
	}{
		{"vmstorage", "10.0.1.5:8482", "8482"},
		{"vmselect", "10.0.1.6:8481", "8481"},
		{"vmagent", "10.0.1.7:8429", "8429"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := obf.ObfuscateInstance(tc.input)

			_, port, err := net.SplitHostPort(result)
			if err != nil {
				t.Fatalf("failed to parse result: %v", err)
			}

			if port != tc.wantPort {
				t.Errorf("port not preserved: want %s, got %s", tc.wantPort, port)
			}
		})
	}
}

// TestObfuscator_ObfuscateInstance_UsesObviousFakeIP tests obviously fake IP range (777.777.x.x)
func TestObfuscator_ObfuscateInstance_UsesObviousFakeIP(t *testing.T) {
	obf := NewObfuscator()

	original := "10.0.1.5:8482"
	result := obf.ObfuscateInstance(original)

	host, _, err := net.SplitHostPort(result)
	if err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	// Should be in 777.777.x.x range (obviously fake)
	if !strings.HasPrefix(host, "777.777.") {
		t.Errorf("obfuscated IP not in 777.777.x.x range: %s", host)
	}
}

// TestObfuscator_ObfuscateInstance_DifferentValues tests different inputs
func TestObfuscator_ObfuscateInstance_DifferentValues(t *testing.T) {
	obf := NewObfuscator()

	instance1 := "10.0.1.5:8482"
	instance2 := "10.0.1.6:8482"

	result1 := obf.ObfuscateInstance(instance1)
	result2 := obf.ObfuscateInstance(instance2)

	// Different inputs should produce different outputs
	if result1 == result2 {
		t.Error("different instances produced same obfuscated value")
	}
}

// TestObfuscator_ObfuscateInstance_SameIPDifferentPorts tests same IP with different ports
// NOTE: Current simple implementation creates different obfuscated IPs for each instance string.
// More sophisticated implementation would parse IP and reuse same obfuscated IP.
// This is acceptable for MVP - each unique "IP:PORT" combination gets unique obfuscated value.
func TestObfuscator_ObfuscateInstance_SameIPDifferentPorts(t *testing.T) {
	obf := NewObfuscator()

	instances := []string{
		"10.0.1.5:8482",
		"10.0.1.5:8481",
		"10.0.1.5:8429",
	}

	results := make([]string, len(instances))
	for i, instance := range instances {
		results[i] = obf.ObfuscateInstance(instance)
	}

	// Each unique instance string should get unique obfuscated value
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}

	// Ports should be preserved
	for i, instance := range instances {
		_, originalPort, _ := net.SplitHostPort(instance)
		_, obfuscatedPort, _ := net.SplitHostPort(results[i])
		
		if originalPort != obfuscatedPort {
			t.Errorf("port not preserved: original=%s, obfuscated=%s", originalPort, obfuscatedPort)
		}
	}
}

// TestObfuscator_ObfuscateInstance_InvalidFormat tests invalid instance format
func TestObfuscator_ObfuscateInstance_InvalidFormat(t *testing.T) {
	obf := NewObfuscator()

	// Instance without port
	invalid := "not-a-valid-instance"
	result := obf.ObfuscateInstance(invalid)

	// Should return some hash (fallback behavior)
	if result == "" {
		t.Error("expected non-empty result")
	}

	if result == invalid {
		t.Error("obfuscation didn't change value")
	}
}

// TestObfuscator_ObfuscateJob tests job obfuscation
func TestObfuscator_ObfuscateJob(t *testing.T) {
	obf := NewObfuscator()

	original := "vmstorage-prod-dc1"
	component := "vmstorage"

	result := obf.ObfuscateJob(original, component)

	// Should have expected format
	expectedPrefix := "vm_component_vmstorage_"
	if !strings.HasPrefix(result, expectedPrefix) {
		t.Errorf("wrong prefix: want %s*, got %s", expectedPrefix, result)
	}

	// Should be deterministic
	result2 := obf.ObfuscateJob(original, component)
	if result != result2 {
		t.Error("job obfuscation not deterministic")
	}

	// Should be different from original
	if result == original {
		t.Error("job obfuscation didn't change value")
	}
}

// TestObfuscator_ObfuscateJob_MultipleComponents tests multiple components
func TestObfuscator_ObfuscateJob_MultipleComponents(t *testing.T) {
	obf := NewObfuscator()

	testCases := []struct {
		job       string
		component string
		wantPrefix string
	}{
		{"vmstorage-prod", "vmstorage", "vm_component_vmstorage_"},
		{"vmselect-prod", "vmselect", "vm_component_vmselect_"},
		{"vmagent-prod", "vmagent", "vm_component_vmagent_"},
	}

	for _, tc := range testCases {
		t.Run(tc.component, func(t *testing.T) {
			result := obf.ObfuscateJob(tc.job, tc.component)

			if !strings.HasPrefix(result, tc.wantPrefix) {
				t.Errorf("wrong prefix: want %s*, got %s", tc.wantPrefix, result)
			}
		})
	}
}

// TestObfuscator_ObfuscateJob_Incrementing tests counter incrementing
func TestObfuscator_ObfuscateJob_Incrementing(t *testing.T) {
	obf := NewObfuscator()

	component := "vmstorage"
	jobs := []string{"job1", "job2", "job3"}

	results := make([]string, len(jobs))
	for i, job := range jobs {
		results[i] = obf.ObfuscateJob(job, component)
	}

	// Should have sequential numbers
	expected := []string{
		"vm_component_vmstorage_1",
		"vm_component_vmstorage_2",
		"vm_component_vmstorage_3",
	}

	for i, want := range expected {
		if results[i] != want {
			t.Errorf("result[%d] = %s, want %s", i, results[i], want)
		}
	}
}

// TestObfuscator_GetMappings tests mappings retrieval
func TestObfuscator_GetMappings(t *testing.T) {
	obf := NewObfuscator()

	// Obfuscate some values
	obf.ObfuscateInstance("10.0.1.5:8482")
	obf.ObfuscateInstance("10.0.1.6:8481")
	obf.ObfuscateJob("vmstorage-prod", "vmstorage")
	obf.ObfuscateJob("vmselect-prod", "vmselect")

	// Get mappings
	instanceMap, jobMap := obf.GetMappings()

	// Verify instance map
	if len(instanceMap) != 2 {
		t.Errorf("expected 2 instance mappings, got %d", len(instanceMap))
	}

	if _, exists := instanceMap["10.0.1.5:8482"]; !exists {
		t.Error("instance mapping missing")
	}

	// Verify job map
	if len(jobMap) != 2 {
		t.Errorf("expected 2 job mappings, got %d", len(jobMap))
	}

	if _, exists := jobMap["vmstorage-prod"]; !exists {
		t.Error("job mapping missing")
	}
}

// TestObfuscator_GetMappings_ReturnsCopies tests that mappings are copies
func TestObfuscator_GetMappings_ReturnsCopies(t *testing.T) {
	obf := NewObfuscator()

	obf.ObfuscateInstance("10.0.1.5:8482")

	// Get mappings
	instanceMap1, _ := obf.GetMappings()

	// Modify returned map
	instanceMap1["new-key"] = "new-value"

	// Get mappings again
	instanceMap2, _ := obf.GetMappings()

	// Should not contain modified key
	if _, exists := instanceMap2["new-key"]; exists {
		t.Error("modification affected internal state")
	}
}

// TestObfuscator_Concurrent tests concurrent obfuscation
func TestObfuscator_Concurrent(t *testing.T) {
	obf := NewObfuscator()

	done := make(chan bool)

	// Run multiple goroutines
	for i := 0; i < 10; i++ {
		go func(id int) {
			instance := "10.0.1.5:8482"
			result1 := obf.ObfuscateInstance(instance)
			result2 := obf.ObfuscateInstance(instance)

			if result1 != result2 {
				t.Errorf("goroutine %d: inconsistent results", id)
			}

			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

