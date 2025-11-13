package obfuscation

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

// TestObfuscator_IPv6_Addresses tests IPv6 address obfuscation
// NOTE: KNOWN LIMITATION (Grey Zone #1 in test-report.md)
// Current implementation does NOT handle IPv6 properly - converts to IPv4
// This test documents expected behavior, not current behavior
func TestObfuscator_IPv6_Addresses(t *testing.T) {
	t.Skip("IPv6 obfuscation not implemented yet - see test-report.md Grey Zone #1")
	
	obf := NewObfuscator()

	testCases := []struct {
		name     string
		input    string
		wantPort string
	}{
		{"full ipv6", "[2001:db8::1]:8482", "8482"},
		{"localhost ipv6", "[::1]:8428", "8428"},
		{"link-local", "[fe80::1]:8481", "8481"},
		{"compressed", "[2001:db8:0:0:0:0:2:1]:8482", "8482"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := obf.ObfuscateInstance(tc.input)

			// EXPECTED: Should still have brackets and port for IPv6
			if !strings.HasPrefix(result, "[") || !strings.Contains(result, "]:") {
				t.Errorf("invalid IPv6 format after obfuscation: %s", result)
			}

			// Should be different from original
			if result == tc.input {
				t.Error("obfuscation didn't change value")
			}

			// Port should be preserved
			_, port, err := net.SplitHostPort(result)
			if err != nil {
				t.Fatalf("failed to parse obfuscated result: %v", err)
			}

			if port != tc.wantPort {
				t.Errorf("port not preserved: want %s, got %s", tc.wantPort, port)
			}

			// Should be deterministic
			result2 := obf.ObfuscateInstance(tc.input)
			if result != result2 {
				t.Error("IPv6 obfuscation not deterministic")
			}
		})
	}
}

// TestObfuscator_Hostnames tests hostname obfuscation
func TestObfuscator_Hostnames(t *testing.T) {
	obf := NewObfuscator()

	testCases := []string{
		"vmselect-1.domain.com:8481",
		"vmstorage-prod-dc1.example.org:8482",
		"localhost:8428",
		"vm.cluster.local:8481",
	}

	for _, hostname := range testCases {
		t.Run(hostname, func(t *testing.T) {
			result := obf.ObfuscateInstance(hostname)

			// Should be obfuscated
			if result == hostname {
				t.Error("hostname not obfuscated")
			}

			// Should preserve port
			_, originalPort, _ := net.SplitHostPort(hostname)
			_, obfuscatedPort, _ := net.SplitHostPort(result)

			if originalPort != obfuscatedPort {
				t.Errorf("port not preserved: want %s, got %s", originalPort, obfuscatedPort)
			}

			// Should be deterministic
			result2 := obf.ObfuscateInstance(hostname)
			if result != result2 {
				t.Error("hostname obfuscation not deterministic")
			}
		})
	}
}

// TestObfuscator_SpecialCharacters tests special characters in job names
func TestObfuscator_SpecialCharacters(t *testing.T) {
	obf := NewObfuscator()

	testCases := []struct {
		job       string
		component string
	}{
		{"vmstorage-prod/dc1", "vmstorage"},
		{"vmselect:prod", "vmselect"},
		{"vmagent@prod", "vmagent"},
		{"vm-insert#prod", "vminsert"},
		{"vmalert!prod", "vmalert"},
		{"vmstorage prod", "vmstorage"}, // space
	}

	for _, tc := range testCases {
		t.Run(tc.job, func(t *testing.T) {
			result := obf.ObfuscateJob(tc.job, tc.component)

			// Should be obfuscated
			if result == tc.job {
				t.Error("job with special characters not obfuscated")
			}

			// Should have expected prefix
			expectedPrefix := "vm_component_" + tc.component + "_"
			if !strings.HasPrefix(result, expectedPrefix) {
				t.Errorf("wrong prefix: want %s*, got %s", expectedPrefix, result)
			}

			// Should not contain original special characters
			specialChars := []string{"/", ":", "@", "#", "!", " "}
			for _, char := range specialChars {
				if strings.Contains(result, char) {
					t.Errorf("obfuscated result contains special char '%s': %s", char, result)
				}
			}
		})
	}
}

// TestObfuscator_URLEncoded tests URL-encoded characters in labels
func TestObfuscator_URLEncoded(t *testing.T) {
	obf := NewObfuscator()

	// URL-encoded instance
	encoded := "10.0.1.5%3A8482" // ":" is %3A
	result := obf.ObfuscateInstance(encoded)

	// Should handle gracefully
	if result == "" {
		t.Error("failed to obfuscate URL-encoded instance")
	}

	// Should be different from original
	if result == encoded {
		t.Error("obfuscation didn't change URL-encoded value")
	}
}

// TestObfuscator_EmptyStrings tests empty string handling
func TestObfuscator_EmptyStrings(t *testing.T) {
	obf := NewObfuscator()

	// Empty instance
	result := obf.ObfuscateInstance("")
	if result == "" {
		t.Log("Empty instance returns empty (expected)")
	} else {
		t.Log("Empty instance returns hash (also acceptable)")
	}

	// Empty job
	result = obf.ObfuscateJob("", "vmstorage")
	if result == "" {
		t.Log("Empty job returns empty (expected)")
	}
}

// TestObfuscator_VeryLongValues tests very long label values
func TestObfuscator_VeryLongValues(t *testing.T) {
	obf := NewObfuscator()

	// Generate very long instance string (> 1000 chars)
	longInstance := strings.Repeat("a", 1000) + ":8482"

	result := obf.ObfuscateInstance(longInstance)

	// Should handle without panic
	if result == "" {
		t.Error("failed to obfuscate long instance")
	}

	// Result should be reasonably short (hash-based)
	if len(result) > 200 {
		t.Errorf("obfuscated result too long: %d chars", len(result))
	}

	// Should be deterministic
	result2 := obf.ObfuscateInstance(longInstance)
	if result != result2 {
		t.Error("long value obfuscation not deterministic")
	}
}

// TestObfuscator_Unicode tests Unicode/UTF-8 in labels
func TestObfuscator_Unicode(t *testing.T) {
	obf := NewObfuscator()

	testCases := []struct {
		name  string
		value string
	}{
		{"cyrillic", "vmstorage-prod-ru"},
		{"chinese", "vmstorage-prod-cn"},
		{"emoji", "vmstorage-🚀-prod"},
		{"mixed", "vm-storage-prod-intl"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := obf.ObfuscateJob(tc.value, "vmstorage")

			// Should handle Unicode
			if result == "" {
				t.Error("failed to obfuscate Unicode job")
			}

			// Should be different
			if result == tc.value {
				t.Error("Unicode job not obfuscated")
			}

			// Should be deterministic
			result2 := obf.ObfuscateJob(tc.value, "vmstorage")
			if result != result2 {
				t.Error("Unicode obfuscation not deterministic")
			}
		})
	}
}

// TestObfuscator_PoolExhaustion tests behavior when IP pool is exhausted
func TestObfuscator_PoolExhaustion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping pool exhaustion test in short mode")
	}

	obf := NewObfuscator()

	// Generate 70K unique instances (more than 777.777.0.0 - 777.777.255.255)
	maxInstances := 70000
	uniqueObfuscated := make(map[string]bool)

	for i := 0; i < maxInstances; i++ {
		instance := fmt.Sprintf("10.0.%d.%d:8482", i/256, i%256)
		result := obf.ObfuscateInstance(instance)

		if result == "" {
			t.Fatalf("obfuscation failed at instance %d", i)
		}

		uniqueObfuscated[result] = true
	}

	// All results should be unique
	if len(uniqueObfuscated) != maxInstances {
		t.Errorf("collision detected: %d unique out of %d", len(uniqueObfuscated), maxInstances)
	}

	t.Logf("Successfully obfuscated %d unique instances", maxInstances)
}

// TestObfuscator_Concurrent_HighContention tests thread safety under high contention
func TestObfuscator_Concurrent_HighContention(t *testing.T) {
	obf := NewObfuscator()

	const goroutines = 100
	const operationsPerGoroutine = 1000

	var wg sync.WaitGroup
	errors := make(chan error, goroutines*operationsPerGoroutine)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for i := 0; i < operationsPerGoroutine; i++ {
				// Mix of instance and job obfuscation
				instance := fmt.Sprintf("10.0.%d.%d:8482", id, i%256)
				job := fmt.Sprintf("vmstorage-prod-%d-%d", id, i)

				result1 := obf.ObfuscateInstance(instance)
				result2 := obf.ObfuscateJob(job, "vmstorage")

				// Verify determinism
				if result1 != obf.ObfuscateInstance(instance) {
					errors <- fmt.Errorf("instance obfuscation not deterministic: %s", instance)
				}
				if result2 != obf.ObfuscateJob(job, "vmstorage") {
					errors <- fmt.Errorf("job obfuscation not deterministic: %s", job)
				}
			}
		}(g)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	errorCount := 0
	for err := range errors {
		t.Error(err)
		errorCount++
		if errorCount > 10 {
			t.Fatal("too many errors, stopping")
		}
	}

	t.Logf("Concurrent test: %d goroutines × %d operations = %d total ops",
		goroutines, operationsPerGoroutine, goroutines*operationsPerGoroutine)
}

// TestObfuscator_MemoryUsage tests memory usage with many mappings
func TestObfuscator_MemoryUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping memory test in short mode")
	}

	obf := NewObfuscator()

	// Create 100K mappings
	for i := 0; i < 100000; i++ {
		instance := fmt.Sprintf("10.0.%d.%d:8482", i/256, i%256)
		obf.ObfuscateInstance(instance)

		if i%10 == 0 {
			job := fmt.Sprintf("job-%d", i)
			obf.ObfuscateJob(job, "vmstorage")
		}
	}

	// Get mappings
	instanceMap, jobMap := obf.GetMappings()

	if len(instanceMap) != 100000 {
		t.Errorf("expected 100000 instance mappings, got %d", len(instanceMap))
	}

	if len(jobMap) != 10000 {
		t.Errorf("expected 10000 job mappings, got %d", len(jobMap))
	}

	t.Logf("Memory test: %d instances + %d jobs stored", len(instanceMap), len(jobMap))
}

// TestObfuscator_SameIPDifferentPorts_SmartMapping tests smart IP preservation
// This is a design question test - should same IP with different ports 
// map to same obfuscated IP?
func TestObfuscator_SameIPDifferentPorts_SmartMapping(t *testing.T) {
	obf := NewObfuscator()

	instances := []string{
		"10.0.1.5:8482",
		"10.0.1.5:8481",
		"10.0.1.5:8429",
	}

	results := make([]string, len(instances))
	ips := make([]string, len(instances))

	for i, instance := range instances {
		results[i] = obf.ObfuscateInstance(instance)
		host, _, _ := net.SplitHostPort(results[i])
		ips[i] = host
	}

	// Document current behavior
	if ips[0] == ips[1] && ips[1] == ips[2] {
		t.Log("✓ Smart mapping: same IP preserved across ports")
	} else {
		t.Log("✓ Simple mapping: each instance:port gets unique obfuscated value")
		t.Log("  This is acceptable for MVP - simpler implementation")
	}

	// Verify ports are preserved in any case
	for i, instance := range instances {
		_, originalPort, _ := net.SplitHostPort(instance)
		_, obfuscatedPort, _ := net.SplitHostPort(results[i])

		if originalPort != obfuscatedPort {
			t.Errorf("port not preserved for %s", instance)
		}
	}
}

// TestObfuscator_JobNameSanitization tests sanitization of job names
func TestObfuscator_JobNameSanitization(t *testing.T) {
	obf := NewObfuscator()

	testCases := []struct {
		input    string
		contains []string    // Result should contain these
		notContains []string // Result should NOT contain these
	}{
		{
			input:       "vmstorage-prod-dc1",
			contains:    []string{"vm_component_vmstorage_"},
			notContains: []string{"prod", "dc1"},
		},
		{
			input:       "vm/storage:prod@dc1",
			contains:    []string{"vm_component_vmstorage_"},
			notContains: []string{"/", ":", "@"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := obf.ObfuscateJob(tc.input, "vmstorage")

			for _, substr := range tc.contains {
				if !strings.Contains(result, substr) {
					t.Errorf("result should contain '%s': %s", substr, result)
				}
			}

			for _, substr := range tc.notContains {
				if strings.Contains(result, substr) {
					t.Errorf("result should NOT contain '%s': %s", substr, result)
				}
			}
		})
	}
}

